package claimhandoffcmd

import (
	"bytes"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
	_ "modernc.org/sqlite"
)

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

// TestRender_DeliversTheTypeHandoff is the delivery contract (E-1822): the
// render for a task claimed into a live session is the per-type claim handoff,
// pointing at the task's own worktree and branch.
func TestRender_DeliversTheTypeHandoff(t *testing.T) {
	fx := newClaimFixture(t)

	handoff, err := Render(10)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	wants := []string{
		"already-running",
		"/cd " + fx.worktree,
		"(branch task/10-claim-fixture)",
		"Stay focused on E-10 — one session, one task.",
		"--db main",
		// research type -> the artifact rule, NOT the todo `unverified` rule.
		"Findings are the deliverable.",
		// E-2016: and NOT `completed` either — findings work reports done at
		// the review gate and leaves the terminal to the user.
		"--status unreviewed --outcome-file <path> --db main",
		// E-2120: the discovery rule reaches a claimed-in session too — the
		// cost axis for folding work in, and the branch that puts the filing
		// decision in front of the user instead of taking it alone.
		"The test is COST and reviewer confusion, not kinship",
		"ASK me before filing",
		"Filing is the exception, not the default.",
	}
	for _, w := range wants {
		if !strings.Contains(handoff, w) {
			t.Errorf("handoff missing %q\n--- handoff ---\n%s", w, handoff)
		}
	}
	if strings.Contains(handoff, "--status unverified") {
		t.Errorf("research claim handoff carries the todo terminal rule\n--- handoff ---\n%s", handoff)
	}
}

// TestRun_ParsesTheTaskID covers the subcommand surface: an `E-` prefixed or
// bare id renders, and anything else is a usage error rather than a guess.
func TestRun_ParsesTheTaskID(t *testing.T) {
	newClaimFixture(t)
	for _, arg := range []string{"E-10", "e-10", "10"} {
		var out bytes.Buffer
		if err := run([]string{arg}, &out); err != nil {
			t.Errorf("run(%q): %v", arg, err)
			continue
		}
		if !strings.Contains(out.String(), "Stay focused on E-10") {
			t.Errorf("run(%q) did not render the handoff:\n%s", arg, out.String())
		}
	}
	for _, args := range [][]string{nil, {"--task", "10"}, {"E-x"}, {"0"}, {"10", "11"}} {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Errorf("run(%q): want a usage error", args)
		}
	}
}

// TestClaimHandoff_ChecksTheHarness pins the claim handoff's copy (E-1962).
//
// It has a worktree path and a project root, not a projectID/isRegistered pair,
// so it cannot route through the hook's reportChannelOn and the check has to be
// spelled out here. A Desktop session that claims a task would otherwise be
// handed the reporting instructions in its handoff.
func TestClaimHandoff_ChecksTheHarness(t *testing.T) {
	b, err := os.ReadFile("claimhandoff.go")
	if err != nil {
		t.Fatalf("read claimhandoff.go: %v", err)
	}
	if !strings.Contains(string(b), `agentenv.Supported() && monitor.MinimizerEnabledForCwd(`) {
		t.Error("the claim handoff's report_gate var is not harness-gated; " +
			"a Desktop session claiming a task would be handed the reporting contract")
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
	if got, err := Render(10); err == nil {
		t.Errorf("expected no handoff without a worktree; got:\n%s", got)
	}
}

// ─── fixture ────────────────────────────────────────────────────────────────

type claimFixture struct {
	db       *sql.DB
	root     string
	worktree string
}

// newClaimFixture builds a project root registered in a seeded DB, with a real
// git worktree directory at the canonical path so WorktreePathForTask resolves
// and the branch read succeeds. Task 10 is a research task so the type branch
// is observable.
func newClaimFixture(t *testing.T) *claimFixture {
	t.Helper()

	db := newSchemaDB(t)
	restore := monitor.SetTestDB(db)
	t.Cleanup(restore)

	// Canonical form (E-2002): the project row is read back through
	// ProjectPath, which resolves symlinks — so a fixture registered at the raw
	// t.TempDir() (under /var on macOS, a symlink into /private/var) would be
	// asserting against a spelling the product deliberately no longer emits.
	root, rootErr := monitor.ResolvedProjectPath(t.TempDir())
	if rootErr != nil {
		t.Fatalf("ResolvedProjectPath: %v", rootErr)
	}
	worktree := filepath.Join(root, ".endless", "worktrees", "e-10")
	if err := os.MkdirAll(worktree, 0755); err != nil {
		t.Fatalf("mkdir worktree: %v", err)
	}
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

	return &claimFixture{db: db, root: root, worktree: worktree}
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

// newSchemaDB opens a fresh file-backed SQLite DB at the latest schema version.
func newSchemaDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "endless.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}
