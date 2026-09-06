package events

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/sessionstate"
	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
	"github.com/mikeschinkel/endless/internal/taskstatus"
	"github.com/mikeschinkel/endless/internal/tasktype"
)

// dbQuerier is satisfied by both *sql.Tx and *sql.DB, allowing the executor
// functions to work in both transactional and raw-connection contexts.
type dbQuerier interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
	Query(query string, args ...any) (*sql.Rows, error)
}

// ExecuteResult holds the output of a successful execution.
type ExecuteResult struct {
	TaskID          int64              `json:"task_id,omitempty"`           // for task.created/imported
	DecisionID      int64              `json:"decision_id,omitempty"`       // for decision.created (E-1378)
	SessionStatusID int64              `json:"session_status_id,omitempty"` // for session_status.recorded (E-1312)
	Skipped         bool               `json:"skipped,omitempty"`           // dedup-skip path (no row written)
	Markdown        string             `json:"markdown,omitempty"`          // rendered output for chat display
	ProjectNext     *ProjectNextResult `json:"-"`                           // for project_next.revised (E-1436)
}

// PreAllocateTaskID acquires a write lock via BEGIN IMMEDIATE, reads the next
// available task ID, and returns it along with functions to finish the transaction.
//
// Usage:
//  1. Call PreAllocateTaskID() to get the ID and lock the DB
//  2. Write the event to the segment file using the returned ID
//  3. Call execAndCommit(evt) to run the SQL mutation and release the lock
//  4. If anything fails before step 3, call rollback() to release the lock
func PreAllocateTaskID() (id int64, execAndCommit func(*Event, DerivedEmitter) (*ExecuteResult, error), rollback func(), err error) {
	db, err := monitor.DB()
	if err != nil {
		return 0, nil, nil, fmt.Errorf("events: db connection: %w", err)
	}

	// BEGIN IMMEDIATE acquires write lock, blocking other writers
	if _, err := db.Exec("BEGIN IMMEDIATE"); err != nil {
		return 0, nil, nil, fmt.Errorf("events: begin immediate: %w", err)
	}

	// DO NOT point this at live_tasks (E-1929). Removed tasks are retained with
	// removed = 1 precisely so MAX(id) keeps counting past them and an id is
	// never re-minted. Through the view MAX(id) would drop back to the highest
	// LIVE id, ids would be reused again, and the FK-free rows that outlive a
	// task (session_tasks, session_notices, task_landings) would reattach to
	// unrelated work — reintroducing the exact bug ED-1547 exists to fix, while
	// appearing to fix it. Only the highest id is ever re-freed this way;
	// interior gaps are never refilled, which is why retention alone suffices.
	err = db.QueryRow("SELECT COALESCE(MAX(id), 0) + 1 FROM tasks").Scan(&id)
	if err != nil {
		db.Exec("ROLLBACK")
		return 0, nil, nil, fmt.Errorf("events: pre-allocate task id: %w", err)
	}

	doRollback := func() {
		db.Exec("ROLLBACK")
	}

	doExecAndCommit := func(evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
		result, err := dispatch(db, evt, emit)
		if err != nil {
			db.Exec("ROLLBACK")
			return nil, err
		}
		if _, err := db.Exec("COMMIT"); err != nil {
			return nil, fmt.Errorf("events: commit: %w", err)
		}
		return result, nil
	}

	return id, doExecAndCommit, doRollback, nil
}

// PreAllocateDecisionID is the decision-table parallel of PreAllocateTaskID.
// Decisions use their own auto-increment column (E-1378); the ID space is
// independent of tasks (display prefix ED- disambiguates).
//
// Usage mirrors PreAllocateTaskID — see that docstring.
func PreAllocateDecisionID() (id int64, execAndCommit func(*Event, DerivedEmitter) (*ExecuteResult, error), rollback func(), err error) {
	db, err := monitor.DB()
	if err != nil {
		return 0, nil, nil, fmt.Errorf("events: db connection: %w", err)
	}

	if _, err := db.Exec("BEGIN IMMEDIATE"); err != nil {
		return 0, nil, nil, fmt.Errorf("events: begin immediate: %w", err)
	}

	err = db.QueryRow("SELECT COALESCE(MAX(id), 0) + 1 FROM decisions").Scan(&id)
	if err != nil {
		db.Exec("ROLLBACK")
		return 0, nil, nil, fmt.Errorf("events: pre-allocate decision id: %w", err)
	}

	doRollback := func() {
		db.Exec("ROLLBACK")
	}

	doExecAndCommit := func(evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
		result, err := dispatch(db, evt, emit)
		if err != nil {
			db.Exec("ROLLBACK")
			return nil, err
		}
		if _, err := db.Exec("COMMIT"); err != nil {
			return nil, fmt.Errorf("events: commit: %w", err)
		}
		return result, nil
	}

	return id, doExecAndCommit, doRollback, nil
}

// BeginImmediate acquires a write lock via BEGIN IMMEDIATE and returns
// functions to finish the transaction. Unlike PreAllocateTaskID it reads
// nothing up front — it exists so a multi-row rewrite (E-1436 revise) can
// take the write lock BEFORE the events-authoritative ledger append, the
// same ordering as the create path. A losing concurrent writer then fails
// at BEGIN (SQLITE_BUSY) without leaving an orphan ledger line.
//
// Usage:
//  1. Call BeginImmediate() to lock the DB
//  2. Append the event to the segment file
//  3. Call execAndCommit(evt) to run the SQL mutation and release the lock
//  4. If anything fails before step 3, call rollback() to release the lock
func BeginImmediate() (execAndCommit func(*Event, DerivedEmitter) (*ExecuteResult, error), rollback func(), err error) {
	db, err := monitor.DB()
	if err != nil {
		return nil, nil, fmt.Errorf("events: db connection: %w", err)
	}

	if _, err := db.Exec("BEGIN IMMEDIATE"); err != nil {
		return nil, nil, fmt.Errorf("events: begin immediate: %w", err)
	}

	doRollback := func() {
		db.Exec("ROLLBACK")
	}

	doExecAndCommit := func(evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
		result, err := dispatch(db, evt, emit)
		if err != nil {
			db.Exec("ROLLBACK")
			return nil, err
		}
		if _, err := db.Exec("COMMIT"); err != nil {
			return nil, fmt.Errorf("events: commit: %w", err)
		}
		return result, nil
	}

	return doExecAndCommit, doRollback, nil
}

// Execute processes an event: runs the corresponding SQL mutation.
// Used for non-create events where ID pre-allocation is not needed. emit is the
// epic-derivation ledger emitter (E-1541); it is nil for events that cannot
// trigger derivation and from callers that record no derived events (tests).
func Execute(evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
	db, err := monitor.DB()
	if err != nil {
		return nil, fmt.Errorf("events: db connection: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return nil, fmt.Errorf("events: begin tx: %w", err)
	}
	defer tx.Rollback()

	result, err := dispatch(tx, evt, emit)
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("events: commit: %w", err)
	}
	return result, nil
}

// stampTaskActor records WHO is about to change a task, so the
// tasks_notify_sessions trigger can skip notifying the session that made the
// change (E-1917). A session being told about its own edit is noise, and noise
// is what trains an agent to skim the line that matters.
//
// Done once here rather than in each of the eight UPDATE tasks statements
// downstream: a mutation site that forgot would silently inherit the PREVIOUS
// actor and suppress the wrong session. dispatch is the single choke point every
// task mutation passes through, and it already runs inside the caller's
// transaction, so the stamp and the field update land together or not at all.
//
// Writing the actor as a column rather than having the trigger discover it is
// deliberate: a trigger runs inside SQLite and cannot read the process
// environment, and the alternatives (a per-connection temp table, a registered
// SQL function) both add a cross-language connection contract whose only
// consumer is this suppression — and whose failure mode is every UPDATE tasks
// hard-failing.
//
// An empty Actor.SessionID stamps NULL, which suppresses nobody. That is
// correct, not a fallback: a NULL actor is the user editing from a bare
// terminal, which is the case this whole feature exists for.
//
// Non-task events and task events whose entity id is not a single task id
// (bulk operations) no-op here; a bulk clear's notices carry whatever actor was
// stamped last, which is imprecise but harmless — it can only over-notify.
//
// The AGENT-vs-PERSON question, and where its answer comes from.
//
// This distinction is the whole correctness of self-suppression, and the first
// implementation got it wrong by assuming evt.Actor.SessionID answered it.
// It does not. That value answers "which session should this command be
// CREDITED to", and _current_endless_session_id resolves it through four layers,
// three of which fire for a human:
//
//   - ENDLESS_SESSION_ID, exported into the USER'S OWN SHELL by `esu` /
//     `endless shell-init` — which `endless guide` instructs the user to run, so
//     following the documented workflow was enough to trigger the bug;
//   - a TMUX_PANE match;
//   - the sibling-pane inference (E-1294), which deliberately credits a shell
//     pane's command to the lone Claude session sharing its tmux window.
//
// The answer is evt.Actor.Harness: non-empty means an agent harness was running
// the process that EMITTED this event, "" means a person at a shell (E-2005).
//
// E-1917's fix read os.Getenv("CLAUDECODE") here instead — correct in practice,
// but a SECOND answer to a question the envelope had begun recording, and the
// two could disagree in both directions (CLAUDECODE=1 with no entrypoint said
// "agent" where agentenv says none; the reverse said "person" where agentenv
// says claude_cli). E-2006 deleted it. Reading the envelope rather than
// re-sniffing the environment matters for two reasons beyond having one answer:
// the deciding value is now DURABLE — a notice that was dropped can be
// explained afterward from the ledger line, where ambient process state left
// nothing behind — and a hand-rolled or replayed event carries its own answer
// instead of inheriting whichever process happens to be executing it.
//
// This gate is SUBTRACTIVE: it can only turn an attribution into NULL, never
// create one. So no change that is notified today can become suppressed by it,
// and the only movement possible is suppressed -> notified — the direction of
// the bug. A missed detection therefore over-notifies (one redundant line)
// rather than silencing the one session that needed to hear. That is also the
// failure direction of the residual risk: an emit path that hand-builds an
// Actor instead of using events.EmittingActor carries no harness and so stamps
// NULL.
//
// epic.status_derived is the one event whose Actor is built literally with no
// harness (E-2005: nobody typed it). It cannot reach here — dispatch's switch
// has no case for it, so Execute rejects it and it is appended to the ledger
// only — so it needs no exception.
func stampTaskActor(db dbQuerier, evt *Event) error {
	if evt.Entity.Type != EntityTask {
		return nil
	}
	taskID, err := strconv.ParseInt(evt.Entity.ID, 10, 64)
	if err != nil || taskID <= 0 {
		return nil
	}
	var actor any
	if evt.Actor.Harness != "" && evt.Actor.SessionID != "" {
		if sid, err := strconv.ParseInt(evt.Actor.SessionID, 10, 64); err == nil {
			actor = sid
		}
	}
	// Touches no watched field, so this UPDATE cannot itself fire the notice
	// trigger. Affects zero rows for a task.created event (the INSERT has not
	// happened yet), which is right: an INSERT fires no AFTER UPDATE trigger.
	if _, err := db.Exec(
		"UPDATE tasks SET changed_by_session = ? WHERE id = ?", actor, taskID,
	); err != nil {
		return fmt.Errorf("events: stamp task actor: %w", err)
	}
	return nil
}

// dispatch routes an event to its executor. emit (E-1541) is threaded to the
// six task mutations that can change an epic's derived status; the remaining
// executors ignore derivation.
func dispatch(db dbQuerier, evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
	if err := stampTaskActor(db, evt); err != nil {
		return nil, err
	}
	switch evt.Kind {
	case KindTaskCreated:
		return execTaskCreated(db, evt, emit)
	case KindTaskImported:
		return execTaskImported(db, evt, emit)
	case KindTaskStatusChanged:
		return execTaskStatusChanged(db, evt, emit)
	case KindTaskFieldsUpdated:
		return execTaskFieldsUpdated(db, evt, emit)
	case KindTaskMoved:
		return execTaskMoved(db, evt, emit)
	case KindTaskDeleted:
		return execTaskDeleted(db, evt, emit)
	case KindTaskBulkCleared:
		return execTaskBulkCleared(db, evt)
	case KindTaskReleased:
		return execTaskReleased(db, evt)
	case KindTaskClaimed:
		return execTaskClaimed(db, evt)
	case KindTaskLanded:
		return execTaskLanded(db, evt)
	case KindTaskDepCreated:
		return execTaskDepCreated(db, evt)
	case KindTaskDepDeleted:
		return execTaskDepDeleted(db, evt)
	case KindSessionStatusRecorded:
		return execSessionStatusRecorded(db, evt)
	case KindSessionTasksOrdered:
		return execSessionTasksOrdered(db, evt)
	case KindSessionTasksQueued:
		return execSessionTasksQueued(db, evt)
	case KindSessionTasksRemoved:
		return execSessionTasksRemoved(db, evt)
	case KindProjectNextRevised:
		return execProjectNextRevised(db, evt)
	case KindDecisionCreated:
		return execDecisionCreated(db, evt)
	case KindDecisionFieldsUpdated:
		return execDecisionFieldsUpdated(db, evt)
	case KindDecisionAccepted:
		return execDecisionAccepted(db, evt)
	case KindDecisionRejected:
		return execDecisionRejected(db, evt)
	case KindDecisionUnaccepted:
		return execDecisionUnaccepted(db, evt)
	case KindDecisionUnrejected:
		return execDecisionUnrejected(db, evt)
	case KindDecisionSuperseded:
		return execDecisionSuperseded(db, evt)
	case KindDecisionObsoleted:
		return execDecisionObsoleted(db, evt)
	case KindDecisionReinstated:
		return execDecisionReinstated(db, evt)
	case KindDecisionDeleted:
		return execDecisionDeleted(db, evt)
	case KindDecisionRelationCreated:
		return execDecisionRelationCreated(db, evt)
	case KindDecisionRelationDeleted:
		return execDecisionRelationDeleted(db, evt)
	default:
		return nil, fmt.Errorf("events: executor does not handle kind %q", evt.Kind)
	}
}

func resolveProjectID(db dbQuerier, name string) (int64, error) {
	var id int64
	err := db.QueryRow("SELECT id FROM projects WHERE name = ?", name).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("events: project %q not found: %w", name, err)
	}
	return id, nil
}

func now() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05")
}

// isPreJudgmentStatus reports whether a status means "nobody has decided this
// task is spec-complete yet" — the states from which attaching a plan is what
// makes it spec-complete, so the plan-attach auto-move to `submitted` applies.
//
// `untriaged` (E-1845) is the status `task add` now defaults to; `unplanned` is
// where triage sends a task that needs design work. Attaching a plan answers the
// open question in both cases, so both promote. Every other status either
// already carries a judgment (submitted/ready and beyond) or is a deliberate
// decision (declined/obsolete) that a plan attachment must not silently undo.
//
// The membership lives in taskstatus.PreJudgment (E-1891); this stays a named
// predicate because it reads as one at both call sites below.
func isPreJudgmentStatus(status string) bool {
	return taskstatus.Has(taskstatus.PreJudgment, status)
}

func execTaskCreated(db dbQuerier, evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
	var p TaskCreatedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.created payload: %w", err)
	}
	if err := ValidatePhase(p.Phase); err != nil {
		return nil, err
	}
	if err := ValidateMaybeParentless(p.Phase, p.ParentID); err != nil {
		return nil, err
	}
	typeID, err := tasktype.Parse(p.Type)
	if err != nil {
		return nil, err
	}

	projectID, err := resolveProjectID(db, evt.Project)
	if err != nil {
		return nil, err
	}

	sortOrder := p.SortOrder
	if p.AfterID != nil {
		var afterSort int
		err := db.QueryRow("SELECT sort_order FROM tasks WHERE id = ?", *p.AfterID).Scan(&afterSort)
		if err != nil {
			return nil, fmt.Errorf("events: after task %d not found: %w", *p.AfterID, err)
		}
		sortOrder = afterSort + 5
	} else if sortOrder == 0 {
		var maxSort sql.NullInt64
		db.QueryRow("SELECT MAX(sort_order) FROM tasks WHERE project_id = ? AND phase = ?",
			projectID, p.Phase).Scan(&maxSort)
		if maxSort.Valid {
			sortOrder = int(maxSort.Int64) + 10
		} else {
			sortOrder = 10
		}
	}

	// Use explicit ID from entity ref (pre-allocated by caller)
	taskID := mustParseInt64(evt.Entity.ID)
	ts := now()

	// Attaching a non-empty plan at creation moves the task to `submitted`
	// (spec-complete, awaiting human approval — NOT `ready`, which now means
	// human-approved). Mirrors task.fields_updated when --text is supplied.
	// Only fires from a pre-judgment status — an explicit override (e.g. a
	// tier-1 task created at `ready`, or any other non-default status) is
	// preserved. E-1845 added `untriaged`, which is now the default `task add`
	// lands on; without it, `task add --text plan.md` would file a fully planned
	// task as untriaged and strand it there.
	status := p.Status
	if isPreJudgmentStatus(status) && strings.TrimSpace(p.Text) != "" {
		status = "submitted"
	}

	var notes any
	if p.Notes != "" {
		notes = p.Notes
	}
	_, err = db.Exec(
		`INSERT INTO tasks (id, project_id, phase, title, description, text, analysis, notes, status, type_id, sort_order, parent_id, tier, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, projectID, p.Phase, p.Title, p.Description, p.Text, p.Analysis, notes, status, int(typeID),
		sortOrder, p.ParentID, p.Tier, ts, ts,
	)
	if err != nil {
		return nil, fmt.Errorf("events: insert task: %w", err)
	}

	if shouldRecordSessionTouch(evt) {
		// Created in-session → surfaced.
		if err := upsertSessionTask(db, evt.Actor.SessionID, taskID, sessiontaskrelation.RelationSurfaced); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}

	if p.Phase == "urgent" {
		if err := autoAddUrgentPending(db, evt, taskID); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}

	// E-1541: a new child can change its parent epic's derived status.
	if p.ParentID != nil {
		if err := recomputeEpicStatus(db, emit, *p.ParentID); err != nil {
			return nil, err
		}
	}

	return &ExecuteResult{TaskID: taskID}, nil
}

func execTaskImported(db dbQuerier, evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
	var p TaskImportedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.imported payload: %w", err)
	}
	if err := ValidatePhase(p.Phase); err != nil {
		return nil, err
	}

	projectID, err := resolveProjectID(db, evt.Project)
	if err != nil {
		return nil, err
	}

	taskID := mustParseInt64(evt.Entity.ID)
	ts := now()

	_, err = db.Exec(
		`INSERT INTO tasks (id, project_id, phase, title, description, status, source_file, sort_order, parent_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, 'unplanned', ?, ?, ?, ?, ?)`,
		taskID, projectID, p.Phase, p.Title, p.Description, p.SourceFile,
		p.SortOrder, p.ParentID, ts, ts,
	)
	if err != nil {
		return nil, fmt.Errorf("events: insert imported task: %w", err)
	}

	if shouldRecordSessionTouch(evt) {
		// Imported into the project in-session → surfaced.
		if err := upsertSessionTask(db, evt.Actor.SessionID, taskID, sessiontaskrelation.RelationSurfaced); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}

	// E-1541: an imported child can change its parent epic's derived status.
	if p.ParentID != nil {
		if err := recomputeEpicStatus(db, emit, *p.ParentID); err != nil {
			return nil, err
		}
	}

	return &ExecuteResult{TaskID: taskID}, nil
}

func execTaskStatusChanged(db dbQuerier, evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
	var p TaskStatusChangedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.status_changed payload: %w", err)
	}

	taskID := evt.Entity.ID

	var completedAt *string
	tier := 0
	if taskstatus.Has(taskstatus.SetsCompletedAt, p.NewStatus) {
		ts := now()
		completedAt = &ts
	}

	if p.Cascade {
		_, err := db.Exec(
			`WITH RECURSIVE tree(id) AS (
				SELECT id FROM tasks WHERE id = ?
				UNION ALL
				SELECT t.id FROM tasks t JOIN tree ON t.parent_id = tree.id
			) UPDATE tasks SET status = ?, completed_at = ?, tier = ?
			WHERE id IN (SELECT id FROM tree) AND status != ?`,
			taskID, p.NewStatus, completedAt, tier, p.NewStatus,
		)
		if err != nil {
			return nil, fmt.Errorf("events: cascade status change: %w", err)
		}
	} else {
		_, err := db.Exec(
			"UPDATE tasks SET status = ?, completed_at = ?, tier = ? WHERE id = ?",
			p.NewStatus, completedAt, tier, taskID,
		)
		if err != nil {
			return nil, fmt.Errorf("events: status change: %w", err)
		}
	}

	if p.Outcome != "" {
		if _, err := db.Exec(
			"UPDATE tasks SET outcome = ? WHERE id = ?",
			p.Outcome, taskID,
		); err != nil {
			return nil, fmt.Errorf("events: set outcome: %w", err)
		}
	}

	if shouldRecordSessionTouch(evt) {
		// Touched a pre-existing task (not claimed) → revisited.
		if err := upsertSessionTask(db, evt.Actor.SessionID, mustParseInt64(evt.Entity.ID), sessiontaskrelation.RelationRevisited); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}

	// E-1541: the changed task's status may alter its parent epic chain.
	// Recompute from the parent up; the changed task itself is never
	// re-derived, so an explicit set (e.g. a cascade confirm) is preserved.
	if parentID, ok, err := taskParentID(db, mustParseInt64(taskID)); err != nil {
		return nil, err
	} else if ok {
		if err := recomputeEpicStatus(db, emit, parentID); err != nil {
			return nil, err
		}
	}

	return &ExecuteResult{}, nil
}

func execTaskFieldsUpdated(db dbQuerier, evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
	var p TaskFieldsUpdatedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.fields_updated payload: %w", err)
	}

	if len(p.Fields) == 0 {
		return &ExecuteResult{}, nil
	}

	taskID := evt.Entity.ID

	// E-1541: a status or parent_id change can alter a parent epic's derived
	// status. Capture the old parent before the update so a re-parent
	// recomputes both the old and new chains.
	_, statusChanging := p.Fields["status"]
	_, parentChanging := p.Fields["parent_id"]
	var oldParentID int64
	var hasOldParent bool
	if parentChanging {
		var perr error
		oldParentID, hasOldParent, perr = taskParentID(db, mustParseInt64(taskID))
		if perr != nil {
			return nil, perr
		}
	}

	var setClauses []string
	var args []any

	allowedFields := map[string]string{
		"title": "title", "description": "description", "text": "text",
		"notes": "notes",
		"phase": "phase", "tier": "tier",
		"type": "type_id", "status": "status", "parent_id": "parent_id",
		"outcome": "outcome", "analysis": "analysis",
	}

	for field, value := range p.Fields {
		col, ok := allowedFields[field]
		if !ok {
			return nil, fmt.Errorf("events: unknown field %q in task.fields_updated", field)
		}
		if field == "phase" {
			phaseStr, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("events: phase field must be string, got %T", value)
			}
			if err := ValidatePhase(phaseStr); err != nil {
				return nil, err
			}
		}
		if field == "type" {
			typeStr, ok := value.(string)
			if !ok {
				return nil, fmt.Errorf("events: type field must be string, got %T", value)
			}
			tt, err := tasktype.Parse(typeStr)
			if err != nil {
				return nil, err
			}
			value = int(tt)
		}
		setClauses = append(setClauses, col+" = ?")
		args = append(args, value)
	}

	// Reject a task that would be both maybe-phase and parented. Only
	// evaluate when this update touches phase or parent_id — an unrelated
	// edit must not be blocked just because the row already violates. The
	// incoming field wins; the absent side is read from the current row.
	// parent_id arrives as a JSON number (float64) or null; null/0 → root.
	_, phaseSet := p.Fields["phase"]
	rawParent, parentSet := p.Fields["parent_id"]
	if phaseSet || parentSet {
		var curPhase string
		var curParent sql.NullInt64
		if err := db.QueryRow("SELECT phase, parent_id FROM tasks WHERE id = ?",
			taskID).Scan(&curPhase, &curParent); err != nil {
			return nil, fmt.Errorf("events: load task for maybe-parent check: %w", err)
		}
		effPhase := curPhase
		if v, ok := p.Fields["phase"].(string); ok {
			effPhase = v
		}
		var effParent *int64
		if parentSet {
			if v, ok := rawParent.(float64); ok && v > 0 {
				id := int64(v)
				effParent = &id
			}
		} else if curParent.Valid {
			effParent = &curParent.Int64
		}
		if err := ValidateMaybeParentless(effPhase, effParent); err != nil {
			return nil, err
		}
		// E-2067: `task update --parent` writes the same column `task move`
		// does, so it runs the same ancestor walk. Only when this update
		// actually touches parent_id — a phase-only edit cannot introduce a
		// cycle, and must not be blocked by one the row already has.
		if parentSet {
			if err := ValidateNoParentCycle(db, mustParseInt64(taskID), effParent); err != nil {
				return nil, err
			}
		}
	}

	// newStatus is the status this update will actually write, whatever its
	// source — the explicit --status field, or the plan-attach promotion just
	// below. Tracked in one variable so the E-2018 guard has ONE thing to
	// validate: a second write path that skipped the guard is precisely how
	// `task update --parent` slipped past the cycle check (E-2067).
	var newStatus string
	var hasNewStatus bool
	if status, ok := p.Fields["status"]; ok {
		newStatus = fmt.Sprintf("%v", status)
		hasNewStatus = true
	}

	// Attaching a non-empty plan (--text) to a pre-judgment task moves it to
	// `submitted` (spec-complete, awaiting human approval — NOT `ready`,
	// which now means human-approved). Only fires when the same update does
	// not already set status explicitly (caller wins).
	if textVal, hasText := p.Fields["text"]; hasText {
		if !hasNewStatus {
			textStr, _ := textVal.(string)
			if strings.TrimSpace(textStr) != "" {
				var currentStatus string
				if err := db.QueryRow("SELECT status FROM tasks WHERE id = ?",
					taskID).Scan(&currentStatus); err == nil {
					if isPreJudgmentStatus(currentStatus) {
						setClauses = append(setClauses, "status = ?")
						args = append(args, taskstatus.Submitted)
						newStatus = taskstatus.Submitted
						hasNewStatus = true
					}
				}
			}
		}
	}

	// E-2018: the status lifecycle is enforced here, on the same executor and
	// by the same route that let E-2067's parent cycle through. Two checks —
	// is this edge in the table, and does the actor have standing to take it.
	// Only when the update actually writes a status; an unrelated edit must not
	// be refused because the row already sits somewhere the table disallows.
	if hasNewStatus {
		currentStatus, currentType, terr := taskStatusAndType(db, taskID)
		if terr != nil {
			return nil, terr
		}
		// The incoming --type wins when the same update changes it: the write
		// lands as one row, so the lane it is judged against is the lane it
		// ends up on.
		effectiveType := currentType
		if typeStr, ok := p.Fields["type"].(string); ok {
			if tt, perr := tasktype.Parse(typeStr); perr == nil {
				effectiveType = tt
			}
		}
		if err := ValidateStatusTransition(currentStatus, newStatus, effectiveType); err != nil {
			return nil, err
		}
		if err := ValidateStatusActor(db, mustParseInt64(taskID), newStatus, evt.Actor); err != nil {
			return nil, err
		}
	}

	if status, ok := p.Fields["status"]; ok {
		statusStr := fmt.Sprintf("%v", status)
		// Settled = the work is over one way or another, shipped or abandoned,
		// so a priority tier no longer means anything (E-1891 relocated this
		// set; it is unchanged).
		if taskstatus.Has(taskstatus.Settled, statusStr) {
			if _, tierSet := p.Fields["tier"]; !tierSet {
				setClauses = append(setClauses, "tier = ?")
				args = append(args, 0)
			}
		}
		if taskstatus.Has(taskstatus.SetsCompletedAt, statusStr) {
			setClauses = append(setClauses, "completed_at = ?")
			args = append(args, now())
		} else {
			setClauses = append(setClauses, "completed_at = NULL")
		}
	}

	args = append(args, taskID)
	query := fmt.Sprintf("UPDATE tasks SET %s WHERE id = ?",
		joinStrings(setClauses, ", "))

	if _, err := db.Exec(query, args...); err != nil {
		return nil, fmt.Errorf("events: update task fields: %w", err)
	}

	if shouldRecordSessionTouch(evt) {
		// Touched a pre-existing task (not claimed) → revisited.
		if err := upsertSessionTask(db, evt.Actor.SessionID, mustParseInt64(evt.Entity.ID), sessiontaskrelation.RelationRevisited); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}

	if phaseVal, hasPhase := p.Fields["phase"]; hasPhase {
		if phaseStr, ok := phaseVal.(string); ok && phaseStr == "urgent" {
			if err := autoAddUrgentPending(db, evt, mustParseInt64(evt.Entity.ID)); err != nil {
				return nil, fmt.Errorf("events: %w", err)
			}
		}
	}

	// E-1541: recompute the affected epic chains. The current (post-update)
	// parent covers a status change and a re-parent's new chain; oldParentID
	// covers the chain the task left.
	if statusChanging || parentChanging {
		var parentIDs []int64
		if pid, ok, err := taskParentID(db, mustParseInt64(taskID)); err != nil {
			return nil, err
		} else if ok {
			parentIDs = append(parentIDs, pid)
		}
		if parentChanging && hasOldParent {
			parentIDs = append(parentIDs, oldParentID)
		}
		if len(parentIDs) > 0 {
			if err := recomputeEpicStatus(db, emit, parentIDs...); err != nil {
				return nil, err
			}
		}
	}

	return &ExecuteResult{}, nil
}

func execTaskMoved(db dbQuerier, evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
	var p TaskMovedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.moved payload: %w", err)
	}

	taskID := evt.Entity.ID

	if p.NewParentID != nil {
		// Reject moving a maybe-phase task under a parent (root moves are fine).
		var curPhase string
		if err := db.QueryRow("SELECT phase FROM tasks WHERE id = ?", taskID).Scan(&curPhase); err != nil {
			return nil, fmt.Errorf("events: load task for maybe-parent check: %w", err)
		}
		if err := ValidateMaybeParentless(curPhase, p.NewParentID); err != nil {
			return nil, err
		}

		// E-2067: the ancestor walk lives in ValidateNoParentCycle so that
		// task.fields_updated — the other writer of tasks.parent_id — runs
		// the identical guard.
		if err := ValidateNoParentCycle(db, mustParseInt64(taskID), p.NewParentID); err != nil {
			return nil, err
		}
	}

	if _, err := db.Exec("UPDATE tasks SET parent_id = ? WHERE id = ?",
		p.NewParentID, taskID); err != nil {
		return nil, fmt.Errorf("events: move task: %w", err)
	}

	if shouldRecordSessionTouch(evt) {
		// Touched a pre-existing task (not claimed) → revisited.
		if err := upsertSessionTask(db, evt.Actor.SessionID, mustParseInt64(evt.Entity.ID), sessiontaskrelation.RelationRevisited); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}

	// E-1541: both the old and new parent chains can change derived status.
	var parentIDs []int64
	if p.NewParentID != nil {
		parentIDs = append(parentIDs, *p.NewParentID)
	}
	if p.OldParentID != nil {
		parentIDs = append(parentIDs, *p.OldParentID)
	}
	if len(parentIDs) > 0 {
		if err := recomputeEpicStatus(db, emit, parentIDs...); err != nil {
			return nil, err
		}
	}

	return &ExecuteResult{}, nil
}

func execTaskDeleted(db dbQuerier, evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
	var p TaskDeletedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.deleted payload: %w", err)
	}

	taskID := evt.Entity.ID

	// E-1541: capture the parent before the removal so its epic chain can be
	// recomputed afterwards (the task left the parent's child set).
	parentID, hasParent, err := taskParentID(db, mustParseInt64(taskID))
	if err != nil {
		return nil, err
	}

	// ED-1547 (E-1929): mark removed, never DELETE — see task_removal.go. The
	// projector's replay handler calls the same function, so the live path and
	// the rebuild path cannot disagree about what removal means.
	if _, err = removeTaskTree(db, mustParseInt64(taskID), p.Cascade); err != nil {
		return nil, err
	}

	// Record only the primary entity even on cascade — cascaded child
	// removals are derived effects, not direct touches by the session.
	// session_tasks has no FK on task_id, so the row survives the removal.
	if shouldRecordSessionTouch(evt) {
		// Touched a pre-existing task (not claimed) → revisited.
		if err := upsertSessionTask(db, evt.Actor.SessionID, mustParseInt64(evt.Entity.ID), sessiontaskrelation.RelationRevisited); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}

	if hasParent {
		if err := recomputeEpicStatus(db, emit, parentID); err != nil {
			return nil, err
		}
	}

	return &ExecuteResult{}, nil
}

func execTaskBulkCleared(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p TaskBulkClearedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.bulk_cleared payload: %w", err)
	}

	projectID, err := resolveProjectID(db, evt.Project)
	if err != nil {
		return nil, err
	}

	// ED-1547 (E-1929): mark removed, never DELETE — see task_removal.go, which
	// the projector's replay handler calls too. It returns the ids it covered so
	// session_tasks can record a per-cleared-task touch; session_tasks has no FK
	// on task_id, so those rows outlive the removal.
	ids, err := removeTasksBySourceFile(db, projectID, p.SourceFile)
	if err != nil {
		return nil, err
	}

	if shouldRecordSessionTouch(evt) {
		for _, id := range ids {
			// Bulk-clear touches pre-existing tasks → revisited.
			if err := upsertSessionTask(db, evt.Actor.SessionID, id, sessiontaskrelation.RelationRevisited); err != nil {
				return nil, fmt.Errorf("events: %w", err)
			}
		}
	}

	return &ExecuteResult{}, nil
}

func execTaskClaimed(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p TaskClaimedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.claimed payload: %w", err)
	}
	taskID := evt.Entity.ID
	// Also resolve and store epic_id: the nearest type='epic' ancestor of
	// the claimed task (the task itself if it is an epic, NULL if no epic
	// ancestor). E-1571 deferred this coordinator-side write; without it an
	// interactive session's epic_id stays NULL and the status-line epic prefix
	// never fires. The correlated subquery walks tasks.parent_id upward as a
	// single atomic statement. A NULL result also clears any stale epic id left
	// from a prior claim. (E-2074 removed monitor.nearestEpicAncestor, the
	// background-agent-side twin this once mirrored; this is now the only
	// epic-ancestor resolver.)
	// Snapshot the session's pre-write state (within this tx) so the
	// machine-local diagnostic log can record the task_id transition —
	// the single-pointer rebind that otherwise leaves no trail.
	snap := monitor.SnapshotSessionByID(db, p.SessionID)
	res, err := db.Exec(
		`UPDATE sessions
		    SET task_id = ?,
		        -- Revive an 'ended' row on bind (E-1686), mirroring TouchSession's
		        -- revival: binding a task to a session is proof it's alive, so
		        -- task bind must not silently land on (and report success against)
		        -- an invisible dead row. CASE keeps live states authoritative.
		        state = CASE WHEN state = ? THEN ? ELSE state END,
		        epic_id = (
		          WITH RECURSIVE ancestry(id, parent_id, type_id, depth) AS (
		            SELECT id, parent_id, type_id, 0 FROM tasks WHERE id = ?
		            UNION ALL
		            SELECT t.id, t.parent_id, t.type_id, a.depth + 1
		              FROM tasks t JOIN ancestry a ON t.id = a.parent_id
		          )
		          SELECT a.id FROM ancestry a
		            JOIN task_types tt ON tt.id = a.type_id
		           WHERE tt.slug = 'epic'
		           ORDER BY a.depth LIMIT 1
		        )
		  WHERE id = ?`,
		taskID, sessionstate.Ended, sessionstate.NeedsInput, taskID, p.SessionID,
	)
	if err != nil {
		return nil, fmt.Errorf("events: claim task: %w", err)
	}
	logSessionClaim(res, snap, taskID)
	if shouldRecordSessionTouch(evt) {
		// Claimed → the session's claimed task.
		if err := upsertSessionTask(db, evt.Actor.SessionID, mustParseInt64(evt.Entity.ID), sessiontaskrelation.RelationClaimed); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}
	return &ExecuteResult{}, nil
}

func execTaskLanded(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p TaskLandedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.landed payload: %w", err)
	}
	taskID := mustParseInt64(evt.Entity.ID)
	var sessionID any
	if evt.Actor.SessionID != "" {
		sessionID = mustParseInt64(evt.Actor.SessionID)
	}
	// A record-only/historical landing (E-1719) knows no base branch — the land
	// it records happened before anything was asked to remember one — so record
	// NULL rather than an empty string (E-2005).
	baseBranch := nullIfEmpty(p.BaseBranch)
	// landed_at is the event timestamp, not now(): a historical record-only
	// landing (E-1719) sets evt.TS to the commit date via `emit --ts`, so the
	// row records when the work actually landed. For a normal live land evt.TS
	// is the emit instant, so this is unchanged in practice — and it makes the
	// live insert agree with replayTaskLanded, which already uses evt.TS.
	landedAt := kairosToISO(evt.TS)
	if _, err := db.Exec(
		`INSERT INTO task_landings
		     (task_id, session_id, base_branch, merge_commit_sha,
		      landed_at, landed_by_harness)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		taskID, sessionID, baseBranch, p.MergeCommitSHA, landedAt,
		nullIfEmpty(evt.Actor.Harness),
	); err != nil {
		return nil, fmt.Errorf("events: insert task_landing: %w", err)
	}
	if shouldRecordSessionTouch(evt) {
		// Landing is a touch; `claimed` is set only by the claim event, so a land
		// that did not claim in-session counts as a revisit (set-once leaves a
		// prior claimed/surfaced classification intact).
		if err := upsertSessionTask(db, evt.Actor.SessionID, taskID, sessiontaskrelation.RelationRevisited); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}
	return &ExecuteResult{}, nil
}

// execTaskReleased clears a session's task binding.
//
// E-1968 / ED-1560 left NO live producer of `task.released`: `task reopen`
// stopped emitting it, and `task release` is disabled. Do not treat it as a
// supported path for new writes.
//
// It is also unreachable for historical ones, and the reason is worth stating
// because an earlier revision of this comment got it backwards. `sessions` is
// machine-local runtime state, NOT a projection of the ledger: projector.go has
// cases for task and decision events only, and none for `task.claimed` or
// `task.released`. A `rebuild-db` therefore never replays a session bind or
// release, never reaches this executor, and never meets E-1969's write-once
// trigger here. Nothing had to be exempted or migrated for that trigger to
// land, and E-1967 restores the bindings these releases destroyed without any
// fear that a rebuild will destroy them again. The function survives as the
// executor for a kind the dispatcher still routes, not as a replay path.
func execTaskReleased(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p TaskReleasedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task.released payload: %w", err)
	}
	taskID := evt.Entity.ID
	// Clear epic_id alongside task_id so a released coordinator
	// session does not keep a stale epic prefix / auto-resolve target.
	snap := monitor.SnapshotSessionByID(db, p.SessionID)
	res, err := db.Exec(
		"UPDATE sessions SET task_id = NULL, epic_id = NULL WHERE id = ? AND task_id = ?",
		p.SessionID, taskID,
	)
	if err != nil {
		return nil, fmt.Errorf("events: release task: %w", err)
	}
	logSessionRelease(res, snap)
	if shouldRecordSessionTouch(evt) {
		// Touched a pre-existing task (not claimed) → revisited.
		if err := upsertSessionTask(db, evt.Actor.SessionID, mustParseInt64(evt.Entity.ID), sessiontaskrelation.RelationRevisited); err != nil {
			return nil, fmt.Errorf("events: %w", err)
		}
	}
	return &ExecuteResult{}, nil
}

// logSessionClaim records the claim UPDATE in the machine-local diagnostic log
// (E-1857). Skipped when the UPDATE matched no session row. new_state mirrors the
// SQL's revive CASE: an 'ended' row becomes 'needs_input', any other state is
// left as-is.
func logSessionClaim(res sql.Result, snap monitor.SessionSnapshot, taskID string) {
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return
	}
	newState := snap.State
	if newState == sessionstate.Ended {
		newState = sessionstate.NeedsInput
	}
	newTaskID := mustParseInt64(taskID)
	monitor.LogSessionTxn(monitor.SessionTxn{
		SessionGUID: snap.SessionGUID,
		OldState:    snap.State,
		NewState:    newState,
		OldTaskID:   snap.TaskID,
		NewTaskID:   &newTaskID,
		Reason:      monitor.SessionLogClaimEvent,
		Caller:      "events.execTaskClaimed",
	})
}

// logSessionRelease records the release UPDATE in the machine-local diagnostic
// log (E-1857). Skipped when the UPDATE matched no row (the session was not
// bound to this task). new_task_id is nil: release NULLs task_id.
func logSessionRelease(res sql.Result, snap monitor.SessionSnapshot) {
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return
	}
	monitor.LogSessionTxn(monitor.SessionTxn{
		SessionGUID: snap.SessionGUID,
		OldState:    snap.State,
		NewState:    snap.State, // release does not change state
		OldTaskID:   snap.TaskID,
		NewTaskID:   nil,
		Reason:      monitor.SessionLogRelease,
		Caller:      "events.execTaskReleased",
	})
}

// execTaskDepCreated inserts a task→task relation row and records a session
// touch for BOTH endpoints: linking or blocking a task is an interaction with
// both the subject and the referenced task, so both enroll in session_tasks as
// 'revisited'. Python resolves the stored dep_type and applies any inverse-view
// swap before emitting, so the payload's source_id/target_id are already in
// storage (active-voice) order — this executor writes them verbatim.
func execTaskDepCreated(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p TaskDepCreatedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task_dep.created payload: %w", err)
	}
	if p.DepType == "" {
		return nil, fmt.Errorf("events: task_dep.created requires dep_type")
	}
	if _, err := db.Exec(
		`INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
		 VALUES ('task', ?, 'task', ?, ?)`,
		p.SourceID, p.TargetID, p.DepType,
	); err != nil {
		return nil, fmt.Errorf("events: insert task_dep: %w", err)
	}
	if err := recordDepTouch(db, evt, p.SourceID, p.TargetID); err != nil {
		return nil, err
	}
	return &ExecuteResult{}, nil
}

// execTaskDepDeleted removes a task→task relation row (keyed on dep_type, since
// a task pair may hold several relation types) and records the same 'revisited'
// touch for both endpoints — unlinking/unblocking is an interaction too.
func execTaskDepDeleted(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	var p TaskDepDeletedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return nil, fmt.Errorf("events: unmarshal task_dep.deleted payload: %w", err)
	}
	if p.DepType == "" {
		return nil, fmt.Errorf("events: task_dep.deleted requires dep_type")
	}
	result, err := db.Exec(
		`DELETE FROM task_deps
		  WHERE source_type = 'task' AND source_id = ?
		    AND target_type = 'task' AND target_id = ? AND dep_type = ?`,
		p.SourceID, p.TargetID, p.DepType,
	)
	if err != nil {
		return nil, fmt.Errorf("events: delete task_dep: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return nil, fmt.Errorf("events: delete task_dep: no matching row")
	}
	if err := recordDepTouch(db, evt, p.SourceID, p.TargetID); err != nil {
		return nil, err
	}
	return &ExecuteResult{}, nil
}

// recordDepTouch enrolls both relation endpoints in session_tasks as
// 'revisited' when the actor carries a session. session_tasks is set-once per
// relation strength, so a task already enrolled as claimed/surfaced keeps its
// stronger classification (upsertSessionTask handles that).
func recordDepTouch(db dbQuerier, evt *Event, sourceID, targetID int64) error {
	if !shouldRecordSessionTouch(evt) {
		return nil
	}
	for _, id := range []int64{sourceID, targetID} {
		if err := upsertSessionTask(db, evt.Actor.SessionID, id, sessiontaskrelation.RelationRevisited); err != nil {
			return fmt.Errorf("events: %w", err)
		}
	}
	return nil
}

func joinStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += sep + s
	}
	return result
}

func mustParseInt64(s string) int64 {
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n
}

// nullIfEmpty binds a string as SQL NULL when it is empty, and as itself
// otherwise.
//
// The distinction is load-bearing wherever a column means "nobody recorded
// this": an empty string is a recorded value, so `IS NULL` stops answering the
// question and every reader has to remember to spell it as NULL-or-empty.
// task_landings.branch established the rule for a record-only backfill (E-1719)
// and E-2108 retired that column; base_branch and landed_by_harness (E-2005)
// inherited the rule and keep it.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
