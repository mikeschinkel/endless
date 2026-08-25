package events

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
	_ "modernc.org/sqlite"
)

// Tests for RepairClaimBindings (E-1967): restoring the session→task bindings
// the pre-ED-1560 code destroyed, from the `task.claimed` entries in each
// project's ledger.

// newRepairTestDB opens a file-backed SQLite DB with the real schema — the
// write-once trigger included, because "these writes cannot trip it" is one of
// the things under test — and registers `root` as the sole project so the
// repair reads the ledger written there.
func newRepairTestDB(t *testing.T, root string) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	t.Cleanup(monitor.SetTestDB(db))
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, path, status, created_at, updated_at)
		 VALUES (1, 'test', ?, 'active', '2026-08-01T00:00:00', '2026-08-01T00:00:00')`,
		root,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return db
}

func seedRepairTask(t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, status, type_id, phase, created_at)
		 VALUES (?, 1, 'probe', 'assumed', 1, 'now', '2026-08-01T00:00:00')`, id,
	); err != nil {
		t.Fatalf("seed task %d: %v", id, err)
	}
}

func seedRepairSession(t *testing.T, db *sql.DB, id int64, taskID any) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, state, task_id, started_at)
		 VALUES (?, ?, 1, 'ended', ?, '2026-08-01T00:00:00')`,
		id, "uuid-"+strconv.FormatInt(id, 10), taskID,
	); err != nil {
		t.Fatalf("seed session %d: %v", id, err)
	}
}

// writeClaimLedger writes one `task.claimed` entry per (taskID, sessionID) pair
// into root's ledger, through the real Writer so the repair reads the same file
// layout production does.
func writeClaimLedger(t *testing.T, root string, pairs [][2]int64) {
	t.Helper()
	w, err := NewWriter(root, "abcd")
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	for i, pair := range pairs {
		payload, err := json.Marshal(TaskClaimedPayload{SessionID: pair[1]})
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		line, err := json.Marshal(Event{
			V:       Version,
			TS:      "5X4D1C7AE881" + strconv.Itoa(100+i),
			Kind:    KindTaskClaimed,
			Project: "test",
			Entity:  EntityRef{Type: EntityTask, ID: strconv.FormatInt(pair[0], 10)},
			Actor:   Actor{Kind: "cli", ID: "someone@somewhere"},
			Payload: payload,
		})
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		if err := w.Append(line); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
}

func runRepair(t *testing.T, db *sql.DB) ClaimBindingRepair {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	repair, err := RepairClaimBindings(tx)
	if err != nil {
		tx.Rollback()
		t.Fatalf("RepairClaimBindings: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return repair
}

func boundTask(t *testing.T, db *sql.DB, sessionID int64) any {
	t.Helper()
	var taskID sql.NullInt64
	if err := db.QueryRow(
		"SELECT task_id FROM sessions WHERE id = ?", sessionID,
	).Scan(&taskID); err != nil {
		t.Fatalf("read binding for %d: %v", sessionID, err)
	}
	if !taskID.Valid {
		return nil
	}
	return taskID.Int64
}

// TestRepairClaimBindings_RestoresADestroyedBinding is the E-1859 case: the
// session claimed the task, `task reopen` NULLed the binding, and the ledger is
// the only surviving evidence.
func TestRepairClaimBindings_RestoresADestroyedBinding(t *testing.T) {
	root := t.TempDir()
	db := newRepairTestDB(t, root)
	seedRepairTask(t, db, 1859)
	seedRepairSession(t, db, 1046, nil)
	writeClaimLedger(t, root, [][2]int64{{1859, 1046}})

	repair := runRepair(t, db)

	if repair.Restored != 1 {
		t.Errorf("Restored = %d, want 1 (%+v)", repair.Restored, repair)
	}
	if got := boundTask(t, db, 1046); got != int64(1859) {
		t.Errorf("session 1046 bound to %v, want 1859", got)
	}
}

// TestRepairClaimBindings_LeavesAnExistingBindingAlone pins that a session that
// still holds a binding is never a candidate — which is also why the write-once
// trigger cannot fire: every write this makes is NULL -> value.
func TestRepairClaimBindings_LeavesAnExistingBindingAlone(t *testing.T) {
	root := t.TempDir()
	db := newRepairTestDB(t, root)
	seedRepairTask(t, db, 100)
	seedRepairTask(t, db, 200)
	seedRepairSession(t, db, 42, int64(100))
	// The ledger disagrees with the live binding. The live binding wins, and
	// nothing aborts.
	writeClaimLedger(t, root, [][2]int64{{200, 42}})

	repair := runRepair(t, db)

	if repair.Restored != 0 {
		t.Errorf("Restored = %d, want 0 (%+v)", repair.Restored, repair)
	}
	if got := boundTask(t, db, 42); got != int64(100) {
		t.Errorf("session 42 bound to %v, want its original 100", got)
	}
}

// TestRepairClaimBindings_SkipsAmbiguousSessions pins the "skip rather than
// guess" rule: a session the ledger shows claiming two different tasks is a
// pre-write-once rebind, and which claim was "the" one is unknowable.
func TestRepairClaimBindings_SkipsAmbiguousSessions(t *testing.T) {
	root := t.TempDir()
	db := newRepairTestDB(t, root)
	seedRepairTask(t, db, 100)
	seedRepairTask(t, db, 200)
	seedRepairSession(t, db, 42, nil)
	writeClaimLedger(t, root, [][2]int64{{100, 42}, {200, 42}})

	repair := runRepair(t, db)

	if repair.Ambiguous != 1 || repair.Restored != 0 {
		t.Errorf("want 1 ambiguous / 0 restored, got %+v", repair)
	}
	if got := boundTask(t, db, 42); got != nil {
		t.Errorf("session 42 bound to %v, want it left unbound", got)
	}
}

// A session that re-claimed the SAME task is not ambiguous — there is one
// answer and the ledger repeats it.
func TestRepairClaimBindings_RepeatedClaimOfOneTaskIsNotAmbiguous(t *testing.T) {
	root := t.TempDir()
	db := newRepairTestDB(t, root)
	seedRepairTask(t, db, 100)
	seedRepairSession(t, db, 42, nil)
	writeClaimLedger(t, root, [][2]int64{{100, 42}, {100, 42}})

	repair := runRepair(t, db)

	if repair.Restored != 1 || repair.Ambiguous != 0 {
		t.Errorf("want 1 restored / 0 ambiguous, got %+v", repair)
	}
}

// The FK on sessions.task_id means a binding to a deleted task cannot be
// written. Counted and skipped rather than failing the whole repair.
func TestRepairClaimBindings_SkipsAClaimOnAMissingTask(t *testing.T) {
	root := t.TempDir()
	db := newRepairTestDB(t, root)
	seedRepairSession(t, db, 42, nil)
	writeClaimLedger(t, root, [][2]int64{{999, 42}})

	repair := runRepair(t, db)

	if repair.MissingTask != 1 || repair.Restored != 0 {
		t.Errorf("want 1 missing-task / 0 restored, got %+v", repair)
	}
}

// TestRepairClaimBindings_IsIdempotent pins the second-run behavior the
// _schema_version marker would normally prevent anyway: nothing is a candidate,
// so nothing is written and the write-once trigger is never approached.
func TestRepairClaimBindings_IsIdempotent(t *testing.T) {
	root := t.TempDir()
	db := newRepairTestDB(t, root)
	seedRepairTask(t, db, 1859)
	seedRepairSession(t, db, 1046, nil)
	writeClaimLedger(t, root, [][2]int64{{1859, 1046}})

	first := runRepair(t, db)
	second := runRepair(t, db)

	if first.Restored != 1 {
		t.Fatalf("first run restored %d, want 1", first.Restored)
	}
	if second.Restored != 0 {
		t.Errorf("second run restored %d, want 0", second.Restored)
	}
	if got := boundTask(t, db, 1046); got != int64(1859) {
		t.Errorf("session 1046 bound to %v after the second run, want 1859", got)
	}
}

// A project with no ledger directory at all is not an error — a freshly
// registered project has one until its first event.
func TestRepairClaimBindings_ToleratesAMissingLedger(t *testing.T) {
	root := filepath.Join(t.TempDir(), "no-ledger-here")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	db := newRepairTestDB(t, root)
	seedRepairSession(t, db, 42, nil)

	repair := runRepair(t, db)

	if repair.Restored != 0 || repair.Ambiguous != 0 {
		t.Errorf("want an empty repair, got %+v", repair)
	}
}

// A `task.released` entry is NOT replayed by the repair: the ledger records
// that a release happened, and this task's whole point is that it should not
// have. Only `task.claimed` is read.
func TestRepairClaimBindings_IgnoresTaskReleased(t *testing.T) {
	root := t.TempDir()
	db := newRepairTestDB(t, root)
	seedRepairTask(t, db, 1859)
	seedRepairSession(t, db, 1046, nil)
	writeClaimLedger(t, root, [][2]int64{{1859, 1046}})

	w, err := NewWriter(root, "abcd")
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	payload, _ := json.Marshal(TaskReleasedPayload{SessionID: 1046})
	line, _ := json.Marshal(Event{
		V:       Version,
		TS:      "5X4D1C7AE881999",
		Kind:    KindTaskReleased,
		Project: "test",
		Entity:  EntityRef{Type: EntityTask, ID: "1859"},
		Actor:   Actor{Kind: "cli", ID: "someone@somewhere"},
		Payload: payload,
	})
	if err := w.Append(line); err != nil {
		t.Fatalf("append release: %v", err)
	}

	repair := runRepair(t, db)

	if repair.Restored != 1 {
		t.Errorf("Restored = %d, want 1 — the release must not cancel the claim", repair.Restored)
	}
	if got := boundTask(t, db, 1046); got != int64(1859) {
		t.Errorf("session 1046 bound to %v, want 1859", got)
	}
}
