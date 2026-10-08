package triagejob

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mikeschinkel/endless/internal/autospawnjob"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/sessionstate"
	"github.com/mikeschinkel/endless/internal/taskstatus"
)

// senderTimeout bounds one `claude -p` sender: it lists agents, sends one
// message and exits.
const senderTimeout = 3 * time.Minute

// senderDir names the sender's working folder. Claude Code names a session
// after its folder, so this is what a messaged session sees the message come
// from.
const senderDir = "endless-triage"

// realEffects is effects against the real database, tmux, Claude and the
// Python CLI.
type realEffects struct {
	db *sql.DB
}

func (realEffects) now() time.Time { return time.Now() }

func (fx realEffects) session(id int64) (s sessionInfo, err error) {
	var state, server string
	var task sql.NullInt64

	err = fx.db.QueryRow(`
		SELECT s.state, COALESCE(s.last_activity, ''), COALESCE(s.session_id, ''), s.task_id,
		       COALESCE(p.address, ''), COALESCE(p.server_uuid, '')
		  FROM sessions s
		  LEFT JOIN processes p ON p.id = s.process_id
		 WHERE s.id = ?`, id,
	).Scan(&state, &s.lastActivity, &s.uuid, &task, &s.pane, &server)
	if err == sql.ErrNoRows {
		return sessionInfo{}, nil
	}
	if err != nil {
		return s, fmt.Errorf("reading ES-%d: %w", id, err)
	}
	s.found = true
	s.taskID = task.Int64
	s.idle = state == string(sessionstate.Idle)

	liveness, err := monitor.SessionLiveness(id)
	if err != nil {
		return s, fmt.Errorf("observing ES-%d: %w", id, err)
	}
	s.unobservable = liveness == monitor.LivenessUnknown
	s.live = state != string(sessionstate.Ended) && !liveness.IsGone() && !s.unobservable

	// A pane id means something only on the server that issued it.
	here, _ := monitor.TmuxServerUUID()
	if !strings.HasPrefix(s.pane, "%") || here == "" || server != here {
		s.pane = ""
	}
	return s, nil
}

// transcriptExists reports whether Claude still holds the session's
// transcript: <$CLAUDE_CONFIG_DIR or ~/.claude>/projects/*/<uuid>.jsonl, the
// same place `session goto --resume` checks.
func (realEffects) transcriptExists(uuid string) bool {
	dir := os.Getenv("CLAUDE_CONFIG_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		dir = filepath.Join(home, ".claude")
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "projects", "*", uuid+".jsonl"))
	return len(matches) > 0
}

// deliver runs the throwaway sender (E-2269's PoC): a headless Claude allowed
// only the three tools a cross-session message needs, run from a folder named
// for what it is, with hooks off so it never registers as an Endless session.
//
// The prompt comes BEFORE --allowedTools: that flag takes several values and
// swallows a prompt that follows it.
func (realEffects) deliver(ctx context.Context, pane, message string) (d delivery, err error) {
	var bin, dir string
	var out []byte
	var cancel context.CancelFunc

	bin, err = exec.LookPath("claude")
	if err != nil {
		return failed, fmt.Errorf("%w: claude is not on PATH", err)
	}
	dir = filepath.Join(monitor.ConfigDir(), senderDir)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return failed, fmt.Errorf("creating the sender's folder: %w", err)
	}

	ctx, cancel = context.WithTimeout(ctx, senderTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-p", senderPrompt(pane, message),
		"--settings", `{"disableAllHooks":true}`,
		"--allowedTools", "SendMessage,ListAgents,ToolSearch")
	cmd.Dir = dir
	cmd.Env = childEnv()
	out, err = cmd.Output()
	if err != nil {
		return failed, fmt.Errorf("claude -p sender: %w: %s", err, tailOf(string(out)))
	}
	return parseDelivery(string(out)), nil
}

// inFlight are the statuses of a fix task still being worked: started or
// waiting to be, and not yet handed off as unverified.
var inFlight = quoted(taskstatus.Unplanned, taskstatus.Submitted, taskstatus.Ready,
	taskstatus.Underway, taskstatus.Revisit)

func quoted(statuses ...taskstatus.Status) string {
	parts := make([]string, len(statuses))
	for i, s := range statuses {
		parts[i] = "'" + string(s) + "'"
	}
	return strings.Join(parts, ", ")
}

// startAllowed is the throttle: one outstanding started session at a time,
// machine-wide — a resumed session that has not answered, or a spawned fix task
// not yet unverified or beyond — and a tmux client attached to see it.
func (fx realEffects) startAllowed() (ok bool, why string, err error) {
	var errorID, fixTask int64
	var state string

	err = fx.db.QueryRow(`
		SELECT t.error_id, t.state, COALESCE(t.fix_task_id, 0)
		  FROM error_triage t
		 WHERE t.state = 'resumed'
		    OR (t.state = 'spawned' AND EXISTS (
		        SELECT 1 FROM tasks k
		         WHERE k.id = t.fix_task_id
		           AND k.status IN (`+inFlight+`)))
		 LIMIT 1`).Scan(&errorID, &state, &fixTask)
	switch {
	case err == sql.ErrNoRows:
		err = nil
	case err != nil:
		return false, "", fmt.Errorf("checking the triage throttle: %w", err)
	case state == "resumed":
		return false, fmt.Sprintf("a resumed session has not answered error %d yet", errorID), nil
	default:
		return false, fmt.Sprintf("fix task E-%d (error %d) is still being worked", fixTask, errorID), nil
	}

	_, why, err = autospawnjob.ResolveWindow()
	if err != nil || why != "" {
		return false, why, err
	}
	return true, "", nil
}

// resume reopens an ended session in a new, unfocused tmux window with the
// triage message as its first prompt, through `session goto --resume`'s own
// path — its transcript check, window identity and layout — never reopening
// the session's task: the session sets revisit itself if it accepts.
func (realEffects) resume(ctx context.Context, sessionID int64, projectPath, message string) (err error) {
	var f *os.File
	var w autospawnjob.Window

	w, _, err = autospawnjob.ResolveWindow()
	if err != nil {
		return err
	}
	if w.Target == "" {
		w.Target, err = monitorSession()
		if err != nil {
			return err
		}
	}
	f, err = os.CreateTemp("", "endless-triage-prompt-*.md")
	if err != nil {
		return fmt.Errorf("writing the resume prompt: %w", err)
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(message)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("writing the resume prompt: %w", err)
	}

	_, err = autospawnjob.RunEndless(ctx, projectPath, childEnv(), []string{
		"--no-session", "session", "goto", "ES-" + strconv.FormatInt(sessionID, 10),
		"--resume", "--no-revisit", "--background",
		"--target-session", w.Target, "--prompt-file", f.Name(),
	})
	return err
}

// monitorSession is the tmux session the runner's own pane is in, for an
// auto_spawn.target of monitor.
func monitorSession() (string, error) {
	pane := os.Getenv("TMUX_PANE")
	out, err := exec.Command("tmux", "display-message", "-p", "-t", pane, "#{session_id}").Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return "", fmt.Errorf("resolving the runner's tmux session from pane %q: %v", pane, err)
	}
	return strings.TrimSpace(string(out)), nil
}

var addedRE = regexp.MustCompile(`Added E-(\d+)`)

// fileTask files a bugfix task through the Python CLI, as the system: unrated,
// so the rater job rates it, which is why the child's environment carries no
// agent markers — an agent attaching a plan must rate it in the same call.
func (realEffects) fileTask(ctx context.Context, projectPath string, spec taskSpec) (taskID int64, err error) {
	var contextFile, planFile, descFile string
	var out []byte

	dir, err := os.MkdirTemp("", "endless-triage-task-*")
	if err != nil {
		return 0, fmt.Errorf("staging the bugfix task: %w", err)
	}
	defer os.RemoveAll(dir)
	contextFile, planFile, descFile = filepath.Join(dir, "context.md"), filepath.Join(dir, "plan.md"), filepath.Join(dir, "description.md")
	for path, text := range map[string]string{contextFile: spec.context, planFile: spec.plan, descFile: spec.description} {
		if err = os.WriteFile(path, []byte(text), 0o644); err != nil {
			return 0, fmt.Errorf("staging the bugfix task: %w", err)
		}
	}

	args := []string{"--no-session", "task", "add", spec.title,
		"--type", "bugfix", "--phase", "now",
		"--description-file", descFile, "--context-file", contextFile, "--plan-file", planFile}
	for _, id := range spec.cleansUp {
		args = append(args, "--cleans-up", "E-"+strconv.FormatInt(id, 10))
	}
	out, err = autospawnjob.RunEndless(ctx, projectPath, childEnv(), args)
	if err != nil {
		return 0, err
	}
	m := addedRE.FindStringSubmatch(string(out))
	if m == nil {
		return 0, fmt.Errorf("task add: no new task id in its output: %s", tailOf(string(out)))
	}
	return strconv.ParseInt(m[1], 10, 64)
}

// updateContext rewrites a fix task's context. A context edit never changes
// status.
func (realEffects) updateContext(ctx context.Context, projectPath string, taskID int64, text string) (err error) {
	f, err := os.CreateTemp("", "endless-triage-context-*.md")
	if err != nil {
		return fmt.Errorf("staging E-%d's context: %w", taskID, err)
	}
	defer os.Remove(f.Name())
	_, err = f.WriteString(text)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("staging E-%d's context: %w", taskID, err)
	}
	_, err = autospawnjob.RunEndless(ctx, projectPath, childEnv(), []string{
		"--no-session", "task", "update", "E-" + strconv.FormatInt(taskID, 10), "--context-file", f.Name(),
	})
	return err
}

// spawn starts a session on a fix task exactly as auto-spawn does: detached,
// into the user's auto_spawn target and placement.
func (realEffects) spawn(ctx context.Context, projectPath string, taskID int64) (where string, err error) {
	w, skip, err := autospawnjob.ResolveWindow()
	if err != nil {
		return "", err
	}
	if skip != "" {
		return "", fmt.Errorf("E-%d: %s", taskID, skip)
	}
	_, err = autospawnjob.RunEndless(ctx, projectPath, childEnv(), autospawnjob.SpawnArgs(taskID, w))
	return w.Describe(), err
}

func (fx realEffects) fixTask(taskID int64) (settled, claimed bool, err error) {
	var status string
	err = fx.db.QueryRow(`SELECT status, EXISTS (SELECT 1 FROM sessions s WHERE s.task_id = t.id)
	                        FROM tasks t WHERE t.id = ?`, taskID).Scan(&status, &claimed)
	if err == sql.ErrNoRows {
		// Removed: as good as settled, and nobody holds it.
		return true, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("reading fix task E-%d: %w", taskID, err)
	}
	return taskstatus.Has(taskstatus.Terminal, taskstatus.Status(status)), claimed, nil
}

// agentMarkers are the variables by which Endless and Claude Code recognise a
// process as an agent's, or as one particular session's. A child of this job
// is neither — the job is the system acting — so they are stripped: an
// inherited CLAUDECODE would make `task add` demand the ratings only an agent
// must give, and an inherited session id would credit the child's events to
// whichever session happened to run `endless jobs run`.
var agentMarkers = []string{"CLAUDECODE", "CLAUDE_CODE_SESSION_ID", "CLAUDE_CODE_ENTRYPOINT",
	"AI_AGENT", "ENDLESS_AUDIENCE", "ENDLESS_SESSION_ID"}

func childEnv() (env []string) {
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		strip := false
		for _, m := range agentMarkers {
			if name == m {
				strip = true
				break
			}
		}
		if !strip {
			env = append(env, kv)
		}
	}
	return env
}

const tailLimit = 2000

func tailOf(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > tailLimit {
		s = "…" + s[len(s)-tailLimit:]
	}
	return s
}
