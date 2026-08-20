package hookcmd

import (
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// TestClaimHandoffResponse_Shape pins the structural contract E-1822's delivery
// depends on (the E-1803 mechanism): PostToolUse additionalContext must be
// nested under hookSpecificOutput with the event name, and must carry the
// rendered handoff verbatim.
func TestClaimHandoffResponse_Shape(t *testing.T) {
	b, err := json.Marshal(claimHandoffResponse("HANDOFF TEXT"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	hso, ok := got["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("hookSpecificOutput missing/wrong type: %v", got["hookSpecificOutput"])
	}
	if hso["hookEventName"] != "PostToolUse" {
		t.Errorf("hookEventName = %v, want PostToolUse", hso["hookEventName"])
	}
	if hso["additionalContext"] != "HANDOFF TEXT" {
		t.Errorf("additionalContext = %v, want the rendered handoff", hso["additionalContext"])
	}
	if _, leaked := got["decision"]; leaked {
		t.Errorf("claim handoff must not carry a decision field: %v", got)
	}
}

// TestHierarchicalLabelPrefix mirrors Python's `_hierarchical_label_prefix`
// (E-1620): parented tasks render `E-<parent>/E-<id>`, roots the bare `E-<id>`.
func TestHierarchicalLabelPrefix(t *testing.T) {
	cases := []struct {
		name   string
		parent sql.NullInt64
		want   string
	}{
		{"root", sql.NullInt64{}, "E-42"},
		{"parented", sql.NullInt64{Int64: 7, Valid: true}, "E-7/E-42"},
		{"zero parent", sql.NullInt64{Int64: 0, Valid: true}, "E-42"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hierarchicalLabelPrefix(42, c.parent); got != c.want {
				t.Errorf("hierarchicalLabelPrefix = %q, want %q", got, c.want)
			}
		})
	}
}

// TestChildrenBreakdown mirrors Python's `_children_state` (E-1567): lifecycle
// order, terminal statuses collapsed into one bucket, and a total that always
// reconciles with the child count.
func TestChildrenBreakdown(t *testing.T) {
	db := newSchemaDB(t)
	mustExec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	mustExec("INSERT INTO projects (id, name, path) VALUES (1, 'p', '/p')")
	mustExec("INSERT INTO tasks (id, project_id, title, status) VALUES (10, 1, 'epic', 'underway')")
	mustExec("INSERT INTO tasks (id, project_id, title, status) VALUES (20, 1, 'lonely', 'underway')")

	n, state, err := childrenBreakdown(db, 20)
	if err != nil {
		t.Fatalf("childrenBreakdown (no children): %v", err)
	}
	if n != 0 || state != "no children yet" {
		t.Errorf("no children: got (%d, %q), want (0, \"no children yet\")", n, state)
	}

	for id, status := range map[int]string{
		11: "ready", 12: "ready", 13: "unplanned",
		14: "underway", 15: "confirmed", 16: "obsolete",
	} {
		mustExec(
			"INSERT INTO tasks (id, project_id, parent_id, title, status) VALUES (?, 1, 10, 'c', ?)",
			id, status,
		)
	}

	n, state, err = childrenBreakdown(db, 10)
	if err != nil {
		t.Fatalf("childrenBreakdown: %v", err)
	}
	if n != 6 {
		t.Errorf("child count = %d, want 6", n)
	}
	want := "1 unplanned, 2 ready, 1 underway, 2 terminal (6 total)"
	if state != want {
		t.Errorf("children state = %q, want %q", state, want)
	}
}

// TestClaimHandoffContext_SkipsSubagent verifies an Agent-tool subagent never
// receives the handoff. A subagent shares its parent's cwd and represents tool
// use, not a session taking ownership of a task.
func TestClaimHandoffContext_SkipsSubagent(t *testing.T) {
	if got := claimHandoffContext(1, 1, claudePayload{AgentID: "sub-1"}); got != "" {
		t.Errorf("subagent got a claim handoff (%d bytes); want none", len(got))
	}
}

// TestHandlePostToolUseSession_ClaimDeliversHandoff is the end-to-end delivery
// contract (E-1822): a PostToolUse payload for `endless task claim <id>` in a
// live session returns the rendered per-type claim handoff, which the caller
// emits as additionalContext. Drives the real matcher load, the real
// StartWorkSession write, and the real template render.
func TestHandlePostToolUseSession_ClaimDeliversHandoff(t *testing.T) {
	fx := newClaimFixture(t)

	handoff, err := fx.postToolUse("endless task claim E-10")
	if err != nil {
		t.Fatalf("handlePostToolUseSession: %v", err)
	}
	if handoff == "" {
		t.Fatal("claim into a live session produced no handoff")
	}

	wants := []string{
		"already-running",
		"/cd " + fx.worktree,
		"(branch task/10-claim-fixture)",
		"Stay focused on E-10 — one session, one task.",
		"--db main",
		// research type -> the artifact rule, NOT the todo `unverified` rule.
		"Findings are the deliverable.",
		"--status completed --outcome-file <path> --db main",
	}
	for _, w := range wants {
		if !strings.Contains(handoff, w) {
			t.Errorf("handoff missing %q\n--- handoff ---\n%s", w, handoff)
		}
	}
	if strings.Contains(handoff, "--status unverified") {
		t.Errorf("research claim handoff carries the todo terminal rule\n--- handoff ---\n%s", handoff)
	}

	// The claim itself still happened.
	var status string
	if err := fx.db.QueryRow("SELECT status FROM tasks WHERE id = 10").Scan(&status); err != nil {
		t.Fatalf("read task status: %v", err)
	}
	if status != "underway" {
		t.Errorf("task status = %q, want underway", status)
	}
}

// TestHandlePostToolUseSession_NonClaimYieldsNoHandoff confirms the injection is
// scoped to the claim: other endless verbs, and unrelated commands, return "" so
// the caller falls through to its existing PostToolUse handling.
func TestHandlePostToolUseSession_NonClaimYieldsNoHandoff(t *testing.T) {
	fx := newClaimFixture(t)
	for _, cmd := range []string{
		"endless task show E-10 --text --db main",
		"endless task report E-10 --db main",
		"endless task release",
		`git commit -m "E-10: deliver the claim handoff"`,
		"ls -la",
	} {
		t.Run(cmd, func(t *testing.T) {
			handoff, err := fx.postToolUse(cmd)
			if err != nil {
				t.Fatalf("handlePostToolUseSession: %v", err)
			}
			if handoff != "" {
				t.Errorf("non-claim command produced a handoff\n--- handoff ---\n%s", handoff)
			}
		})
	}
}

// TestClaimHandoffVars_NoWorktree_YieldsNoHandoff covers the refused/failed
// claim: no worktree means the claim did not get far enough to be worth a
// handoff, and pointing the session at a nonexistent directory would be worse
// than staying silent.
func TestClaimHandoffVars_NoWorktree_YieldsNoHandoff(t *testing.T) {
	fx := newClaimFixture(t)
	if err := os.RemoveAll(fx.worktree); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}
	if got := claimHandoffContext(1, 10, claudePayload{SessionID: "sess-1"}); got != "" {
		t.Errorf("expected no handoff without a worktree; got:\n%s", got)
	}
}

// ─── fixture ────────────────────────────────────────────────────────────────

type claimFixture struct {
	db       *sql.DB
	root     string
	worktree string
}

// newClaimFixture builds a project root registered in a seeded DB, with the
// `start`/`task` matcher in the project's .endless/config.json (that file is
// where matchers.Load reads project matchers from) and a real git worktree
// directory at the canonical path so WorktreePathForTask resolves and the branch
// read succeeds. Task 10 is a research task so the type branch is observable.
func newClaimFixture(t *testing.T) *claimFixture {
	t.Helper()

	db := newSchemaDB(t)
	restore := monitor.SetTestDB(db)
	t.Cleanup(restore)

	// Canonical form (E-2002): the hook normalizes the cwd it is handed and
	// reads the project row back through ProjectPath, which resolves symlinks
	// — so a fixture registered at the raw t.TempDir() (under /var on macOS, a
	// symlink into /private/var) would be asserting against a spelling the
	// product deliberately no longer emits.
	root := monitor.NormalizeProjectPath(t.TempDir())
	worktree := filepath.Join(root, ".endless", "worktrees", "e-10")
	if err := os.MkdirAll(worktree, 0755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
	writeProjectConfig(t, root)
	gitInitBranch(t, worktree, "task/10-claim-fixture")

	mustExec := func(q string, a ...any) {
		t.Helper()
		if _, err := db.Exec(q, a...); err != nil {
			t.Fatalf("exec %q: %v", q, err)
		}
	}
	mustExec("INSERT INTO projects (id, name, path) VALUES (1, 'p', ?)", root)
	mustExec(
		"INSERT INTO tasks (id, project_id, title, status, type_id) " +
			"VALUES (10, 1, 'Investigate the thing', 'ready', 3)", // type 3 = research
	)
	mustExec(`INSERT INTO sessions (id, session_id, project_id, platform, state, started_at, last_activity)
	          VALUES (1, 'sess-1', 1, 'claude', 'working', '2026-08-01T00:00:00', '2026-08-01T00:00:00')`)

	return &claimFixture{db: db, root: root, worktree: worktree}
}

// postToolUse drives handlePostToolUseSession with a Bash PostToolUse payload
// carrying cmd, and returns the claim handoff it produced (empty when none).
func (f *claimFixture) postToolUse(cmd string) (string, error) {
	input, err := json.Marshal(map[string]string{"command": cmd})
	if err != nil {
		return "", err
	}
	return handlePostToolUseSession(1, claudePayload{
		EventName: "PostToolUse",
		ToolName:  "Bash",
		SessionID: "sess-1",
		CWD:       f.root,
		ToolInput: input,
	})
}

// writeProjectConfig writes the project matchers matchers.Load reads. Only the
// `start`/`task` entry matters here; it mirrors the shipped default pattern.
func writeProjectConfig(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".endless")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir .endless: %v", err)
	}
	cfg := `{"matchers":[{"type":"start","scope":"task","method":"regex",` +
		`"match":"endless\\s+task\\s+claim\\s+(?:[Ee]-)?(\\d+)"}]}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
}

// gitInitBranch makes dir a git repo on branch so the handoff's branch read
// returns a real name rather than the "<task branch>" placeholder.
func gitInitBranch(t *testing.T, dir, branch string) {
	t.Helper()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "--initial-branch="+branch)
}
