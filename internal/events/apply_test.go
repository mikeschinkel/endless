// Tests for the apply-first emit transaction (E-2578): the SQL runs before any
// ledger line is appended, the lines are appended before the COMMIT, and a
// COMMIT that fails after the append leaves a ledger.voided line covering the
// whole run.
package events_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/kairos"
	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/tasktype"
)

// newLedger makes a writer over an empty ledger root and returns both.
func newLedger(t *testing.T) (root string, w *events.Writer) {
	t.Helper()
	root = t.TempDir()
	w, err := events.NewWriter(root, "a7f3")
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	return root, w
}

// seedEpicAndChild inserts an epic (unplanned) with one child at childStatus.
func seedEpicAndChild(t *testing.T, db *sql.DB, epicID, childID int64, childStatus string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, phase, status, type_id, sort_order)
		 VALUES (?, 1, 'epic', 'now', 'unplanned', ?, 10)`, epicID, int(tasktype.TaskTypeEpic),
	); err != nil {
		t.Fatalf("seed epic: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, parent_id, title, phase, status, type_id, sort_order)
		 VALUES (?, 1, ?, 'child', 'now', ?, 1, 20)`, childID, epicID, childStatus,
	); err != nil {
		t.Fatalf("seed child: %v", err)
	}
}

// landedEvent is a task.landed for taskID, which fails at its INSERT when the
// task does not exist (task_landings.task_id is a foreign key).
func landedEvent(t *testing.T, taskID string) *events.Event {
	t.Helper()
	payload, err := json.Marshal(events.TaskLandedPayload{MergeCommitSHA: "abc123", BaseBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	return &events.Event{
		V: events.Version, TS: kairos.NewClock(0xa7f3).Now().String(), Kind: events.KindTaskLanded,
		Project: "test",
		Entity:  events.EntityRef{Type: events.EntityTask, ID: taskID},
		Actor:   events.Actor{Kind: events.ActorCLI, ID: "tester"},
		Payload: payload,
	}
}

func ledgerFiles(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".endless", events.LedgerDirName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// An apply whose SQL fails leaves no line on the ledger and no change in the
// database, returns the SQL's error, and releases the write lock. This is the
// 2026-11-08 drift — a task.landed on the ledger with no task_landings row —
// closed.
func TestApply_SQLFailureLeavesNothing(t *testing.T) {
	db := withExecutorDB(t)
	root, w := newLedger(t)

	tx, err := events.BeginWrite()
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	_, err = tx.Apply(events.Emit{Event: landedEvent(t, "999"), Writer: w, Clock: kairos.NewClock(0xa7f3)})
	if err == nil {
		t.Fatal("apply of a landing for a task that does not exist succeeded")
	}
	if got := err.Error(); !strings.Contains(got, "insert task_landing") {
		t.Errorf("error = %q, want the INSERT's error", got)
	}
	var ve *events.VoidedError
	if errors.As(err, &ve) {
		t.Errorf("a SQL failure was reported as a voiding: %v", err)
	}

	if files := ledgerFiles(t, root); len(files) != 0 {
		t.Errorf("ledger holds %v after a failed apply; want nothing", files)
	}
	var n int
	if err := db.QueryRow("SELECT count(*) FROM task_landings").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("task_landings has %d rows; want 0", n)
	}

	// The lock is released: the next writer gets in.
	next, err := events.BeginWrite()
	if err != nil {
		t.Fatalf("BeginWrite after a failed apply: %v", err)
	}
	next.Rollback()
}

// A successful apply appends the primary line, then the lines the apply
// derived, and commits; the offsets it reports are where ReadAllEvents finds
// the lines.
func TestApply_AppendsPrimaryThenDerivedThenCommits(t *testing.T) {
	db := withExecutorDB(t)
	root, w := newLedger(t)
	seedEpicAndChild(t, db, 700, 701, "ready")

	tx, err := events.BeginWrite()
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	evt := newStatusChangedEvent(t, 701, "ready", "confirmed")
	applied, err := tx.Apply(events.Emit{Event: evt, Writer: w, Clock: kairos.NewClock(0xa7f3)})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if len(applied.Events) != 2 || applied.Events[0].Kind != events.KindTaskStatusChanged ||
		applied.Events[1].Kind != events.KindEpicStatusDerived {
		t.Fatalf("applied events = %v, want [task.status_changed epic.status_derived]", kindsOf(applied.Events))
	}
	if len(applied.Offsets) != 2 || applied.Offsets[0] != 0 || applied.Offsets[1] <= 0 {
		t.Errorf("offsets = %v, want [0 <len of first line>]", applied.Offsets)
	}
	if applied.Segment != w.CurrentSegment() {
		t.Errorf("segment = %q, want %q", applied.Segment, w.CurrentSegment())
	}

	read, err := events.ReadAllEvents(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 2 {
		t.Fatalf("ledger holds %d events, want 2", len(read))
	}
	for i, e := range read {
		if e.Segment != applied.Segment || e.Offset != applied.Offsets[i] {
			t.Errorf("read[%d] at %s:%d, applied reported %s:%d", i, e.Segment, e.Offset, applied.Segment, applied.Offsets[i])
		}
	}

	var epicStatus string
	if err := db.QueryRow("SELECT status FROM tasks WHERE id = 700").Scan(&epicStatus); err != nil {
		t.Fatal(err)
	}
	if epicStatus != "completed" {
		t.Errorf("epic status = %q, want completed", epicStatus)
	}
}

// A COMMIT that fails after the lines are appended voids the whole run: a
// ledger.voided line follows the primary and derived lines, naming each by
// offset and carrying the COMMIT's error, the database is unchanged, the
// error is a REPORT refusal whose cause is the *VoidedError, and the lock is
// released.
//
// The failure is real, not injected: in rollback-journal mode a COMMIT needs
// an exclusive lock, which a reader holding a SHARED lock denies, and with no
// busy_timeout the denial is immediate. WAL would let the COMMIT through, so
// the test pins the journal mode first.
func TestApply_CommitFailureVoidsTheRun(t *testing.T) {
	db := withExecutorDB(t)
	root, w := newLedger(t)
	seedEpicAndChild(t, db, 700, 701, "ready")
	if _, err := db.Exec("PRAGMA journal_mode=DELETE; PRAGMA busy_timeout=0"); err != nil {
		t.Fatal(err)
	}
	var path string
	if err := db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&path); err != nil {
		t.Fatal(err)
	}
	reader, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	reader.SetMaxOpenConns(1)
	rtx, err := reader.Begin()
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := rtx.QueryRow("SELECT count(*) FROM tasks").Scan(&n); err != nil {
		t.Fatal(err)
	}
	defer rtx.Rollback()

	tx, err := events.BeginWrite()
	if err != nil {
		t.Fatalf("BeginWrite: %v", err)
	}
	evt := newStatusChangedEvent(t, 701, "ready", "confirmed")
	_, err = tx.Apply(events.Emit{Event: evt, Writer: w, Clock: kairos.NewClock(0xa7f3)})
	if err == nil {
		t.Fatal("COMMIT under a reader's SHARED lock succeeded; the test's premise is wrong")
	}
	rtx.Rollback()

	var ve *events.VoidedError
	if !errors.As(err, &ve) {
		t.Fatalf("error is not a voiding: %v", err)
	}
	if refusal.From(err).Class() != refusal.ClassReport {
		t.Errorf("class = %v, want REPORT", refusal.From(err).Class())
	}
	if !strings.Contains(err.Error(), "the ledger holds the entry") || !strings.Contains(err.Error(), "database is unchanged") {
		t.Errorf("message = %q; want it to say the ledger holds the entry and the database is unchanged", err.Error())
	}

	read, err := events.ReadAllEvents(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := kindsOf(ptrs(read)); len(read) != 3 || read[0].Kind != events.KindTaskStatusChanged ||
		read[1].Kind != events.KindEpicStatusDerived || read[2].Kind != events.KindLedgerVoided {
		t.Fatalf("ledger = %v, want [task.status_changed epic.status_derived ledger.voided]", got)
	}
	if len(ve.Refs) != 2 || ve.Refs[0] != read[0].Offset || ve.Refs[1] != read[1].Offset || ve.VoidOffset != read[2].Offset {
		t.Errorf("voided refs %v void at %d; lines are at %d %d %d", ve.Refs, ve.VoidOffset, read[0].Offset, read[1].Offset, read[2].Offset)
	}
	var p events.VoidedPayload
	if err := json.Unmarshal(read[2].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Segment != w.CurrentSegment() || len(p.Refs) != 2 || p.Refs[0] != read[0].Offset || p.Refs[1] != read[1].Offset {
		t.Errorf("void payload = %+v; want segment %s refs [%d %d]", p, w.CurrentSegment(), read[0].Offset, read[1].Offset)
	}
	if !strings.Contains(p.Error, "commit") || p.Reason == "" {
		t.Errorf("void payload error=%q reason=%q; want the COMMIT error and a reason", p.Error, p.Reason)
	}
	// The voiding carries the voided event's actor and entity.
	if read[2].Actor != evt.Actor || read[2].Entity != evt.Entity {
		t.Errorf("void actor/entity = %+v/%+v, want the voided event's %+v/%+v", read[2].Actor, read[2].Entity, evt.Actor, evt.Entity)
	}

	var childStatus, epicStatus string
	if err := db.QueryRow("SELECT status FROM tasks WHERE id = 701").Scan(&childStatus); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT status FROM tasks WHERE id = 700").Scan(&epicStatus); err != nil {
		t.Fatal(err)
	}
	if childStatus != "ready" || epicStatus != "unplanned" {
		t.Errorf("child/epic = %s/%s after a voided apply; want ready/unplanned", childStatus, epicStatus)
	}

	next, err := events.BeginWrite()
	if err != nil {
		t.Fatalf("BeginWrite after a voided apply: %v", err)
	}
	next.Rollback()

	// The projection skips the voided run: with nothing else in the ledger it
	// replays the void alone and counts the two lines it names as skipped.
	tempPath, res, err := events.ProjectToTempDB(root)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	defer os.Remove(tempPath)
	if res.EventsSkipped != 2 || res.EventsReplayed != 1 {
		t.Errorf("projection replayed %d skipped %d; want 1 and 2", res.EventsReplayed, res.EventsSkipped)
	}
}

// Apply with no Writer — the test seam — still needs no clock, and Apply with
// a Writer and no Clock is refused before anything runs.
func TestApply_WriterNeedsClock(t *testing.T) {
	withExecutorDB(t)
	_, w := newLedger(t)
	tx, err := events.BeginWrite()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Apply(events.Emit{Event: landedEvent(t, "1"), Writer: w}); err == nil {
		t.Error("Apply with a writer and no clock succeeded")
	}
}

func kindsOf(evts []*events.Event) []events.Kind {
	kinds := make([]events.Kind, len(evts))
	for i, e := range evts {
		kinds[i] = e.Kind
	}
	return kinds
}

func ptrs(evts []events.Event) []*events.Event {
	out := make([]*events.Event, len(evts))
	for i := range evts {
		out[i] = &evts[i]
	}
	return out
}
