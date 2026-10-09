package verifycmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/mikeschinkel/endless/internal/agentenv"
	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/verify"
	"github.com/mikeschinkel/go-doterr"
	"github.com/mikeschinkel/go-dt"
)

// A suite runs as a PERSON, whoever starts it (E-2278).
//
// Before this, a suite ran as whoever called the runner. Isolation replaced only
// HOME and XDG_CONFIG_HOME, so an agent's identity variables reached every
// check: the harness's own, the audience the front door exported, the session it
// routed through, and the tmux pane it sat in. The same suite then tested the
// agent's experience when an agent ran it and the person's when a person did,
// and gave the two of them different verdicts — 14 of 32 "passed for the agent,
// failed for the user" cases in E-2266 had exactly that cause.
//
// So the runner decides who a check is, rather than inheriting it. The default
// is a person; a check that tests the agent's side says so (`as_agent` in a
// script suite, `as = "agent"` on a manifest check) and gets a synthesized agent
// that is identical for every caller. Nothing here is specific to Endless as the
// system under test: in any project, a tool that changes behaviour under
// CLAUDECODE diverges the same way, and no project's suite should reach the
// user's live tmux server.

// Environment the runner exports so a suite can opt a check into the agent's
// side or into tmux.
const (
	// EnvAgentEnvFile names a file of KEY=VALUE lines, one per line: the agent's
	// environment, for `as_agent` to layer onto the person default.
	EnvAgentEnvFile = "ENDLESS_VERIFY_AGENT_ENV"

	// EnvTmuxTmpdir is tmux's own variable for where its sockets live. Pointing
	// it at a per-run directory gives every run a private, initially empty tmux
	// server, which is what `with_tmux` starts a fixture pane on.
	EnvTmuxTmpdir = "TMUX_TMPDIR"
)

// agentEnvFile is the per-run file EnvAgentEnvFile points at.
const agentEnvFile = "agent.env"

// fixtureSessionID is the harness session id the synthesized agent carries.
// Fixed, not drawn per run and never copied from the caller, so an `as_agent`
// check resolves the same session whoever runs the suite.
const fixtureSessionID = "00000000-0000-4000-8000-000000000001"

// fixtureTmuxSession names the session a tmux = true check's fixture pane lives
// in, on the run's private server. _harness.sh's with_tmux uses the same name.
const fixtureTmuxSession = "endless-verify"

// tmuxTmpRoot is where the private tmux directory is made. NOT the OS temp dir:
// on macOS that is /var/folders/…/T/, and once canonicalized the socket path
// tmux builds under it (<dir>/tmux-<uid>/default) passes the ~104-byte limit on
// a Unix socket path and fails with "File name too long". Under /tmp it is
// about 51 bytes on any machine and independent of HOME. Not under the isolated
// HOME either: a socket is runtime state, not config (E-2186).
const tmuxTmpRoot = "/tmp"

// callerIdentityVars are the variables, beyond the harness's own, by which a
// process says WHO is running it. Each is stripped from the person default:
//
//   - the audience the Python front door exports (refusal.AudienceVar), which
//     turns every refusal and footer into its agent form;
//   - the session a shell was routed through (`esu` exports it);
//   - the tmux pane the caller sits in. Unsetting these alone isolates nothing
//     — tmux with neither set still connects to the default socket — so the run
//     also gets a private server through EnvTmuxTmpdir, replaced below.
var callerIdentityVars = []string{
	refusal.AudienceVar,
	"ENDLESS_SESSION_ID",
	"TMUX",
	"TMUX_PANE",
	EnvTmuxTmpdir,
}

// isCallerIdentity reports whether name says who is running the suite. The
// harness's names come from agentenv, beside the detector table that reads
// them, so the two cannot drift.
func isCallerIdentity(name string) bool {
	return agentenv.IsHarnessVar(name) || slices.Contains(callerIdentityVars, name)
}

// personEnv returns base with every caller-identity variable removed and the
// run's private tmux directory in place. It keeps the real HOME: the sandbox
// reset runs under it before isolation, because Endless's seed hook reads the
// main database.
func personEnv(base []string, tmuxDir dt.DirPath) (env []string) {
	env = make([]string, 0, len(base)+1)
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if isCallerIdentity(name) {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, EnvTmuxTmpdir+"="+string(tmuxDir))
	return env
}

// agentEnv returns the synthesized agent's variables as sorted KEY=VALUE pairs:
// the supported harness as agentenv observed it, with the fixture session id,
// plus the agent audience. Sorted so the file and every check see one order.
func agentEnv() (kvs []string) {
	for k, v := range agentenv.ObservedCLIEnv(fixtureSessionID) {
		kvs = append(kvs, k+"="+v)
	}
	kvs = append(kvs, refusal.AudienceVar+"="+refusal.AudienceAgent)
	sort.Strings(kvs)
	return kvs
}

// writeAgentEnv writes agentEnv to the run dir for `as_agent` to read.
func writeAgentEnv(runDir dt.DirPath) (fp dt.Filepath, err error) {
	fp = dt.FilepathJoin(runDir, agentEnvFile)
	err = fp.WriteFile([]byte(strings.Join(agentEnv(), "\n")+"\n"), 0o644)
	if err != nil {
		err = doterr.NewErr(ErrIsolatingEnv, err, "filepath", fp)
	}
	return fp, err
}

// makeTmuxDir creates the run's private TMUX_TMPDIR under tmuxTmpRoot,
// canonicalized for the same reason makeRunDir's is.
func makeTmuxDir() (dir dt.DirPath, err error) {
	var s, resolved string
	var rerr error

	s, err = os.MkdirTemp(tmuxTmpRoot, "endless-verify-")
	if err != nil {
		err = doterr.NewErr(ErrMakingRunDir, err, "root", tmuxTmpRoot)
		goto end
	}
	resolved, rerr = filepath.EvalSymlinks(s)
	if rerr == nil {
		s = resolved
	}
	dir = dt.DirPath(s)
end:
	return dir, err
}

// tmuxCommand builds a tmux invocation on the private server under tmuxDir. TMUX
// and TMUX_PANE are dropped from env, so the call never reaches the caller's
// server and tmux never treats it as nested.
func tmuxCommand(env []string, tmuxDir dt.DirPath, args ...string) (cmd *exec.Cmd) {
	var clean []string

	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if name == "TMUX" || name == "TMUX_PANE" {
			continue
		}
		clean = append(clean, kv)
	}
	cmd = exec.Command("tmux", args...)
	cmd.Env = overrideEnv(clean, []string{EnvTmuxTmpdir + "=" + string(tmuxDir)})
	return cmd
}

// fixturePaneEnv starts the fixture session on the private server, if it is
// not already running, and returns TMUX and TMUX_PANE naming its pane — what a
// process sitting in that pane would see. The pane runs `sh`, not the caller's
// shell, so it is the same pane whoever runs the suite.
func fixturePaneEnv(env []string, tmuxDir dt.DirPath) (kvs []string, err error) {
	var out []byte
	var fields []string

	if tmuxCommand(env, tmuxDir, "has-session", "-t", fixtureTmuxSession).Run() != nil {
		out, err = tmuxCommand(env, tmuxDir,
			"new-session", "-d", "-s", fixtureTmuxSession, "-x", "200", "-y", "50", "sh").CombinedOutput()
		if err != nil {
			err = doterr.NewErr(ErrFixtureTmux, err, "output", tail(out))
			goto end
		}
	}
	out, err = tmuxCommand(env, tmuxDir, "display-message", "-p", "-t", fixtureTmuxSession+":",
		"#{socket_path},#{pid},#{session_id} #{pane_id}").Output()
	if err != nil {
		err = doterr.NewErr(ErrFixtureTmux, err)
		goto end
	}
	fields = strings.Fields(string(bytes.TrimSpace(out)))
	if len(fields) != 2 {
		err = doterr.NewErr(ErrFixtureTmux, "output", string(out))
		goto end
	}
	// tmux's own TMUX value is socket,pid,session-index; display-message gives
	// the session as "$N".
	kvs = []string{
		"TMUX=" + strings.Replace(fields[0], ",$", ",", 1),
		"TMUX_PANE=" + fields[1],
	}
end:
	return kvs, err
}

// killPrivateTmux stops whatever server a run started on its private socket and
// removes the directory. Both are best-effort and silent: with no server
// running, or no tmux installed, kill-server fails and there is nothing to stop.
func killPrivateTmux(tmuxDir dt.DirPath) {
	if tmuxDir == "" {
		return
	}
	_ = tmuxCommand(os.Environ(), tmuxDir, "kill-server").Run()
	_ = tmuxDir.RemoveAll()
}

// snapshotScript copies the suite into the run dir and returns the copy's path,
// so the run executes a snapshot rather than the file in the worktree. bash
// reads a script as it runs it, so an edit to verify.sh during somebody else's
// run broke that run mid-way (E-2232: "syntax error near unexpected token").
//
// The copy keeps the layout a suite sources through: the suite's whole
// directory, plus the shared files beside it (_harness.sh and the _guard.sh it
// sources), at the same relative paths under <runDir>/snapshot.
//
// The copy is outside any worktree, so _guard.sh's "not yours" check — which
// reads the worktree from the running file's path — fails open on it. Nothing is
// lost: guardOwnTaskOnly has already refused a foreign suite before anything
// ran. ENDLESS_VERIFY_DIR still names the real suite directory.
func snapshotScript(script dt.Filepath, runDir dt.DirPath) (copied dt.Filepath, err error) {
	var suiteDir, tasksDir, dstTasks, dstSuite dt.DirPath
	var entries []os.DirEntry
	var data []byte
	var info os.FileInfo

	suiteDir = script.Dir()
	tasksDir = suiteDir.Dir()
	dstTasks = runDir.Join("snapshot", verify.SuitesDir)
	dstSuite = dstTasks.Join(string(suiteDir.Base()))

	err = os.MkdirAll(string(dstTasks), 0o755)
	if err != nil {
		goto end
	}
	entries, err = os.ReadDir(string(tasksDir))
	if err != nil {
		goto end
	}
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		src := filepath.Join(string(tasksDir), e.Name())
		data, err = os.ReadFile(src)
		if err != nil {
			goto end
		}
		info, err = e.Info()
		if err != nil {
			goto end
		}
		err = os.WriteFile(filepath.Join(string(dstTasks), e.Name()), data, info.Mode().Perm())
		if err != nil {
			goto end
		}
	}
	err = os.CopyFS(string(dstSuite), os.DirFS(string(suiteDir)))
	if err != nil {
		goto end
	}
	copied = dt.FilepathJoin(dstSuite, script.Base())
end:
	if err != nil {
		err = doterr.NewErr(ErrSnapshottingSuite, err, "suite", suiteDir, "dir", runDir)
	}
	return copied, err
}
