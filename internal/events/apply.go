package events

// The emit transaction (E-2578).
//
// Every `event emit` applies its event apply-first, in one write transaction:
//
//	BEGIN IMMEDIATE
//	run the SQL
//	append the primary ledger line
//	append the lines the apply derived (epic.status_derived)
//	COMMIT
//	commit the segment to git            (the caller, outside the transaction)
//
// A SQL failure rolls back and nothing reaches the ledger. The ledger is still
// authoritative in the sense that matters (ED-1024, ED-1525): no committed
// database change is without its ledger line, because the append precedes the
// COMMIT inside the same transaction. E-808 wrote the line first so a failed
// apply could be "picked up by rebuild-db"; E-2062 made that rebuild refuse,
// so a failed apply had become a ledger line nothing would ever apply — the
// drift seen on 2026-11-08, when a task.landed reached the ledger and the
// database refused its row.
//
// BEGIN IMMEDIATE, not a deferred BEGIN: a deferred transaction that has read
// and then writes after another connection committed fails at once with
// SQLITE_BUSY_SNAPSHOT (517), which busy_timeout does not retry. Taking the
// write lock up front turns that into the ordinary wait for a lock another
// writer holds, which the connection's busy_timeout covers. There is no retry
// loop on top of it: a loop would multiply the latency of an emit that will
// fail anyway and hide a lock holder worth finding.
//
// One window remains: the lines are appended and the COMMIT then fails. The
// ledger is then ahead of the database and must say so, which is what the
// ledger.voided entry is for. See void.go.

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/mikeschinkel/endless/internal/kairos"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// WriteTx is an open BEGIN IMMEDIATE transaction on the Endless database: the
// write lock an emit holds from before its first read of the rows it changes
// to after its ledger lines are appended.
type WriteTx struct {
	db   *sql.DB
	open bool
}

// BeginWrite takes the write lock. The caller ends the transaction with Apply
// or Rollback; a transaction left open holds the lock until the process exits.
func BeginWrite() (*WriteTx, error) {
	db, err := monitor.DB()
	if err != nil {
		return nil, fmt.Errorf("events: db connection: %w", err)
	}
	if _, err := db.Exec("BEGIN IMMEDIATE"); err != nil {
		return nil, fmt.Errorf("events: begin immediate: %w", err)
	}
	return &WriteTx{db: db, open: true}, nil
}

// DB is the connection the transaction is open on, for the reads an emit makes
// under the lock before it applies: allocating an id, preparing a payload
// against the current rows.
func (t *WriteTx) DB() *sql.DB { return t.db }

// Rollback ends the transaction without writing. Safe to call after Apply or
// twice.
func (t *WriteTx) Rollback() {
	if !t.open {
		return
	}
	t.db.Exec("ROLLBACK")
	t.open = false
}

// Emit is one `event emit`: the event, the ledger its lines go to, and the
// clock that stamps the lines the apply itself writes.
type Emit struct {
	Event *Event

	// Writer is the ledger the event's lines are appended to. Nil applies the
	// event with no ledger at all, which only a test wants: in production
	// every applied event has its line.
	Writer *Writer

	// Clock stamps the epic.status_derived lines the apply derives and the
	// ledger.voided line a failed COMMIT leaves. Required when Writer is set.
	Clock *kairos.Clock
}

// Applied is what a successful Apply did.
type Applied struct {
	Result *ExecuteResult

	// Events are the lines appended, in ledger order: the primary event, then
	// each epic.status_derived event the apply produced.
	Events []*Event

	// Segment and Offsets locate those lines; empty with no Writer.
	Segment string
	Offsets []int64
}

// Apply runs the event's SQL in the open transaction, appends its ledger lines
// and commits, in that order, then releases the lock.
//
//   - The SQL fails: ROLLBACK, nothing appended, the SQL's error returned.
//   - An append or the COMMIT fails after a line is on the ledger: ROLLBACK,
//     then a ledger.voided line is appended covering every line of the run,
//     and the error returned is a refusal whose cause is a *VoidedError. The
//     caller still commits the segment, so the voiding reaches git with the
//     lines it voids.
//
// Session notices are delivered after the COMMIT, as before: a notice is about
// a change that happened.
func (t *WriteTx) Apply(e Emit) (*Applied, error) {
	if !t.open {
		return nil, fmt.Errorf("events: apply on a transaction that is not open")
	}
	if e.Writer != nil && e.Clock == nil {
		return nil, fmt.Errorf("events: apply with a ledger needs a clock")
	}

	var derived []*Event
	var emit DerivedEmitter
	if e.Writer != nil {
		emit = func(epicID int64, oldStatus, newStatus string) error {
			evt, err := newEpicStatusDerived(e.Clock, e.Event.Project, epicID, oldStatus, newStatus)
			if err != nil {
				return err
			}
			derived = append(derived, evt)
			return nil
		}
	}

	result, notices, err := dispatchWithNotices(t.db, e.Event, emit)
	if err != nil {
		t.Rollback()
		return nil, err
	}

	applied := &Applied{Result: result, Events: append([]*Event{e.Event}, derived...)}
	if e.Writer == nil {
		if _, err := t.db.Exec("COMMIT"); err != nil {
			t.Rollback()
			return nil, fmt.Errorf("events: commit: %w", err)
		}
		t.open = false
		notices.deliver(t.db)
		return applied, nil
	}

	// The run holds the ledger lock from the first line to the last, so the
	// lines are adjacent in one segment and a voiding can name them by offset.
	if err := e.Writer.BeginRun(); err != nil {
		t.Rollback()
		return nil, err
	}
	defer e.Writer.EndRun()
	applied.Segment = e.Writer.CurrentSegment()

	for _, evt := range applied.Events {
		line, err := json.Marshal(evt)
		if err != nil {
			t.Rollback()
			return nil, t.voidOrFail(e, applied, fmt.Errorf("marshal %s event: %w", evt.Kind, err))
		}
		offset, err := e.Writer.AppendOffset(line)
		if err != nil {
			t.Rollback()
			return nil, t.voidOrFail(e, applied, err)
		}
		applied.Offsets = append(applied.Offsets, offset)
	}

	if _, err := t.db.Exec("COMMIT"); err != nil {
		// A failed COMMIT leaves the transaction open; ROLLBACK releases the
		// lock and discards the writes the ledger now says never took effect.
		t.Rollback()
		return nil, t.voidOrFail(e, applied, fmt.Errorf("events: commit: %w", err))
	}
	t.open = false
	notices.deliver(t.db)
	return applied, nil
}

// voidOrFail is the failure after at least one line may be on the ledger: it
// voids the lines appended so far, or, when none was, returns the cause alone.
func (t *WriteTx) voidOrFail(e Emit, applied *Applied, cause error) error {
	if len(applied.Offsets) == 0 {
		return cause
	}
	return voidRun(e.Writer, e.Clock, e.Event, applied.Segment, applied.Offsets, cause)
}

// newEpicStatusDerived builds the epic.status_derived event for one derivation
// (E-1541): a fresh timestamp from the emit's clock, the system/epic-derivation
// actor, the epic as its entity.
//
// Built literally rather than through EmittingActor: this event is synthesized
// by Endless, not typed by anyone, so stamping the emitting process's harness
// onto it would answer a question it was never asked (E-2005).
func newEpicStatusDerived(clock *kairos.Clock, project string, epicID int64, oldStatus, newStatus string) (*Event, error) {
	payload, err := json.Marshal(EpicStatusDerivedPayload{
		TaskID:    epicID,
		OldStatus: oldStatus,
		NewStatus: newStatus,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal epic.status_derived payload: %w", err)
	}
	evt := &Event{
		V:       Version,
		TS:      clock.Now().String(),
		Kind:    KindEpicStatusDerived,
		Project: project,
		Entity:  EntityRef{Type: EntityTask, ID: fmt.Sprintf("%d", epicID)},
		Actor:   Actor{Kind: ActorSystem, ID: "epic-derivation"},
		Payload: payload,
	}
	if err := evt.Validate(); err != nil {
		return nil, err
	}
	return evt, nil
}

// Execute applies an event with no ledger: BEGIN IMMEDIATE, the SQL, COMMIT.
//
// It is the test seam for the executors, and the only caller that may apply
// an event without recording it. The live emit path goes through BeginWrite
// and Apply with a Writer, so every applied event has its line. emit is the
// derivation emitter a test supplies to observe epic.status_derived events;
// nil records none.
func Execute(evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
	tx, err := BeginWrite()
	if err != nil {
		return nil, err
	}
	result, notices, err := dispatchWithNotices(tx.db, evt, emit)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	if _, err := tx.db.Exec("COMMIT"); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("events: commit: %w", err)
	}
	tx.open = false
	notices.deliver(tx.db)
	return result, nil
}

// PreAllocateTaskID takes the write lock and reads the next task id under it,
// so the id written into the event is the id the INSERT will use. The caller
// finishes with tx.Apply, or tx.Rollback when anything fails before it.
func PreAllocateTaskID() (id int64, tx *WriteTx, err error) {
	tx, err = BeginWrite()
	if err != nil {
		return 0, nil, err
	}
	// DO NOT point this at live_tasks (E-1929). Removed tasks are retained with
	// removed = 1 precisely so MAX(id) keeps counting past them and an id is
	// never re-minted. Through the view MAX(id) would drop back to the highest
	// LIVE id, ids would be reused again, and the FK-free rows that outlive a
	// task (session_tasks, session_notices, task_landings) would reattach to
	// unrelated work — reintroducing the exact bug ED-1547 exists to fix, while
	// appearing to fix it. Only the highest id is ever re-freed this way;
	// interior gaps are never refilled, which is why retention alone suffices.
	if err := tx.db.QueryRow("SELECT COALESCE(MAX(id), 0) + 1 FROM tasks").Scan(&id); err != nil {
		tx.Rollback()
		return 0, nil, fmt.Errorf("events: pre-allocate task id: %w", err)
	}
	return id, tx, nil
}

// PreAllocateDecisionID is the decision-table parallel of PreAllocateTaskID.
// Decisions use their own auto-increment column (E-1378); the ID space is
// independent of tasks (display prefix ED- disambiguates).
func PreAllocateDecisionID() (id int64, tx *WriteTx, err error) {
	tx, err = BeginWrite()
	if err != nil {
		return 0, nil, err
	}
	// `decisions`, NEVER live_decisions (E-2010, ED-1547). Removal retains the
	// row precisely so MAX(id) keeps counting past it and an ED-NNNN is never
	// re-minted. Through the view MAX(id) would drop back to the highest live
	// decision and hand a removed id to the next one allocated — the exact bug
	// the retained row exists to prevent, reintroduced while looking fixed.
	// The same warning sits on the tasks allocator above.
	if err := tx.db.QueryRow("SELECT COALESCE(MAX(id), 0) + 1 FROM decisions").Scan(&id); err != nil {
		tx.Rollback()
		return 0, nil, fmt.Errorf("events: pre-allocate decision id: %w", err)
	}
	return id, tx, nil
}
