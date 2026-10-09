package verifycmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/agentenv"
	"github.com/mikeschinkel/go-dt"
)

// callerIdentity is an agent's shell, as the runner used to hand it to a suite:
// the harness's own variables, the audience the front door exported, a routed
// session, and the caller's tmux pane.
var callerIdentity = map[string]string{
	"CLAUDECODE":             "1",
	"CLAUDE_CODE_ENTRYPOINT": "cli",
	"CLAUDE_CODE_SESSION_ID": "6f53f9d3-c73e-4f9e-b04f-fcd3742290f1",
	"CLAUDE_PID":             "123",
	"AI_AGENT":               "claude-code_2-1-222_agent",
	"__CFBundleIdentifier":   "com.apple.Terminal",
	"ENDLESS_AUDIENCE":       "agent",
	"ENDLESS_SESSION_ID":     "1306",
	"TMUX":                   "/tmp/tmux-501/default,1,0",
	"TMUX_PANE":              "%413",
	"TMUX_TMPDIR":            "/somewhere/else",
}

// setCallerIdentity puts callerIdentity into this test's environment.
func setCallerIdentity(t *testing.T) {
	t.Helper()
	for k, v := range callerIdentity {
		t.Setenv(k, v)
	}
}

// envNames returns the variable names in env.
func envNames(env []string) (names []string) {
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		names = append(names, name)
	}
	return names
}

// The person default strips every identity variable the caller carries, keeps
// everything else, and points tmux at the run's private directory.
func TestPersonEnv_StripsTheCallersIdentity(t *testing.T) {
	var base []string
	for k, v := range callerIdentity {
		base = append(base, k+"="+v)
	}
	base = append(base, "PATH=/usr/bin", "HOME=/Users/someone", "TERM=xterm")

	env := personEnv(base, dt.DirPath("/tmp/endless-verify-1"))

	names := envNames(env)
	for k := range callerIdentity {
		if k == EnvTmuxTmpdir {
			continue
		}
		if slices.Contains(names, k) {
			t.Errorf("person env kept the caller's %s", k)
		}
	}
	for _, want := range []string{"PATH=/usr/bin", "HOME=/Users/someone", "TERM=xterm",
		"TMUX_TMPDIR=/tmp/endless-verify-1"} {
		if !slices.Contains(env, want) {
			t.Errorf("person env lacks %q", want)
		}
	}
	if n := strings.Count(strings.Join(names, " "), EnvTmuxTmpdir); n != 1 {
		t.Errorf("TMUX_TMPDIR appears %d times, want once", n)
	}
}

// Every variable the synthesized agent sets is one the person default strips —
// otherwise `as_agent` would set something the caller could have leaked too.
func TestAgentEnv_IsTheSupportedHarness(t *testing.T) {
	vars := map[string]string{}
	for _, kv := range agentEnv() {
		k, v, _ := strings.Cut(kv, "=")
		vars[k] = v
		if !isCallerIdentity(k) {
			t.Errorf("agent env sets %s, which the person default does not strip", k)
		}
	}
	if got := agentenv.DetectWith(func(k string) string { return vars[k] }); got != agentenv.ClaudeCLI {
		t.Errorf("agent env is detected as %q, want %q", got, agentenv.ClaudeCLI)
	}
	if vars["ENDLESS_AUDIENCE"] != "agent" {
		t.Errorf("agent env audience = %q, want agent", vars["ENDLESS_AUDIENCE"])
	}
	if vars["CLAUDE_CODE_SESSION_ID"] != fixtureSessionID {
		t.Errorf("agent session = %q, want the fixed fixture %q", vars["CLAUDE_CODE_SESSION_ID"], fixtureSessionID)
	}
}

// The file as_agent reads holds exactly agentEnv, one pair per line.
func TestWriteAgentEnv(t *testing.T) {
	dir := dt.DirPath(t.TempDir())
	fp, err := writeAgentEnv(dir)
	if err != nil {
		t.Fatalf("writeAgentEnv: %v", err)
	}
	data, err := os.ReadFile(string(fp))
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if !slices.Equal(got, agentEnv()) {
		t.Errorf("agent env file = %q, want %q", got, agentEnv())
	}
}

// The private tmux directory is short enough for a Unix socket path on any
// machine, which a dir under macOS's $TMPDIR is not.
func TestMakeTmuxDir_FitsASocketPath(t *testing.T) {
	dir, err := makeTmuxDir()
	if err != nil {
		t.Fatalf("makeTmuxDir: %v", err)
	}
	defer dir.RemoveAll()
	sock := filepath.Join(string(dir), "tmux-99999", "default")
	if len(sock) > 100 {
		t.Errorf("socket path is %d bytes (> 100): %s", len(sock), sock)
	}
}

// The snapshot keeps the layout a suite sources through: its own directory and
// the shared files beside it, at the same relative paths.
func TestSnapshotScript_KeepsTheLayout(t *testing.T) {
	root := t.TempDir()
	tasks := filepath.Join(root, ".endless", "tasks")
	suite := filepath.Join(tasks, "e-7")
	for path, body := range map[string]string{
		filepath.Join(tasks, "_harness.sh"): "harness",
		filepath.Join(tasks, "_guard.sh"):   "guard",
		filepath.Join(suite, "verify.sh"):   "#!/usr/bin/env bash\n",
		filepath.Join(suite, "fixture.txt"): "beside",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runDir := dt.DirPath(t.TempDir())

	copied, err := snapshotScript(dt.Filepath(filepath.Join(suite, "verify.sh")), runDir)
	if err != nil {
		t.Fatalf("snapshotScript: %v", err)
	}
	want := filepath.Join(string(runDir), "snapshot", ".endless", "tasks", "e-7", "verify.sh")
	if string(copied) != want {
		t.Errorf("copy = %s, want %s", copied, want)
	}
	info, err := os.Stat(want)
	if err != nil || info.Mode()&0o100 == 0 {
		t.Errorf("copied suite is missing or not executable: %v", err)
	}
	for _, rel := range []string{"_harness.sh", "_guard.sh", "e-7/fixture.txt"} {
		if _, err := os.Stat(filepath.Join(string(runDir), "snapshot", ".endless", "tasks", rel)); err != nil {
			t.Errorf("snapshot lacks %s: %v", rel, err)
		}
	}
}

// A suite that rewrites its own verify.sh mid-run still completes as the
// original script (E-2232). Without the snapshot, bash reads the rewritten bytes
// at its current offset and the run dies on a syntax error.
func TestRun_ScriptSuite_RunsASnapshot(t *testing.T) {
	// The rewrite is IN PLACE, as an editor's save often is — a rename would
	// leave bash reading the old inode. It is a long run of comment lines and
	// then `exit 7`, so wherever
	// bash's read offset sits when it resumes, the live file hands it a comment
	// and then the exit — which is what a run of the worktree file would do.
	enterScriptSuite(t, "E-8", `#!/usr/bin/env bash
{ for i in $(seq 1 200); do printf '%079d\n' 0 | tr 0 '#'; done; echo 'exit 7'; } \
    > "$ENDLESS_VERIFY_DIR/verify.sh"
echo "the original carried on"
exit 0
`)
	code, err := run("E-8", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0 from the original script", code)
	}
}

// The same suite gives the same verdict from an agent's shell and from a
// person's: no identity variable reaches it, and its agent side is the one the
// runner synthesizes, not the caller's.
func TestRun_ScriptSuite_SameVerdictWhoeverRunsIt(t *testing.T) {
	const body = `#!/usr/bin/env bash
for v in CLAUDECODE CLAUDE_CODE_ENTRYPOINT CLAUDE_CODE_SESSION_ID CLAUDE_PID AI_AGENT \
         __CFBundleIdentifier ENDLESS_AUDIENCE ENDLESS_SESSION_ID TMUX TMUX_PANE; do
    [[ -z "${!v:-}" ]] || { echo "leaked $v"; exit 11; }
done
[[ "$TMUX_TMPDIR" == */endless-verify-* ]] || exit 12
[[ -f "$ENDLESS_VERIFY_AGENT_ENV" ]] || exit 13
grep -qx "CLAUDE_CODE_SESSION_ID=` + fixtureSessionID + `" "$ENDLESS_VERIFY_AGENT_ENV" || exit 14
grep -qx "ENDLESS_AUDIENCE=agent" "$ENDLESS_VERIFY_AGENT_ENV" || exit 15
exit 0
`
	t.Run("person", func(t *testing.T) {
		for k := range callerIdentity {
			t.Setenv(k, "")
			os.Unsetenv(k)
		}
		enterScriptSuite(t, "E-9", body)
		code, err := run("E-9", false)
		if err != nil || code != 0 {
			t.Errorf("run = %d, %v; want 0", code, err)
		}
	})
	t.Run("agent", func(t *testing.T) {
		setCallerIdentity(t)
		enterScriptSuite(t, "E-9", body)
		code, err := run("E-9", false)
		if err != nil || code != 0 {
			t.Errorf("run = %d, %v; want 0 (11 = an identity variable leaked)", code, err)
		}
	})
}

// A manifest check with as = "agent" sees the agent; its sibling does not.
func TestRun_Manifest_AsAgentIsPerCheck(t *testing.T) {
	setCallerIdentity(t)
	enterSuite(t, "E-AS", `
schema = 1
task   = "E-AS"
[[check]]
runner  = "bats"
command = "if [ \"${CLAUDECODE:-}\" = 1 ] && [ \"${ENDLESS_AUDIENCE:-}\" = agent ]; then echo 'ok 1 agent'; else echo 'not ok 1 agent'; fi; echo 1..1"
as      = "agent"
[[check]]
runner  = "bats"
command = "if [ -z \"${CLAUDECODE:-}\" ] && [ -z \"${ENDLESS_AUDIENCE:-}\" ]; then echo 'ok 1 person'; else echo 'not ok 1 person'; fi; echo 1..1"
`)
	code, err := run("E-AS", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}

// The private server goes when the run does — after a FAILING suite too — and
// the caller's live server is never the one a suite reached.
func TestRun_PrivateTmuxIsTornDown(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	out := filepath.Join(t.TempDir(), "tmuxdir")
	t.Setenv("E2278_OUT", out)
	enterScriptSuite(t, "E-10", `#!/usr/bin/env bash
printf '%s' "$TMUX_TMPDIR" > "$E2278_OUT"
tmux new-session -d -s probe sh || exit 3
tmux has-session -t probe || exit 4
exit 1
`)
	code, err := run("E-10", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 1 {
		t.Fatalf("exit code = %d, want the suite's 1 (3/4 = tmux did not start)", code)
	}
	dir, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(dir), "/") {
		t.Fatalf("suite saw TMUX_TMPDIR %q", dir)
	}
	if _, err := os.Stat(string(dir)); !os.IsNotExist(err) {
		t.Errorf("private tmux dir %s outlived the run: %v", dir, err)
	}
	probe := exec.Command("tmux", "has-session", "-t", "probe")
	probe.Env = append(os.Environ(), "TMUX_TMPDIR="+string(dir))
	if probe.Run() == nil {
		t.Errorf("the private tmux server outlived the run")
	}
}

// A tmux = true check runs in the fixture pane; its sibling runs outside tmux.
func TestRun_Manifest_TmuxIsPerCheck(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	setCallerIdentity(t)
	enterSuite(t, "E-TM", `
schema = 1
task   = "E-TM"
[[check]]
runner  = "bats"
command = "case \"${TMUX_PANE:-}\" in %*) [ \"$(tmux display-message -p '#{session_name}')\" = endless-verify ] && echo 'ok 1 in pane' || echo 'not ok 1 in pane';; *) echo 'not ok 1 in pane';; esac; echo 1..1"
tmux    = true
[[check]]
runner  = "bats"
command = "if [ -z \"${TMUX:-}\" ] && [ -z \"${TMUX_PANE:-}\" ]; then echo 'ok 1 outside'; else echo 'not ok 1 outside'; fi; echo 1..1"
`)
	code, err := run("E-TM", false)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}
