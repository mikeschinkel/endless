// Tests for `endless correct`'s entries (E-2486): preparation under the lock,
// the apply both paths share, the gates and the invariants, and that a ledger
// replayed through the projector reaches the row the live apply did.
package events_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/kairos"
)

// testClock stamps the events a test writes with real kairos timestamps.
var testClock = func() *kairos.Clock {
	nid, err := kairos.ParseNodeID("a1b2")
	if err != nil {
		panic(err)
	}
	return kairos.NewClock(nid)
}()

// seedTask inserts a task row directly, with the status and type the case
// needs, and returns its id.
func seedTask(t *testing.T, db *sql.DB, title, status string, typeID int) int64 {
	t.Helper()
	res, err := db.Exec(
		`INSERT INTO tasks (project_id, title, phase, status, type_id, sort_order, created_at)
		 VALUES (1, ?, 'now', ?, ?, 10, '2026-05-30T00:00:00')`, title, status, typeID)
	if err != nil {
		t.Fatalf("seed task: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// seedPlanItem inserts one planned deliverable item, approved or not.
func seedPlanItem(t *testing.T, db *sql.DB, taskID, seq int64, approved bool) {
	t.Helper()
	var approvedStatement, approvedKind, approvedAt any
	if approved {
		approvedStatement, approvedKind, approvedAt = "It works", "deliverable", "2026-05-30T00:00:00"
	}
	if _, err := db.Exec(
		`INSERT INTO task_plan_items (task_id, seq, sort_order, kind, statement, origin, state,
		    approved_statement, approved_kind, approved_at, created_at, updated_at)
		 VALUES (?, ?, ?, 'deliverable', 'It works', 'planned', 'active', ?, ?, ?,
		    '2026-05-30T00:00:00', '2026-05-30T00:00:00')`,
		taskID, seq, seq*10, approvedStatement, approvedKind, approvedAt); err != nil {
		t.Fatalf("seed plan item: %v", err)
	}
}

// prepare runs PrepareCorrection in a BEGIN IMMEDIATE and rolls it back, so a
// case can read what preparation decided without writing anything.
func prepare(t *testing.T, req events.CorrectionRequest) (*events.PreparedCorrection, error) {
	t.Helper()
	lock, err := events.BeginLocked()
	if err != nil {
		t.Fatalf("BeginLocked: %v", err)
	}
	defer lock.Rollback()
	return events.PrepareCorrection(lock.DB, req)
}

// correct prepares and applies one correction the way the command does, and
// returns the two events it wrote.
func correct(t *testing.T, req events.CorrectionRequest) (*events.PreparedCorrection, []*events.Event) {
	t.Helper()
	lock, err := events.BeginLocked()
	if err != nil {
		t.Fatalf("BeginLocked: %v", err)
	}
	prep, err := events.PrepareCorrection(lock.DB, req)
	if err != nil {
		lock.Rollback()
		t.Fatalf("PrepareCorrection: %v", err)
	}
	var evts []*events.Event
	corrected := events.CorrectedPayload{Reason: req.Reason, SkipGates: req.SkipGates, Refs: []int64{}}
	if prep.Change != nil {
		evts = append(evts, correctionEvent(t, events.KindLedgerRowChanged, prep.Entity, prep.Change))
		corrected.Refs = append(corrected.Refs, 0)
		corrected.Before = append(corrected.Before, *prep.Before)
	}
	evts = append(evts, correctionEvent(t, events.KindLedgerCorrected, prep.Entity, corrected))
	if err := lock.ExecAllAndCommit(evts); err != nil {
		t.Fatalf("ExecAllAndCommit: %v", err)
	}
	return prep, evts
}

func correctionEvent(t *testing.T, kind events.Kind, entity events.EntityRef, payload any) *events.Event {
	ts := testClock.Now().String()
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	evt := &events.Event{
		V: events.Version, TS: ts, Kind: kind, Project: "test", Entity: entity,
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: raw,
	}
	if err := evt.Validate(); err != nil {
		t.Fatalf("validate %s: %v", kind, err)
	}
	return evt
}

func scalar[T any](t *testing.T, db *sql.DB, q string, args ...any) T {
	t.Helper()
	var v T
	if err := db.QueryRow(q, args...).Scan(&v); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return v
}

// The deadlock case that prompted the command: a landed todo with an
// unapproved plan item. Gates refuse it, name the approval, and --skip-gates
// records the correction past them.
func TestCorrection_LandedTaskToAssumed(t *testing.T) {
	db := withExecutorDB(t)
	id := seedTask(t, db, "Landed with unapproved items", "landed", 1)
	seedPlanItem(t, db, id, 1, false)
	key := fmt.Sprint(id)

	_, err := prepare(t, events.CorrectionRequest{Target: "task", Key: key, Op: events.RowOpUpdate,
		Values: map[string]string{"status": "assumed"}, Reason: "the land skipped approval", Project: "test"})
	if err == nil || !strings.Contains(err.Error(), "not approved") {
		t.Fatalf("gates on: want a plan approval refusal, got %v", err)
	}
	prep, evts := correct(t, events.CorrectionRequest{Target: "task", Key: key, Op: events.RowOpUpdate,
		Values: map[string]string{"status": "assumed"}, Reason: "the land skipped approval",
		SkipGates: true, Project: "test"})
	if got := scalar[string](t, db, "SELECT status FROM tasks WHERE id = ?", id); got != "assumed" {
		t.Errorf("status = %q, want assumed", got)
	}
	if prep.Before.Values["status"] != "landed" {
		t.Errorf("before = %v, want status landed", prep.Before.Values)
	}
	if len(evts) != 2 || evts[0].Kind != events.KindLedgerRowChanged || evts[1].Kind != events.KindLedgerCorrected {
		t.Fatalf("events = %v", evts)
	}
	var p events.CorrectedPayload
	_ = json.Unmarshal(evts[1].Payload, &p)
	if !p.SkipGates || p.Reason != "the land skipped approval" || len(p.Refs) != 1 {
		t.Errorf("corrected payload = %+v", p)
	}
	if evts[0].Entity != (events.EntityRef{Type: events.EntityTask, ID: key}) {
		t.Errorf("entity = %v", evts[0].Entity)
	}
	if !strings.Contains(prep.Summary, "status landed → assumed") {
		t.Errorf("summary = %q", prep.Summary)
	}
	// No existing kind's effects: completed_at stays as the row had it.
	if got := scalar[sql.NullString](t, db, "SELECT completed_at FROM tasks WHERE id = ?", id); got.Valid {
		t.Errorf("completed_at was set to %q; a correction writes only what it is told", got.String)
	}
}

func TestCorrection_TransitionTableGate(t *testing.T) {
	db := withExecutorDB(t)
	id := seedTask(t, db, "Unplanned", "unplanned", 1)
	_, err := prepare(t, events.CorrectionRequest{Target: "task", Key: fmt.Sprint(id), Op: events.RowOpUpdate,
		Values: map[string]string{"status": "confirmed"}, Reason: "x", Project: "test"})
	if err == nil || !strings.Contains(err.Error(), "not a legal status change") {
		t.Fatalf("want the transition table's refusal, got %v", err)
	}
	// The vocabulary is a gate too, and --skip-gates bypasses it; the column
	// list is not, and holds either way.
	_, err = prepare(t, events.CorrectionRequest{Target: "task", Key: fmt.Sprint(id), Op: events.RowOpUpdate,
		Values: map[string]string{"status": "bogus"}, Reason: "x", Project: "test"})
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("want a vocabulary refusal, got %v", err)
	}
	for _, skip := range []bool{false, true} {
		_, err = prepare(t, events.CorrectionRequest{Target: "task", Key: fmt.Sprint(id), Op: events.RowOpUpdate,
			Values: map[string]string{"sort_order": "5"}, Reason: "x", SkipGates: skip, Project: "test"})
		if err == nil || !strings.Contains(err.Error(), "allowed: title, description") {
			t.Fatalf("skip=%v: want the column list named, got %v", skip, err)
		}
	}
}

func TestCorrection_PlanItemUpdate(t *testing.T) {
	db := withExecutorDB(t)
	id := seedTask(t, db, "Has items", "underway", 1)
	seedPlanItem(t, db, id, 3, false)
	prep, _ := correct(t, events.CorrectionRequest{Target: "plan_item", Key: fmt.Sprintf("E-%d:P3", id),
		Op: events.RowOpUpdate, Values: map[string]string{"approved_at": "2026-06-01T00:00:00",
			"approved_statement": "It works", "approved_kind": "deliverable"},
		Reason: "approval was given in conversation", Project: "test"})
	got := scalar[string](t, db, "SELECT approved_at FROM task_plan_items WHERE task_id = ? AND seq = 3", id)
	if got != "2026-06-01T00:00:00" {
		t.Errorf("approved_at = %q", got)
	}
	if prep.Entity != (events.EntityRef{Type: events.EntityTask, ID: fmt.Sprint(id)}) {
		t.Errorf("a plan item correction is recorded against its task, got %v", prep.Entity)
	}
	if prep.Change.Key != fmt.Sprintf("%d:3", id) {
		t.Errorf("key = %q", prep.Change.Key)
	}
	if prep.Before.Values["approved_at"] != nil {
		t.Errorf("before approved_at = %v, want NULL", prep.Before.Values["approved_at"])
	}
	if len(prep.TaskIDs) != 1 || prep.TaskIDs[0] != id {
		t.Errorf("mirror scope = %v", prep.TaskIDs)
	}
}

func TestCorrection_InsertAndDelete(t *testing.T) {
	db := withExecutorDB(t)
	id := seedTask(t, db, "Gets a row", "underway", 1)
	if _, err := db.Exec(`INSERT INTO sessions (id, project_id, state) VALUES (7, 1, 'working')`); err != nil {
		t.Fatal(err)
	}
	// A composite-key table: the key comes from the values.
	prep, _ := correct(t, events.CorrectionRequest{Target: "session_pinned_task", Op: events.RowOpInsert,
		Values: map[string]string{"session_id": "ES-7", "task_id": fmt.Sprint(id), "pinned_at": "2026-06-01T00:00:00"},
		Reason: "the pin was lost", Project: "test"})
	if prep.Change.Key != fmt.Sprintf("7:%d", id) {
		t.Errorf("key = %q", prep.Change.Key)
	}
	if n := scalar[int](t, db, "SELECT COUNT(*) FROM session_pinned_tasks WHERE session_id = 7 AND task_id = ?", id); n != 1 {
		t.Errorf("pinned rows = %d", n)
	}
	// A second insert of the same key is a duplicate.
	_, err := prepare(t, events.CorrectionRequest{Target: "session_pinned_task", Op: events.RowOpInsert,
		Values: map[string]string{"session_id": "7", "task_id": fmt.Sprint(id), "pinned_at": "x"},
		Reason: "again", Project: "test"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want a duplicate refusal, got %v", err)
	}
	// An id-keyed table allocates its id and reports it in the key.
	prep, _ = correct(t, events.CorrectionRequest{Target: "plan_item", Op: events.RowOpInsert,
		Values: map[string]string{"task_id": fmt.Sprint(id), "seq": "1", "sort_order": "10", "kind": "deliverable",
			"statement": "Added by correction", "origin": "planned", "created_at": "2026-06-01T00:00:00",
			"updated_at": "2026-06-01T00:00:00"},
		Reason: "the item was never recorded", Project: "test"})
	if prep.Change.Key != fmt.Sprintf("%d:1", id) || prep.Change.Values["id"] == nil {
		t.Errorf("insert key = %q values = %v", prep.Change.Key, prep.Change.Values)
	}
	// A required column missing is named.
	_, err = prepare(t, events.CorrectionRequest{Target: "plan_item", Op: events.RowOpInsert,
		Values: map[string]string{"task_id": fmt.Sprint(id), "seq": "2"}, Reason: "x", Project: "test"})
	if err == nil || !strings.Contains(err.Error(), "needs") {
		t.Fatalf("want the required columns named, got %v", err)
	}
	// Delete: refused while rows refer, allowed once they do not.
	_, err = prepare(t, events.CorrectionRequest{Target: "task", Key: fmt.Sprint(id), Op: events.RowOpDelete,
		Reason: "x", Project: "test"})
	if err == nil || !strings.Contains(err.Error(), "session_pinned_tasks") {
		t.Fatalf("want the referrers named, got %v", err)
	}
	prep, _ = correct(t, events.CorrectionRequest{Target: "session_pinned_task", Key: fmt.Sprintf("7:%d", id),
		Op: events.RowOpDelete, Reason: "the pin was wrong after all", Project: "test"})
	if prep.Before.Values["pinned_at"] != "2026-06-01T00:00:00" {
		t.Errorf("a delete keeps the whole row as before values, got %v", prep.Before.Values)
	}
	if n := scalar[int](t, db, "SELECT COUNT(*) FROM session_pinned_tasks WHERE session_id = 7"); n != 0 {
		t.Errorf("pinned rows after delete = %d", n)
	}
}

func TestCorrection_TaskDeleteMarksRemoved(t *testing.T) {
	db := withExecutorDB(t)
	id := seedTask(t, db, "Mistaken", "unplanned", 1)
	// The task's own content is not another row referring to it.
	if _, err := db.Exec("INSERT INTO task_content (task_id, name, content) VALUES (?, 'plan', 'p')", id); err != nil {
		t.Fatal(err)
	}
	prep, _ := correct(t, events.CorrectionRequest{Target: "task", Key: fmt.Sprint(id), Op: events.RowOpDelete,
		Reason: "filed twice", Project: "test"})
	if prep.Before.Values["plan"] != "p" || prep.Before.Values["title"] != "Mistaken" {
		t.Errorf("a task delete keeps the row and its content as before values, got %v", prep.Before.Values)
	}
	if got := scalar[int](t, db, "SELECT removed FROM tasks WHERE id = ?", id); got != 1 {
		t.Errorf("removed = %d; a task is retained with removed = 1, never deleted (ED-1547)", got)
	}
}

func TestCorrection_NoOpUnknownTargetAndAnnotation(t *testing.T) {
	db := withExecutorDB(t)
	id := seedTask(t, db, "Same", "unplanned", 1)
	_, err := prepare(t, events.CorrectionRequest{Target: "task", Key: fmt.Sprint(id), Op: events.RowOpUpdate,
		Values: map[string]string{"status": "unplanned"}, Reason: "x", Project: "test"})
	if err == nil || !strings.Contains(err.Error(), "nothing to correct") {
		t.Fatalf("want the no-op refusal, got %v", err)
	}
	_, err = prepare(t, events.CorrectionRequest{Target: "widgets", Key: "1", Reason: "x", Project: "test"})
	if err == nil || !strings.Contains(err.Error(), "unknown target") || !strings.Contains(err.Error(), "plan_item") {
		t.Fatalf("want the target list, got %v", err)
	}
	_, err = prepare(t, events.CorrectionRequest{Target: "task", Key: "999", Reason: "x", Project: "test"})
	if err == nil || !strings.Contains(err.Error(), "no task row") {
		t.Fatalf("want a missing row refusal, got %v", err)
	}
	_, err = prepare(t, events.CorrectionRequest{Target: "task", Key: fmt.Sprint(id), Op: events.RowOpUpdate,
		Values: map[string]string{"status": "assumed"}, Reason: "", Project: "test"})
	if err == nil || !strings.Contains(err.Error(), "--reason is required") {
		t.Fatalf("want the reason required, got %v", err)
	}
	// Annotations: on a row, and on an entity type with no table.
	prep, evts := correct(t, events.CorrectionRequest{Target: "task", Key: fmt.Sprint(id),
		Reason: "the title was agreed in conversation", Project: "test"})
	if prep.Change != nil || len(evts) != 1 || evts[0].Kind != events.KindLedgerCorrected {
		t.Fatalf("an annotation is one corrected entry, got %d events", len(evts))
	}
	var p events.CorrectedPayload
	_ = json.Unmarshal(evts[0].Payload, &p)
	if len(p.Refs) != 0 || p.Refs == nil {
		t.Errorf("refs = %v, want an empty list", p.Refs)
	}
	prep, _ = correct(t, events.CorrectionRequest{Target: "conversation", Key: "abc-123",
		Reason: "this conversation was a duplicate", Project: "test"})
	if prep.Entity != (events.EntityRef{Type: events.EntityConversation, ID: "abc-123"}) {
		t.Errorf("entity = %v", prep.Entity)
	}
	_, err = prepare(t, events.CorrectionRequest{Target: "conversation", Key: "abc", Op: events.RowOpUpdate,
		Values: map[string]string{"x": "y"}, Reason: "x", Project: "test"})
	if err == nil || !strings.Contains(err.Error(), "annotations only") {
		t.Fatalf("want annotations only, got %v", err)
	}
}

func TestCorrection_EpicNoteAndContent(t *testing.T) {
	db := withExecutorDB(t)
	epic := seedTask(t, db, "An epic", "underway", 4)
	prep, _ := correct(t, events.CorrectionRequest{Target: "task", Key: fmt.Sprint(epic), Op: events.RowOpUpdate,
		Values: map[string]string{"status": "completed"}, Reason: "children all done", SkipGates: true, Project: "test"})
	if len(prep.Notes) != 1 || !strings.Contains(prep.Notes[0], "next derivation") {
		t.Errorf("notes = %v", prep.Notes)
	}
	// Content fields write task_content, read back as before values, and mark
	// the task for a mirror rewrite.
	id := seedTask(t, db, "Has an outcome", "unplanned", 1)
	if _, err := db.Exec("INSERT INTO task_content (task_id, name, content) VALUES (?, 'outcome', 'old')", id); err != nil {
		t.Fatal(err)
	}
	prep, _ = correct(t, events.CorrectionRequest{Target: "task", Key: fmt.Sprint(id), Op: events.RowOpUpdate,
		Values: map[string]string{"outcome": "new", "type": "research", "complexity": "high"},
		Reason: "wrong outcome", Project: "test"})
	if prep.Before.Values["outcome"] != "old" || prep.Before.Values["type"] != "todo" || prep.Before.Values["complexity"] != nil {
		t.Errorf("before = %v", prep.Before.Values)
	}
	if got := scalar[string](t, db, "SELECT content FROM task_content WHERE task_id = ? AND name = 'outcome'", id); got != "new" {
		t.Errorf("outcome = %q", got)
	}
	if got := scalar[int](t, db, "SELECT type_id FROM tasks WHERE id = ?", id); got != 3 {
		t.Errorf("type_id = %d, want research", got)
	}
	if got := scalar[int](t, db, "SELECT complexity_id FROM tasks WHERE id = ?", id); got != 5 {
		t.Errorf("complexity_id = %d, want high", got)
	}
	if len(prep.TaskIDs) != 1 {
		t.Errorf("mirror scope = %v", prep.TaskIDs)
	}
	// A wrong project is refused before anything is read further.
	if _, err := db.Exec(`INSERT INTO projects (id, name, path, status, created_at, updated_at)
		 VALUES (2, 'other', '/tmp/other', 'active', '', '')`); err != nil {
		t.Fatal(err)
	}
	_, err := prepare(t, events.CorrectionRequest{Target: "task", Key: fmt.Sprint(id), Reason: "x", Project: "other"})
	if err == nil || !strings.Contains(err.Error(), "belongs to project test") {
		t.Fatalf("want the project refusal, got %v", err)
	}
}

// The projector applies ledger.row_changed and treats ledger.corrected as a
// no-op, so a ledger holding a correction rebuilds to the live row.
func TestCorrection_ProjectionMatchesLive(t *testing.T) {
	db := withExecutorDB(t)
	root := t.TempDir()
	created := newTaskCreatedEvent(t, 1, "Rebuilt")
	if _, err := events.Execute(created, nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := db.Exec("UPDATE tasks SET status = 'landed' WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	statusEvt := newStatusChangedEvent(t, 1, "unplanned", "landed")
	_, evts := correct(t, events.CorrectionRequest{Target: "task", Key: "1", Op: events.RowOpUpdate,
		Values: map[string]string{"status": "assumed", "outcome": "shipped", "verified_sha": "abc"},
		Reason: "gate skipped", SkipGates: true, Project: "test"})

	w, err := events.NewWriter(root, "a1b2")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range append([]*events.Event{created, statusEvt}, evts...) {
		line, _ := json.Marshal(e)
		if err := w.Append(line); err != nil {
			t.Fatal(err)
		}
	}
	tempPath, res, err := events.ProjectToTempDB(root)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	defer os.Remove(tempPath)
	if len(res.Errors) > 0 {
		t.Fatalf("projection errors: %v", res.Errors)
	}
	proj, err := sql.Open("sqlite", tempPath)
	if err != nil {
		t.Fatal(err)
	}
	defer proj.Close()
	type row struct{ status, outcome, sha string }
	read := func(d *sql.DB) row {
		var r row
		var sha sql.NullString
		if err := d.QueryRow(`SELECT t.status, COALESCE(c.content, ''), t.verified_sha FROM tasks t
		    LEFT JOIN task_content c ON c.task_id = t.id AND c.name = 'outcome' WHERE t.id = 1`).
			Scan(&r.status, &r.outcome, &sha); err != nil {
			t.Fatal(err)
		}
		r.sha = sha.String
		return r
	}
	live, projected := read(db), read(proj)
	if live != projected || live.status != "assumed" || live.outcome != "shipped" {
		t.Errorf("live %+v != projected %+v", live, projected)
	}
}

// A correction run's lines are adjacent and each recorded offset is where its
// change entry's line starts.
func TestWriter_RunOffsetsPointAtLineStarts(t *testing.T) {
	root := t.TempDir()
	w, err := events.NewWriter(root, "c0de")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append([]byte(`{"v":1,"kind":"earlier"}`)); err != nil {
		t.Fatal(err)
	}
	if err := w.BeginRun(); err != nil {
		t.Fatal(err)
	}
	lines := []string{`{"v":1,"kind":"ledger.row_changed","n":1}`, `{"v":1,"kind":"ledger.row_changed","n":22}`,
		`{"v":1,"kind":"ledger.corrected"}`}
	var offsets []int64
	for _, l := range lines {
		off, err := w.AppendOffset([]byte(l))
		if err != nil {
			t.Fatal(err)
		}
		offsets = append(offsets, off)
	}
	w.EndRun()
	data, err := os.ReadFile(filepath.Join(root, ".endless", events.LedgerDirName, w.CurrentSegment()))
	if err != nil {
		t.Fatal(err)
	}
	for i, off := range offsets {
		if off > 0 && data[off-1] != '\n' {
			t.Errorf("offset %d does not start a line", off)
		}
		if !strings.HasPrefix(string(data[off:]), lines[i]+"\n") {
			t.Errorf("offset %d points at %q, want %q", off, string(data[off:off+20]), lines[i])
		}
	}
	// Walking back as many lines as refs has entries lands on the first change.
	all := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if all[len(all)-3] != lines[0] {
		t.Errorf("the change entries do not precede the correction: %v", all)
	}
	if _, err := os.Stat(filepath.Join(root, ".endless", "tmp", events.LedgerLockFile)); err != nil {
		t.Errorf("lock file: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".endless", "tmp", ".gitignore")); string(b) != "*\n" {
		t.Errorf("the lock dir ignores itself, got %q", b)
	}
}

// A run checks rotation once: its lines share one segment even past the limit.
func TestWriter_RunStaysInOneSegment(t *testing.T) {
	root := t.TempDir()
	w, err := events.NewWriter(root, "c0de")
	if err != nil {
		t.Fatal(err)
	}
	w.SetMaxCount(2)
	if err := w.Append([]byte(`{"n":0}`)); err != nil {
		t.Fatal(err)
	}
	if err := w.BeginRun(); err != nil {
		t.Fatal(err)
	}
	seg := w.CurrentSegment()
	for i := 1; i <= 3; i++ {
		if _, err := w.AppendOffset([]byte(fmt.Sprintf(`{"n":%d}`, i))); err != nil {
			t.Fatal(err)
		}
		if w.CurrentSegment() != seg {
			t.Fatalf("line %d rotated into %s", i, w.CurrentSegment())
		}
	}
	w.EndRun()
	if err := w.Append([]byte(`{"n":4}`)); err != nil {
		t.Fatal(err)
	}
	if w.CurrentSegment() == seg {
		t.Errorf("the append after the run should rotate")
	}
}

// Writers in different goroutines (separate lock descriptors, as separate
// processes would hold) interleave only whole lines.
func TestWriter_ConcurrentAppendsStayWholeLines(t *testing.T) {
	root := t.TempDir()
	const writers, each = 8, 40
	done := make(chan error, writers)
	for i := 0; i < writers; i++ {
		go func(i int) {
			w, err := events.NewWriter(root, "c0de")
			if err != nil {
				done <- err
				return
			}
			for j := 0; j < each; j++ {
				line := fmt.Sprintf(`{"v":1,"writer":%d,"n":%d,"pad":"%s"}`, i, j, strings.Repeat("x", 300))
				if err := w.Append([]byte(line)); err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}(i)
	}
	for i := 0; i < writers; i++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(root, ".endless", events.LedgerDirName))
	total := 0
	for _, e := range entries {
		data, _ := os.ReadFile(filepath.Join(root, ".endless", events.LedgerDirName, e.Name()))
		for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
			var v map[string]any
			if err := json.Unmarshal([]byte(line), &v); err != nil {
				t.Fatalf("torn line %q: %v", line, err)
			}
			total++
		}
	}
	if total != writers*each {
		t.Errorf("lines = %d, want %d", total, writers*each)
	}
}
