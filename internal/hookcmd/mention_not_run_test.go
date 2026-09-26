package hookcmd

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// E-2177: the PostToolUse hook must not infer state changes from the text of a
// Bash command. The CLI and the event executor own the claim bind and the
// confirm; the hook used to duplicate both by regex-matching the whole command
// string, so a heredoc, a quoted argument or a commit message that merely
// MENTIONED a verb performed its effect.
//
// Every trigger string below is assembled at runtime. A literal claim or confirm
// of a numeric id in this file — or in the Bash command that writes it — fires
// the very bug these tests pin (which is how the bug was found).

var (
	verbClaim   = "cl" + "aim"
	verbConfirm = "con" + "firm"
)

// taskVerb renders `endless task <verb> E-<id>` without spelling it literally.
func taskVerb(verb, id string) string {
	return strings.Join([]string{"endless", "task", verb, "E-" + id}, " ")
}

// TestPostToolUse_HeredocMentionOfClaimDoesNotBind: a heredoc body naming a claim
// of a real, unheld task is bytes written to a file. The session stays unbound
// and the task's status is unchanged.
func TestPostToolUse_HeredocMentionOfClaimDoesNotBind(t *testing.T) {
	fx := newMentionFixture(t)
	cmd := "cat > notes.md <<'EOF'\nTo start, run " + taskVerb(verbClaim, "10") + "\nEOF"

	fx.postToolUse(t, cmd)

	fx.assertUnbound(t)
	fx.assertStatus(t, "ready")
}

// TestPostToolUse_UnattendedClaimDoesNotBind: `--unattended` (E-2093) means "no
// Claude session bound". Run as a Bash tool call from a Claude session, the hook
// used to bind the session anyway, overriding the flag.
func TestPostToolUse_UnattendedClaimDoesNotBind(t *testing.T) {
	fx := newMentionFixture(t)

	fx.postToolUse(t, taskVerb(verbClaim, "10")+" --unattended")

	fx.assertUnbound(t)
}

// TestPostToolUse_CommitMessageMentionOfConfirmDoesNotConfirm: a commit message
// naming a confirm must not mark the task confirmed.
func TestPostToolUse_CommitMessageMentionOfConfirmDoesNotConfirm(t *testing.T) {
	fx := newMentionFixture(t)
	cmd := `git commit -m "Mike will run ` + taskVerb(verbConfirm, "10") + ` after review"`

	fx.postToolUse(t, cmd)

	fx.assertStatus(t, "ready")
}

// ─── fixture ────────────────────────────────────────────────────────────────

type mentionFixture struct {
	db   *sql.DB
	root string
}

// newMentionFixture seeds a registered project, a research task 10 in `ready`
// and a live session that is NOT bound to any task, and writes the shipped
// start/confirm action matchers into the project config — the configuration
// under which the hook used to act on text. The matchers are written even
// though nothing reads them any more: these tests must fail if anything starts
// reading them again.
func newMentionFixture(t *testing.T) *mentionFixture {
	t.Helper()
	db := newSchemaDB(t)
	t.Cleanup(monitor.SetTestDB(db))

	root, err := monitor.ResolvedProjectPath(t.TempDir())
	if err != nil {
		t.Fatalf("ResolvedProjectPath: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".endless", "worktrees", "e-10"), 0755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	cfg := `{"matchers":[` +
		`{"type":"start","scope":"task","method":"regex",` +
		`"match":"endless\\s+task\\s+` + verbClaim + `\\s+(?:[Ee]-)?(\\d+)"},` +
		`{"type":"` + verbConfirm + `","scope":"task","method":"regex",` +
		`"match":"endless\\s+task\\s+` + verbConfirm + `\\s+(?:[Ee]-)?(\\d+)"}]}`
	if err := os.WriteFile(filepath.Join(root, ".endless", "config.json"), []byte(cfg), 0644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}

	for _, q := range []string{
		"INSERT INTO projects (id, name, path) VALUES (1, 'p', '" + root + "')",
		"INSERT INTO tasks (id, project_id, title, status, type_id) VALUES (10, 1, 'Investigate the thing', 'ready', 3)",
		`INSERT INTO sessions (id, session_id, project_id, platform, state, started_at, last_activity)
		 VALUES (1, 'sess-1', 1, 'claude', 'working', '2026-08-01T00:00:00', '2026-08-01T00:00:00')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	return &mentionFixture{db: db, root: root}
}

// postToolUse drives the real PostToolUse entry point with a Bash payload.
func (f *mentionFixture) postToolUse(t *testing.T, cmd string) {
	t.Helper()
	input, err := json.Marshal(map[string]string{"command": cmd})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	payload := claudePayload{
		EventName: "PostToolUse",
		ToolName:  "Bash",
		SessionID: "sess-1",
		CWD:       f.root,
		ToolInput: input,
	}
	var herr error
	out := captureStdout(t, func() { herr = handlePostToolUse(1, true, payload) })
	if herr != nil {
		t.Fatalf("handlePostToolUse: %v", herr)
	}
	if out != "" {
		t.Errorf("a mention produced hook output:\n%s", out)
	}
}

func (f *mentionFixture) assertUnbound(t *testing.T) {
	t.Helper()
	var taskID sql.NullInt64
	if err := f.db.QueryRow("SELECT task_id FROM sessions WHERE session_id = 'sess-1'").Scan(&taskID); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if taskID.Valid {
		t.Errorf("session bound to task %d by text in a command; want unbound", taskID.Int64)
	}
}

func (f *mentionFixture) assertStatus(t *testing.T, want string) {
	t.Helper()
	var status string
	if err := f.db.QueryRow("SELECT status FROM tasks WHERE id = 10").Scan(&status); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if status != want {
		t.Errorf("task status = %q, want %q", status, want)
	}
}
