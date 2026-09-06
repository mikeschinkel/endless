package monitor

import (
	"database/sql"
	"testing"
)

// snTask inserts a task with explicit phase/status/text/type so the
// session-status query's canonicalization and has_text columns can be exercised.
func snTask(t *testing.T, db *sql.DB, id, projectID int64, status, phase, text string) {
	t.Helper()
	var textVal any
	if text != "" {
		textVal = text
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, status, phase, text)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, projectID, "task-"+status, status, phase, textVal,
	); err != nil {
		t.Fatalf("snTask id=%d: %v", id, err)
	}
}

// snSession inserts a session with an explicit id and task_id so the
// row-set membership (sessions on the focal task) and in_flight decoration can
// be driven directly.
func snSession(t *testing.T, db *sql.DB, id, projectID, taskID int64, state string) {
	t.Helper()
	var at any
	if taskID != 0 {
		at = taskID
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, platform, state, task_id, started_at, last_activity)
		 VALUES (?, NULL, ?, 'claude', ?, ?, '2026-06-20T00:00:00', '2026-06-20T00:00:00')`,
		id, projectID, state, at,
	); err != nil {
		t.Fatalf("snSession id=%d: %v", id, err)
	}
}

func snSessionTask(t *testing.T, db *sql.DB, sessionID, taskID int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
		 VALUES (?, ?, '2026-06-20T00:00:00', '2026-06-20T00:00:00')`,
		sessionID, taskID,
	); err != nil {
		t.Fatalf("snSessionTask s=%d t=%d: %v", sessionID, taskID, err)
	}
}

// snSessionTaskRel inserts a session_tasks row with an explicit relation_id so
// the surfaced(2)/revisited(3)/goal(1) classification the no-goal view filters
// on can be driven directly (E-1802).
func snSessionTaskRel(t *testing.T, db *sql.DB, sessionID, taskID, relationID int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at)
		 VALUES (?, ?, ?, '2026-06-20T00:00:00', '2026-06-20T00:00:00')`,
		sessionID, taskID, relationID,
	); err != nil {
		t.Fatalf("snSessionTaskRel s=%d t=%d rel=%d: %v", sessionID, taskID, relationID, err)
	}
}

func snBlocks(t *testing.T, db *sql.DB, blockerID, blockedID int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
		 VALUES ('task', ?, 'task', ?, 'blocks')`,
		blockerID, blockedID,
	); err != nil {
		t.Fatalf("snBlocks %d->%d: %v", blockerID, blockedID, err)
	}
}

// snLanding inserts one task_landings row so the query's `landed` column can be
// exercised. session_id is left NULL; landed_at takes its schema default.
func snLanding(t *testing.T, db *sql.DB, id, taskID int64, sha string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO task_landings (id, task_id, session_id, merge_commit_sha)
		 VALUES (?, ?, NULL, ?)`,
		id, taskID, sha,
	); err != nil {
		t.Fatalf("snLanding id=%d task=%d: %v", id, taskID, err)
	}
}

func snRowByID(rows []SessionStatusRow, id int64) (SessionStatusRow, bool) {
	for _, r := range rows {
		if r.ID == id {
			return r, true
		}
	}
	return SessionStatusRow{}, false
}

// TestSessionStatusRows_RowSetAndDecorations drives the whole query: the row set
// (sessions on focal ∪ focal ∪ real parent ∪ spawner's task), the focal/parent/
// from/in_flight decorations, the block counts, and the terminal-status filter.
// realParent (focal's tasks.parent_id) and spawnerTask (the spawning session's
// active task) are DISTINCT tasks here (E-1694) so the ↑ parent / ↩ from split is
// exercised directly.
func TestSessionStatusRows_RowSetAndDecorations(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	const focal, sibling, realParent, spawnerTask, blocker, doneSibling = 100, 101, 150, 200, 300, 400

	snTask(t, db, focal, 1, "underway", "now", "")
	snTask(t, db, sibling, 1, "ready", "next", "has-plan")
	snTask(t, db, realParent, 1, "ready", "later", "")  // focal's task-tree parent
	snTask(t, db, spawnerTask, 1, "ready", "later", "") // the spawning session's active task
	snTask(t, db, blocker, 1, "underway", "now", "")    // open blocker of focal
	snTask(t, db, doneSibling, 1, "confirmed", "now", "")

	// focal's real task-tree parent is realParent, NOT the spawner.
	if _, err := db.Exec(`UPDATE tasks SET parent_id = ? WHERE id = ?`, realParent, focal); err != nil {
		t.Fatalf("set focal parent_id: %v", err)
	}

	// s1 is on the focal task and has touched focal, sibling, and the done one.
	snSession(t, db, 1, 1, focal, "working")
	snSessionTask(t, db, 1, focal)
	snSessionTask(t, db, 1, sibling)
	snSessionTask(t, db, 1, doneSibling)
	// s2 is the spawning session; its active task is spawnerTask (→ ↩ from).
	snSession(t, db, 2, 1, spawnerTask, "working")
	// s3 is a live session on the sibling → sibling is in_flight.
	snSession(t, db, 3, 1, sibling, "working")

	snBlocks(t, db, blocker, focal) // focal is blocked by an open task
	snBlocks(t, db, focal, sibling) // focal blocks the sibling

	rows, err := SessionStatusRows(focal, 2, false)
	if err != nil {
		t.Fatalf("SessionStatusRows: %v", err)
	}

	// doneSibling is terminal and not a decorated row → filtered out.
	if _, ok := snRowByID(rows, doneSibling); ok {
		t.Errorf("terminal task %d should be excluded without --all", doneSibling)
	}
	for _, id := range []int64{focal, sibling, realParent, spawnerTask} {
		if _, ok := snRowByID(rows, id); !ok {
			t.Errorf("expected task %d in row set, missing", id)
		}
	}

	f, _ := snRowByID(rows, focal)
	if !f.IsFocal || f.IsParent || f.IsFrom || f.InFlight {
		t.Errorf("focal decorations wrong: %+v", f)
	}
	if f.BlockedByN != 1 {
		t.Errorf("focal BlockedByN = %d, want 1", f.BlockedByN)
	}
	if f.BlocksN != 1 {
		t.Errorf("focal BlocksN = %d, want 1", f.BlocksN)
	}

	p, _ := snRowByID(rows, realParent)
	if !p.IsParent || p.IsFrom || p.IsFocal {
		t.Errorf("real-parent decorations wrong: %+v", p)
	}

	from, _ := snRowByID(rows, spawnerTask)
	if !from.IsFrom || from.IsParent || from.IsFocal {
		t.Errorf("spawner (from) decorations wrong: %+v", from)
	}

	s, _ := snRowByID(rows, sibling)
	if !s.InFlight {
		t.Errorf("sibling should be in_flight (live session on it): %+v", s)
	}
	if !s.HasText {
		t.Errorf("sibling has plan text, HasText should be true: %+v", s)
	}
}

// TestSessionStatusRows_AllIncludesDoneWork confirms --all surfaces terminal rows
// that are part of the row set.
func TestSessionStatusRows_AllIncludesDoneWork(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	const focal, done = 10, 20
	snTask(t, db, focal, 1, "underway", "now", "")
	snTask(t, db, done, 1, "confirmed", "now", "")
	snSession(t, db, 1, 1, focal, "working")
	snSessionTask(t, db, 1, focal)
	snSessionTask(t, db, 1, done)

	rows, err := SessionStatusRows(focal, 0, true)
	if err != nil {
		t.Fatalf("SessionStatusRows: %v", err)
	}
	if _, ok := snRowByID(rows, done); !ok {
		t.Errorf("--all should include terminal task %d", done)
	}
}

// TestSessionStatusRows_FocalDependents drives the E-1685 read-time dependents
// UNION: the focal task's direct dependents (tasks it blocks) appear as rows
// purely from task_deps, with no session_tasks membership; they carry ⊗
// (BlockedByN>0) while the focal is open and shed it once the focal lands; and a
// terminal dependent is omitted by default but surfaced under --all.
func TestSessionStatusRows_FocalDependents(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	const focal, dep, doneDep = 500, 501, 502
	snTask(t, db, focal, 1, "underway", "now", "")
	snTask(t, db, dep, 1, "ready", "next", "")
	snTask(t, db, doneDep, 1, "confirmed", "next", "")

	// The only session is on the focal and has touched ONLY the focal — neither
	// dependent is in session_tasks, so their presence proves the read-time UNION.
	snSession(t, db, 1, 1, focal, "working")
	snSessionTask(t, db, 1, focal)

	snBlocks(t, db, focal, dep)     // focal blocks an open dependent
	snBlocks(t, db, focal, doneDep) // focal blocks a terminal dependent

	// ── focal open: dependent shown, carrying ⊗ (BlockedByN counts the open focal)
	rows, err := SessionStatusRows(focal, 0, false)
	if err != nil {
		t.Fatalf("SessionStatusRows: %v", err)
	}
	d, ok := snRowByID(rows, dep)
	if !ok {
		t.Fatalf("open dependent %d should be a row while focal is open", dep)
	}
	if d.IsFocal || d.IsParent || d.IsFrom {
		t.Errorf("dependent should not be focal/parent/from: %+v", d)
	}
	if d.BlockedByN != 1 {
		t.Errorf("dependent BlockedByN = %d, want 1 (blocked by open focal)", d.BlockedByN)
	}
	if _, ok := snRowByID(rows, doneDep); ok {
		t.Errorf("terminal dependent %d should be omitted without --all", doneDep)
	}

	// ── --all surfaces the terminal dependent
	rowsAll, err := SessionStatusRows(focal, 0, true)
	if err != nil {
		t.Fatalf("SessionStatusRows --all: %v", err)
	}
	if _, ok := snRowByID(rowsAll, doneDep); !ok {
		t.Errorf("--all should include terminal dependent %d", doneDep)
	}

	// ── focal lands (terminal): the dependent's ⊗ clears, it still shows
	if _, err := db.Exec(`UPDATE tasks SET status = 'confirmed' WHERE id = ?`, focal); err != nil {
		t.Fatalf("land focal: %v", err)
	}
	rows2, err := SessionStatusRows(focal, 0, false)
	if err != nil {
		t.Fatalf("SessionStatusRows after land: %v", err)
	}
	d2, ok := snRowByID(rows2, dep)
	if !ok {
		t.Fatalf("dependent %d should still show after focal lands", dep)
	}
	if d2.BlockedByN != 0 {
		t.Errorf("dependent BlockedByN = %d after focal lands, want 0 (⊗ cleared)", d2.BlockedByN)
	}
}

// TestSessionStatusRows_UpstreamBlockerChain drives the E-1795 transitive
// upstream walk: for every task already shown, its blocked_by chain is walked to
// the head. Here the focal's direct dependent `dep` is blocked by a chain
// mid → head (head blocks mid blocks dep). None of mid/head has any session_tasks
// membership or task-tree relation to the focal, so their presence proves the
// transitive upstream UNION. A TERMINAL blocker higher up stops the walk (E-876
// status-based release): its own prerequisites are NOT surfaced.
func TestSessionStatusRows_UpstreamBlockerChain(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	// focal ← dep (dependent) ← mid ← head ← (doneGate ← ghost).
	// dep is a direct dependent of the focal (focal blocks dep). The chain above
	// dep is pure blocked_by. doneGate is terminal, so head's walk stops there and
	// ghost (blocked by the done gate) is NOT surfaced.
	const focal, dep, mid, head, doneGate, ghost = 900, 901, 902, 903, 904, 905
	snTask(t, db, focal, 1, "underway", "now", "")
	snTask(t, db, dep, 1, "ready", "next", "")
	snTask(t, db, mid, 1, "ready", "next", "")
	snTask(t, db, head, 1, "unplanned", "now", "") // chain head — an unplanned epic
	snTask(t, db, doneGate, 1, "confirmed", "now", "")
	snTask(t, db, ghost, 1, "ready", "next", "")

	// Only session is on the focal, touching only the focal.
	snSession(t, db, 1, 1, focal, "working")
	snSessionTask(t, db, 1, focal)

	snBlocks(t, db, focal, dep) // focal blocks its direct dependent (one hop down)
	snBlocks(t, db, mid, dep)   // dep blocked by mid
	snBlocks(t, db, head, mid)  // mid blocked by head  → transitive
	// A terminal gate blocks head; the walk must NOT climb past it to ghost.
	snBlocks(t, db, doneGate, head)
	snBlocks(t, db, ghost, doneGate)

	rows, err := SessionStatusRows(focal, 0, false)
	if err != nil {
		t.Fatalf("SessionStatusRows: %v", err)
	}

	// The whole open chain above the dependent is surfaced.
	for _, id := range []int64{dep, mid, head} {
		if _, ok := snRowByID(rows, id); !ok {
			t.Errorf("upstream blocker %d should be surfaced transitively, missing", id)
		}
	}
	// head is the chain head; its own blocker is terminal (released), so the walk
	// stops and the beyond-the-gate task never appears.
	if _, ok := snRowByID(rows, ghost); ok {
		t.Errorf("task %d beyond a terminal gate should NOT be surfaced", ghost)
	}
	// The terminal gate itself is dropped by the final terminal-status filter.
	if _, ok := snRowByID(rows, doneGate); ok {
		t.Errorf("terminal gate %d should be omitted without --all", doneGate)
	}

	// mid carries ⊗ (it is blocked by the open head).
	m, _ := snRowByID(rows, mid)
	if m.BlockedByN != 1 {
		t.Errorf("mid BlockedByN = %d, want 1 (blocked by open head)", m.BlockedByN)
	}
}

// TestSessionStatusRows_LandedColumn drives the E-1693 `landed` column: a landed
// non-terminal task is STILL returned (it passes the terminal-status filter) with
// Landed == true, while an un-landed task in the same row set has Landed == false.
func TestSessionStatusRows_LandedColumn(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	const focal, landed, plain = 600, 601, 602
	snTask(t, db, focal, 1, "underway", "now", "")
	snTask(t, db, landed, 1, "ready", "next", "") // landed but still non-terminal
	snTask(t, db, plain, 1, "ready", "next", "")  // un-landed sibling

	snSession(t, db, 1, 1, focal, "working")
	snSessionTask(t, db, 1, focal)
	snSessionTask(t, db, 1, landed)
	snSessionTask(t, db, 1, plain)

	snLanding(t, db, 1, landed, "deadbeef")

	rows, err := SessionStatusRows(focal, 0, false)
	if err != nil {
		t.Fatalf("SessionStatusRows: %v", err)
	}

	l, ok := snRowByID(rows, landed)
	if !ok {
		t.Fatalf("landed non-terminal task %d should still be returned (not omitted)", landed)
	}
	if !l.Landed {
		t.Errorf("task %d Landed = false, want true (has a task_landings row)", landed)
	}

	p, ok := snRowByID(rows, plain)
	if !ok {
		t.Fatalf("un-landed task %d should be returned", plain)
	}
	if p.Landed {
		t.Errorf("task %d Landed = true, want false (no task_landings row)", plain)
	}
}

// TestSessionStatusRowsForSession_NoGoalSurfacesWork drives the E-1802 no-goal
// view: a session with a NULL task_id still lists the tasks it filed
// (surfaced=2) and touched (revisited=3), while the goal-relation row (1) is
// excluded (that path is the focal view's) and a terminal row is omitted unless
// includeAll. Decorations that need a focal (is_focal/is_parent/is_from) are all
// false; in_flight/blocked/blocks counts still compute.
func TestSessionStatusRowsForSession_NoGoalSurfacesWork(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	const surfaced, revisited, goalTask, doneTouched, blocker = 1801, 1776, 1797, 1400, 1500
	snTask(t, db, surfaced, 1, "ready", "now", "")
	snTask(t, db, revisited, 1, "unplanned", "next", "")
	snTask(t, db, goalTask, 1, "underway", "now", "")
	snTask(t, db, doneTouched, 1, "confirmed", "now", "")
	snTask(t, db, blocker, 1, "underway", "now", "") // open blocker of `revisited`

	// The emitting session (id=963) has NO claimed goal (task_id NULL).
	snSession(t, db, 963, 1, 0, "working")
	snSessionTaskRel(t, db, 963, surfaced, 2)    // filed this session
	snSessionTaskRel(t, db, 963, revisited, 3)   // touched this session
	snSessionTaskRel(t, db, 963, goalTask, 1)    // goal — belongs to the focal view
	snSessionTaskRel(t, db, 963, doneTouched, 3) // touched but terminal

	// A live session on `revisited` makes it in_flight; an open blocker gives it ⊗.
	snSession(t, db, 964, 1, revisited, "working")
	snBlocks(t, db, blocker, revisited)

	rows, err := SessionStatusRowsForSession(963, false)
	if err != nil {
		t.Fatalf("SessionStatusRowsForSession: %v", err)
	}

	if _, ok := snRowByID(rows, surfaced); !ok {
		t.Errorf("surfaced task %d should be listed in the no-goal view", surfaced)
	}
	r, ok := snRowByID(rows, revisited)
	if !ok {
		t.Fatalf("revisited task %d should be listed in the no-goal view", revisited)
	}
	if r.IsFocal || r.IsParent || r.IsFrom {
		t.Errorf("no-goal rows carry no focal/parent/from decoration: %+v", r)
	}
	if !r.InFlight {
		t.Errorf("revisited %d has a live session on it, InFlight should be true: %+v", revisited, r)
	}
	if r.BlockedByN != 1 {
		t.Errorf("revisited %d BlockedByN = %d, want 1 (open blocker)", revisited, r.BlockedByN)
	}
	if _, ok := snRowByID(rows, goalTask); ok {
		t.Errorf("goal-relation task %d must NOT appear in the no-goal (surfaced/revisited) view", goalTask)
	}
	if _, ok := snRowByID(rows, doneTouched); ok {
		t.Errorf("terminal touched task %d should be omitted without includeAll", doneTouched)
	}

	// includeAll surfaces the terminal touched row.
	rowsAll, err := SessionStatusRowsForSession(963, true)
	if err != nil {
		t.Fatalf("SessionStatusRowsForSession includeAll: %v", err)
	}
	if _, ok := snRowByID(rowsAll, doneTouched); !ok {
		t.Errorf("includeAll should surface terminal touched task %d", doneTouched)
	}
}

// TestSessionStatusRowsForSession_ZeroSession returns nothing for session id 0.
func TestSessionStatusRowsForSession_ZeroSession(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	rows, err := SessionStatusRowsForSession(0, false)
	if err != nil {
		t.Fatalf("SessionStatusRowsForSession: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("session=0 should yield no rows, got %d", len(rows))
	}
}

// TestRepro_E1698_UnrelatedFocalFallback reproduces E-1698: when the pane has
// NO session/active task of its own, the old step-3 machine-wide last resort
// returned an UNRELATED live session's task. The fix drops that fallback, so the
// resolver must return 0 (→ claim/bind hint), not the stray task.
func TestRepro_E1698_UnrelatedFocalFallback(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	// An unrelated live session on task 700, bound to a DIFFERENT pane.
	snTask(t, db, 700, 1, "underway", "now", "")
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process_id, task_id, last_activity)
		 VALUES (NULL, 1, 'claude', 'working', ?, 700, '2026-06-20T00:00:00')`, mustSeedPane(t, db, TestServerUUID, "%888")); err != nil {
		t.Fatalf("seed unrelated session: %v", err)
	}

	// fakePane has no session of its own; the resolver must NOT invent task 700.
	focal, kind, err := ResolveSessionStatusFocal(fakePane)
	if err != nil {
		t.Fatalf("ResolveSessionStatusFocal: %v", err)
	}
	if focal != 0 {
		t.Errorf("focal = %d, want 0 (no unrelated machine-wide fallback)", focal)
	}
	if kind != PaneStatusNone {
		t.Errorf("kind = %d, want PaneStatusNone (%d)", kind, PaneStatusNone)
	}
}

// TestResolveSessionStatusFocal_ActivePaneResolves pins the happy path: a live
// session bound to THIS pane resolves its own active task with PaneStatusActive.
func TestResolveSessionStatusFocal_ActivePaneResolves(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	snTask(t, db, 810, 1, "underway", "now", "")
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process_id, task_id, last_activity)
		 VALUES (NULL, 1, 'claude', 'working', ?, 810, '2026-06-20T00:00:00')`, mustSeedPane(t, db, TestServerUUID, fakePane)); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	focal, kind, err := ResolveSessionStatusFocal(fakePane)
	if err != nil {
		t.Fatalf("ResolveSessionStatusFocal: %v", err)
	}
	if focal != 810 {
		t.Errorf("focal = %d, want 810 (this pane's own active task)", focal)
	}
	if kind != PaneStatusActive {
		t.Errorf("kind = %d, want PaneStatusActive (%d)", kind, PaneStatusActive)
	}
}

// TestResolveSessionStatusFocal_SessionNoTaskResolvesNoTaskKind pins that a
// session present in the pane but with NULL task_id yields no focal and
// the PaneStatusNoTask kind — the "claim a task" case, mirroring the status bar.
func TestResolveSessionStatusFocal_SessionNoTaskResolvesNoTaskKind(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process_id, last_activity)
		 VALUES (NULL, 1, 'claude', 'working', ?, '2026-06-20T00:00:00')`, mustSeedPane(t, db, TestServerUUID, fakePane)); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	focal, kind, err := ResolveSessionStatusFocal(fakePane)
	if err != nil {
		t.Fatalf("ResolveSessionStatusFocal: %v", err)
	}
	if focal != 0 {
		t.Errorf("focal = %d, want 0 (session has no active task)", focal)
	}
	if kind != PaneStatusNoTask {
		t.Errorf("kind = %d, want PaneStatusNoTask (%d)", kind, PaneStatusNoTask)
	}
}

// TestSessionStatusRows_ZeroFocal returns nothing when no focal task resolves.
func TestSessionStatusRows_ZeroFocal(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	rows, err := SessionStatusRows(0, 0, false)
	if err != nil {
		t.Fatalf("SessionStatusRows: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("focal=0 should yield no rows, got %d", len(rows))
	}
}
