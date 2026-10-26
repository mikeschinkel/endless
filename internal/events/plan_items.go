package events

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/planitem"
	"github.com/mikeschinkel/endless/internal/questionstatus"
)

// Executor, replay and the under-the-lock preparation for itemized plans and
// their reconciliation (E-2280).
//
// The same split as task questions (question.go): one apply function per kind,
// called by both the live executor and the projector, so the two cannot drift.
// Everything the ledger must carry for a replay to reproduce the rows — item
// numbers, origins, question ids, which earlier question an entry settles on — is
// decided by a Prepare function under the write lock and written INTO the
// payload before the ledger append. Apply never decides; it only writes what the
// payload says.

// LockedEmit is the write lock a prepared emit holds: the connection inside
// BEGIN IMMEDIATE, plus the two ways out of it.
type LockedEmit struct {
	DB            *sql.DB
	ExecAndCommit func(*Event, DerivedEmitter) (*ExecuteResult, error)
	Rollback      func()
}

// BeginLocked takes the write lock for an emit whose payload must be prepared
// against the current rows — the generalization of PreAllocateQuestions.
func BeginLocked() (*LockedEmit, error) {
	db, err := monitor.DB()
	if err != nil {
		return nil, fmt.Errorf("events: db connection: %w", err)
	}
	if _, err := db.Exec("BEGIN IMMEDIATE"); err != nil {
		return nil, fmt.Errorf("events: begin immediate: %w", err)
	}
	return &LockedEmit{
		DB: db,
		ExecAndCommit: func(evt *Event, emit DerivedEmitter) (*ExecuteResult, error) {
			result, err := dispatch(db, evt, emit)
			if err != nil {
				db.Exec("ROLLBACK")
				return nil, err
			}
			if _, err := db.Exec("COMMIT"); err != nil {
				return nil, fmt.Errorf("events: commit: %w", err)
			}
			return result, nil
		},
		Rollback: func() { db.Exec("ROLLBACK") },
	}, nil
}

// ---------------------------------------------------------------------------
// Payloads

// Plan item operations.
const (
	PlanOpAdd      = "add"
	PlanOpEdit     = "edit"
	PlanOpWithdraw = "withdraw"
	PlanOpMove     = "move"
)

// TaskPlanItemsChangedPayload is one batch of item operations on the task named
// by the entity ref, applied in order. A batch so an `--items-file` writes a
// whole set in one event: all of it lands or none of it does.
type TaskPlanItemsChangedPayload struct {
	Ops []PlanItemOp `json:"ops"`
}

// PlanItemOp is one operation. Seq names the item; for an add it is allocated
// under the lock, as are Origin and SortOrder. Edit fields are pointers so an
// edit says which fields it changes.
type PlanItemOp struct {
	Op        string  `json:"op"`
	Seq       int64   `json:"seq,omitempty"`
	Kind      *string `json:"kind,omitempty"`
	Statement *string `json:"statement,omitempty"`
	Verify    *string `json:"verify,omitempty"`
	Detail    *string `json:"detail,omitempty"`
	Origin    string  `json:"origin,omitempty"`
	SortOrder int64   `json:"sort_order,omitempty"`

	// PostLand sets or clears the post-land mark (E-2444) on an add or edit.
	// ApprovePostLand, on an edit, also records the mark as the user approved
	// it — `question answer --post-land`, the user's answer to a blocked item.
	PostLand        *bool `json:"post_land,omitempty"`
	ApprovePostLand bool  `json:"approve_post_land,omitempty"`
}

// TaskPlanBaselinedPayload copies every active item's statement and kind into
// its baseline, and clears the baseline of every withdrawn item. With no Items
// the rows at the event's position in the ledger are the whole input — `task
// approve`, and every event recorded before items could be approved one at a
// time. Items (sequence numbers) limits it to those items: the user approving
// them one at a time (E-2280), a withdrawn one by accepting its withdrawal.
type TaskPlanBaselinedPayload struct {
	Items []int64 `json:"items,omitempty"`
}

// TaskReconciledPayload records one reconciliation of the task named by the
// entity ref. Seq, Series and every question id are allocated under the lock.
type TaskReconciledPayload struct {
	Seq         int64                 `json:"seq"`
	Fingerprint string                `json:"fingerprint"`
	PlanDigest  string                `json:"plan_digest"`
	Head        string                `json:"head,omitempty"`
	PlanChanged bool                  `json:"plan_changed,omitempty"`
	Entries     []ReconciliationEntry `json:"entries"`
	Series      int64                 `json:"series,omitempty"`
	Questions   []ReconcileQuestion   `json:"questions,omitempty"`
}

// ReconciliationEntry is the verdict on one item. On the way in, AnswerHolds /
// Reask say what the agent decided about an earlier answer; Prepare turns that
// into QuestionID and HoldsReason, and clears the input-only fields.
type ReconciliationEntry struct {
	ItemSeq     int64  `json:"item_seq"`
	Verdict     string `json:"verdict"`
	Note        string `json:"note,omitempty"`
	QuestionID  int64  `json:"question_id,omitempty"`
	HoldsReason string `json:"holds_reason,omitempty"`

	AnswerHolds bool   `json:"answer_holds,omitempty"`
	Reask       bool   `json:"reask,omitempty"`
	Reason      string `json:"reason,omitempty"`
}

// ReconcileQuestion is one discrepancy question a reconciliation files.
type ReconcileQuestion struct {
	ID       int64  `json:"id"`
	Question string `json:"question"`
	Kind     string `json:"kind"`
}

// ---------------------------------------------------------------------------
// Shared reads

type taskFacts struct {
	Status string
	Type   string
}

func readTaskFacts(db dbQuerier, taskID int64) (taskFacts, error) {
	var f taskFacts
	err := db.QueryRow(
		`SELECT t.status, COALESCE(tt.slug, '')
		   FROM live_tasks t LEFT JOIN task_types tt ON tt.id = t.type_id
		  WHERE t.id = ?`, taskID,
	).Scan(&f.Status, &f.Type)
	if errors.Is(err, sql.ErrNoRows) {
		return f, fmt.Errorf("task E-%d not found", taskID)
	}
	if err != nil {
		return f, fmt.Errorf("look up task E-%d: %w", taskID, err)
	}
	return f, nil
}

type itemRow struct {
	Seq        int64
	Kind       string
	Statement  string
	Origin     string
	State      string
	ApprovedAt sql.NullString
	PostLand   bool
}

func readItems(db dbQuerier, taskID int64) (map[int64]*itemRow, []int64, error) {
	rows, err := db.Query(
		`SELECT seq, kind, statement, origin, state, approved_at, post_land
		   FROM task_plan_items WHERE task_id = ? ORDER BY sort_order, seq`, taskID)
	if err != nil {
		return nil, nil, fmt.Errorf("read plan items of E-%d: %w", taskID, err)
	}
	defer rows.Close()
	m := map[int64]*itemRow{}
	var order []int64
	for rows.Next() {
		r := &itemRow{}
		if err := rows.Scan(&r.Seq, &r.Kind, &r.Statement, &r.Origin, &r.State, &r.ApprovedAt, &r.PostLand); err != nil {
			return nil, nil, err
		}
		m[r.Seq] = r
		order = append(order, r.Seq)
	}
	return m, order, rows.Err()
}

func actorSession(evt *Event) any {
	if evt.Actor.SessionID != "" {
		if sid, err := strconv.ParseInt(evt.Actor.SessionID, 10, 64); err == nil {
			return sid
		}
	}
	return nil
}

func entityTaskID(evt *Event) (int64, error) {
	id, err := strconv.ParseInt(evt.Entity.ID, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("events: %s: bad task id %q", evt.Kind, evt.Entity.ID)
	}
	return id, nil
}

func oneLine(field, s string) error {
	if strings.TrimSpace(s) == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	if strings.ContainsAny(s, "\n\r") {
		return fmt.Errorf("%s must be one line", field)
	}
	return nil
}

// ---------------------------------------------------------------------------
// task.plan_items_changed

// PreparePlanItemsChanged validates a batch against the current rows and fills
// in what the database owns: the number, origin and sort order of each added
// item. It refuses a batch on a task whose items are frozen (from `unverified`
// on), an edit, withdrawal or move of an unknown item, and an edit or
// withdrawal of a withdrawn one.
//
// Editing or withdrawing an approved item while the task is underway is NOT
// refused (Mike's choice on E-2280, item 7): the change leaves the item set
// differing from the baseline, and the gate holds until the user re-approves.
//
// A `landed` task's items are frozen apart from one thing (E-2444): the user,
// and no agent, may withdraw a post-land item. byAgent says whether an agent
// harness is emitting the batch.
func PreparePlanItemsChanged(db dbQuerier, taskID int64, p *TaskPlanItemsChangedPayload) error {
	return PreparePlanItemsChangedBy(db, taskID, p, false)
}

// PreparePlanItemsChangedBy is PreparePlanItemsChanged with the emitter named.
func PreparePlanItemsChangedBy(db dbQuerier, taskID int64, p *TaskPlanItemsChangedPayload, byAgent bool) error {
	if len(p.Ops) == 0 {
		return errors.New("no plan item operations")
	}
	facts, err := readTaskFacts(db, taskID)
	if err != nil {
		return err
	}
	items, _, err := readItems(db, taskID)
	if err != nil {
		return err
	}
	if facts.Status == planitem.LandedStatus {
		return prepareLandedWithdrawal(taskID, p, items, byAgent)
	}
	if planitem.IsFrozen(facts.Status) {
		return fmt.Errorf("E-%d is %s, so its plan items are frozen; reopen it with --status revisit to change them", taskID, facts.Status)
	}
	var nextSeq, nextSort int64
	if err := db.QueryRow(
		`SELECT COALESCE(MAX(seq), 0) + 1, COALESCE(MAX(sort_order), 0) + 1
		   FROM task_plan_items WHERE task_id = ?`, taskID,
	).Scan(&nextSeq, &nextSort); err != nil {
		return fmt.Errorf("allocate plan item number: %w", err)
	}
	hadItems := len(items) > 0
	for i := range p.Ops {
		op := &p.Ops[i]
		switch op.Op {
		case PlanOpAdd:
			if op.Seq != 0 {
				return errors.New("an added item must not carry a number; it is allocated here")
			}
			if op.Kind == nil || op.Statement == nil {
				return errors.New("an added item needs a kind and a statement")
			}
			if err := planitem.ValidateKind(*op.Kind); err != nil {
				return err
			}
			if err := oneLine("statement", *op.Statement); err != nil {
				return err
			}
			if err := postLandFits(*op.Kind, op.PostLand); err != nil {
				return err
			}
			if op.ApprovePostLand {
				return errors.New("an added item's post-land mark is approved with the item, not on its own")
			}
			op.Seq = nextSeq
			nextSeq++
			if op.SortOrder == 0 {
				op.SortOrder = nextSort
				nextSort++
			}
			op.Origin = planitem.OriginPlanned
			if hadItems && planitem.GrowsItems(facts.Status) {
				op.Origin = planitem.OriginGrown
			}
			items[op.Seq] = &itemRow{Seq: op.Seq, Kind: *op.Kind, State: planitem.StateActive,
				PostLand: op.PostLand != nil && *op.PostLand}
		case PlanOpEdit, PlanOpWithdraw, PlanOpMove:
			it, ok := items[op.Seq]
			if !ok {
				return fmt.Errorf("E-%d has no plan item %s", taskID, planitem.Label(op.Seq))
			}
			if op.Origin != "" {
				return errors.New("origin is set only when an item is added")
			}
			if op.Op != PlanOpMove && it.State == planitem.StateWithdrawn {
				return fmt.Errorf("%s is withdrawn", planitem.Label(op.Seq))
			}
			switch op.Op {
			case PlanOpEdit:
				if op.Kind == nil && op.Statement == nil && op.Verify == nil && op.Detail == nil && op.PostLand == nil {
					return fmt.Errorf("edit of %s changes nothing", planitem.Label(op.Seq))
				}
				if op.Kind != nil {
					if err := planitem.ValidateKind(*op.Kind); err != nil {
						return err
					}
					it.Kind = *op.Kind
				}
				if op.PostLand != nil {
					it.PostLand = *op.PostLand
				}
				if err := postLandFits(it.Kind, &it.PostLand); err != nil {
					return fmt.Errorf("%s: %w", planitem.Label(op.Seq), err)
				}
				if op.ApprovePostLand && op.PostLand == nil {
					return fmt.Errorf("approving %s's post-land mark needs the mark in the same edit", planitem.Label(op.Seq))
				}
				if op.Statement != nil {
					if err := oneLine("statement", *op.Statement); err != nil {
						return err
					}
				}
			case PlanOpWithdraw:
				it.State = planitem.StateWithdrawn
			}
			if op.Op != PlanOpEdit && (op.PostLand != nil || op.ApprovePostLand) {
				return fmt.Errorf("%s of %s cannot change the post-land mark; edit the item", op.Op, planitem.Label(op.Seq))
			}
			if op.Op == PlanOpMove && op.SortOrder <= 0 {
				return fmt.Errorf("move of %s needs a positive sort order", planitem.Label(op.Seq))
			}
		default:
			return fmt.Errorf("unknown plan item operation %q", op.Op)
		}
	}
	return nil
}

// postLandFits refuses the post-land mark on anything but a deliverable: an
// exclusion is kept or not, never done after the land (E-2444).
func postLandFits(kind string, postLand *bool) error {
	if postLand != nil && *postLand && kind != planitem.KindDeliverable {
		return errors.New("only a deliverable item can be post-land")
	}
	return nil
}

// prepareLandedWithdrawal is the one change a `landed` task's items accept: the
// user withdrawing post-land items (E-2444). An agent may not — the item was
// part of what the user approved and landed, and dropping it is their call.
func prepareLandedWithdrawal(taskID int64, p *TaskPlanItemsChangedPayload, items map[int64]*itemRow, byAgent bool) error {
	for _, op := range p.Ops {
		if op.Op != PlanOpWithdraw {
			return fmt.Errorf("E-%d is landed, so its plan items are frozen apart from withdrawing a post-land item; "+
				"work that changes the plan needs a follow-up task (`endless task add ... --cleans-up E-%d`)", taskID, taskID)
		}
		it, ok := items[op.Seq]
		if !ok {
			return fmt.Errorf("E-%d has no plan item %s", taskID, planitem.Label(op.Seq))
		}
		if it.State != planitem.StateActive {
			return fmt.Errorf("%s is withdrawn", planitem.Label(op.Seq))
		}
		if !it.PostLand {
			return fmt.Errorf("E-%d is landed, and %s is not a post-land item; only post-land items can be withdrawn now", taskID, planitem.Label(op.Seq))
		}
		if byAgent {
			return fmt.Errorf("withdrawing post-land item %s of landed E-%d is the user's to do, not an agent's", planitem.Label(op.Seq), taskID)
		}
	}
	return nil
}

func execTaskPlanItemsChanged(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	if err := applyTaskPlanItemsChanged(db, evt); err != nil {
		return nil, err
	}
	return &ExecuteResult{}, nil
}

func replayTaskPlanItemsChanged(db *sql.DB, evt *Event, _ *ProjectResult) error {
	return applyTaskPlanItemsChanged(db, evt)
}

func applyTaskPlanItemsChanged(db dbQuerier, evt *Event) error {
	var p TaskPlanItemsChangedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("events: unmarshal %s payload: %w", evt.Kind, err)
	}
	taskID, err := entityTaskID(evt)
	if err != nil {
		return err
	}
	if err := requireLiveTask(db, taskID); err != nil {
		return err
	}
	ts := kairosToISO(evt.TS)
	for _, op := range p.Ops {
		label := planitem.Label(op.Seq)
		var res sql.Result
		switch op.Op {
		case PlanOpAdd:
			if op.Kind == nil || op.Statement == nil || op.Seq <= 0 || op.Origin == "" {
				return fmt.Errorf("events: %s: add of %s is incomplete", evt.Kind, label)
			}
			res, err = db.Exec(
				`INSERT INTO task_plan_items
				     (task_id, seq, sort_order, kind, statement, verify, detail, origin,
				      state, created_by_session, created_at, updated_at, post_land)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				taskID, op.Seq, op.SortOrder, *op.Kind, *op.Statement,
				ptrNullIfEmpty(op.Verify), ptrNullIfEmpty(op.Detail), op.Origin,
				planitem.StateActive, actorSession(evt), ts, ts, boolPtrInt(op.PostLand))
		case PlanOpEdit:
			sets, args := []string{}, []any{}
			if op.Kind != nil {
				sets, args = append(sets, "kind = ?"), append(args, *op.Kind)
			}
			if op.Statement != nil {
				sets, args = append(sets, "statement = ?"), append(args, *op.Statement)
			}
			if op.Verify != nil {
				sets, args = append(sets, "verify = ?"), append(args, ptrNullIfEmpty(op.Verify))
			}
			if op.Detail != nil {
				sets, args = append(sets, "detail = ?"), append(args, ptrNullIfEmpty(op.Detail))
			}
			if op.PostLand != nil {
				sets, args = append(sets, "post_land = ?"), append(args, boolPtrInt(op.PostLand))
				if op.ApprovePostLand {
					sets, args = append(sets, "approved_post_land = ?"), append(args, boolPtrInt(op.PostLand))
				}
			}
			if len(sets) == 0 {
				return fmt.Errorf("events: %s: edit of %s changes nothing", evt.Kind, label)
			}
			args = append(args, ts, taskID, op.Seq)
			res, err = db.Exec(
				`UPDATE task_plan_items SET `+strings.Join(sets, ", ")+`, updated_at = ?
				  WHERE task_id = ? AND seq = ?`, args...)
		case PlanOpWithdraw:
			res, err = db.Exec(
				`UPDATE task_plan_items SET state = ?, updated_at = ?
				  WHERE task_id = ? AND seq = ?`,
				planitem.StateWithdrawn, ts, taskID, op.Seq)
		case PlanOpMove:
			// A move is display order only: it does not touch updated_at, so it
			// never makes a reconciliation stale.
			res, err = db.Exec(
				`UPDATE task_plan_items SET sort_order = ? WHERE task_id = ? AND seq = ?`,
				op.SortOrder, taskID, op.Seq)
		default:
			return fmt.Errorf("events: %s: unknown operation %q", evt.Kind, op.Op)
		}
		if err != nil {
			return fmt.Errorf("events: %s %s of E-%d: %w", op.Op, label, taskID, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("events: %s %s of E-%d: no such item", op.Op, label, taskID)
		}
	}
	return nil
}

func boolPtrInt(b *bool) int {
	if b != nil && *b {
		return 1
	}
	return 0
}

func ptrNullIfEmpty(s *string) any {
	if s == nil {
		return nil
	}
	return nullIfEmpty(*s)
}

// ---------------------------------------------------------------------------
// task.plan_baselined

// PreparePlanBaselined refuses a baseline the gate could never pass: a task of
// one that must be itemized, or that has items, with no active deliverable. It also refuses a frozen task.
// Approving named items refuses a number the task does not have; the
// deliverable rule is the whole set's, so it is not applied to them.
func PreparePlanBaselined(db dbQuerier, taskID int64, p *TaskPlanBaselinedPayload) error {
	facts, err := readTaskFacts(db, taskID)
	if err != nil {
		return err
	}
	if planitem.IsFrozen(facts.Status) {
		return fmt.Errorf("E-%d is %s, so its plan items are frozen", taskID, facts.Status)
	}
	if len(p.Items) > 0 {
		for _, seq := range p.Items {
			var n int
			if err := db.QueryRow(
				`SELECT count(*) FROM task_plan_items WHERE task_id = ? AND seq = ?`,
				taskID, seq).Scan(&n); err != nil {
				return fmt.Errorf("read %s of E-%d: %w", planitem.Label(seq), taskID, err)
			}
			if n == 0 {
				return fmt.Errorf("E-%d has no plan item %s", taskID, planitem.Label(seq))
			}
		}
		return nil
	}
	var active, n int
	if err := db.QueryRow(
		`SELECT count(*), COALESCE(SUM(kind = ?), 0) FROM task_plan_items WHERE task_id = ? AND state = ?`,
		planitem.KindDeliverable, taskID, planitem.StateActive,
	).Scan(&active, &n); err != nil {
		return fmt.Errorf("count deliverable items of E-%d: %w", taskID, err)
	}
	if !planitem.MustItemize(facts.Type) && active == 0 {
		return nil
	}
	if n == 0 {
		return fmt.Errorf("E-%d is a %s task with no active deliverable plan item", taskID, facts.Type)
	}
	return nil
}

func execTaskPlanBaselined(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	if err := applyTaskPlanBaselined(db, evt); err != nil {
		return nil, err
	}
	return &ExecuteResult{}, nil
}

func replayTaskPlanBaselined(db *sql.DB, evt *Event, _ *ProjectResult) error {
	return applyTaskPlanBaselined(db, evt)
}

func applyTaskPlanBaselined(db dbQuerier, evt *Event) error {
	taskID, err := entityTaskID(evt)
	if err != nil {
		return err
	}
	if err := requireLiveTask(db, taskID); err != nil {
		return err
	}
	var p TaskPlanBaselinedPayload
	if len(evt.Payload) > 0 {
		if err := json.Unmarshal(evt.Payload, &p); err != nil {
			return fmt.Errorf("events: decode task.plan_baselined payload: %w", err)
		}
	}
	// Which items: all of them, or the ones named. The seq list is bound as
	// arguments, never spliced into the SQL.
	only, args := "", []any{}
	if len(p.Items) > 0 {
		only = " AND seq IN (?" + strings.Repeat(", ?", len(p.Items)-1) + ")"
		for _, seq := range p.Items {
			args = append(args, seq)
		}
	}
	ts := kairosToISO(evt.TS)
	if _, err := db.Exec(
		`UPDATE task_plan_items
		    SET approved_statement = statement, approved_kind = kind, approved_at = ?,
		        approved_post_land = post_land
		  WHERE task_id = ? AND state = ?`+only,
		append([]any{ts, taskID, planitem.StateActive}, args...)...); err != nil {
		return fmt.Errorf("events: baseline E-%d: %w", taskID, err)
	}
	if _, err := db.Exec(
		`UPDATE task_plan_items
		    SET approved_statement = NULL, approved_kind = NULL, approved_at = NULL,
		        approved_post_land = NULL
		  WHERE task_id = ? AND state = ?`+only,
		append([]any{taskID, planitem.StateWithdrawn}, args...)...); err != nil {
		return fmt.Errorf("events: baseline E-%d: %w", taskID, err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// task.reconciled

// earlierEntry is the latest reconciliation's entry for one item, with the
// state of the question it was settled on.
type earlierEntry struct {
	Verdict        string
	QuestionID     int64
	QuestionStatus string
	Answer         string
}

// latestReconciliation returns the newest reconciliation's id and time, or 0.
func latestReconciliation(db dbQuerier, taskID int64) (id int64, createdAt string, err error) {
	err = db.QueryRow(
		`SELECT id, created_at FROM task_reconciliations
		  WHERE task_id = ? ORDER BY seq DESC LIMIT 1`, taskID,
	).Scan(&id, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", nil
	}
	return id, createdAt, err
}

func readEarlierEntries(db dbQuerier, reconciliationID int64) (map[int64]earlierEntry, error) {
	out := map[int64]earlierEntry{}
	if reconciliationID == 0 {
		return out, nil
	}
	rows, err := db.Query(
		`SELECT e.item_seq, e.verdict, COALESCE(e.question_id, 0),
		        COALESCE(q.status, ''), COALESCE(q.answer, q.reason, '')
		   FROM task_reconciliation_entries e
		   LEFT JOIN task_questions q ON q.id = e.question_id
		  WHERE e.reconciliation_id = ?`, reconciliationID)
	if err != nil {
		return nil, fmt.Errorf("read earlier reconciliation: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var seq int64
		var e earlierEntry
		if err := rows.Scan(&seq, &e.Verdict, &e.QuestionID, &e.QuestionStatus, &e.Answer); err != nil {
			return nil, err
		}
		out[seq] = e
	}
	return out, rows.Err()
}

// PlanChangedSince reports whether the plan was (re-)approved after the given
// time — any item's baseline is newer than it.
func planChangedSince(db dbQuerier, taskID int64, since string) (bool, error) {
	if since == "" {
		return false, nil
	}
	var n int
	err := db.QueryRow(
		`SELECT count(*) FROM task_plan_items
		  WHERE task_id = ? AND approved_at IS NOT NULL AND approved_at > ?`,
		taskID, since).Scan(&n)
	return n > 0, err
}

// PrepareReconciled validates a reconciliation against the current items and
// decides, for every discrepancy, which question settles it:
//
//   - not a discrepancy: no question; answer_holds / reask are refused.
//   - the latest reconciliation gave the item the same verdict and its question
//     is still open: that question carries over; reask is refused, since it has
//     not been answered yet.
//   - same verdict, and the earlier question is settled: the entry must say
//     answer_holds (with a reason) or reask (with what changed). Holds keeps the
//     earlier question; reask files a new one that shows the old answer.
//   - anything else, including a changed verdict: a new question.
//
// Discrepancy = a verdict other than as planned / kept, or a grown item the user
// has not approved (kind `more`).
//
// New questions get one series and ids allocated here, and the reconciliation
// its per-task number.
func PrepareReconciled(db dbQuerier, taskID int64, p *TaskReconciledPayload) error {
	if strings.TrimSpace(p.Fingerprint) == "" {
		return errors.New("a reconciliation needs the branch fingerprint")
	}
	if p.Seq != 0 || p.Series != 0 || len(p.Questions) != 0 || p.PlanDigest != "" {
		return errors.New("payload must not carry a number, a series or questions; they are allocated here")
	}
	facts, err := readTaskFacts(db, taskID)
	if err != nil {
		return err
	}
	if planitem.IsFrozen(facts.Status) {
		return fmt.Errorf("E-%d is %s, so it can no longer be reconciled; reopen it with --status revisit first", taskID, facts.Status)
	}
	items, order, err := readItems(db, taskID)
	if err != nil {
		return err
	}
	prevID, prevAt, err := latestReconciliation(db, taskID)
	if err != nil {
		return err
	}
	earlier, err := readEarlierEntries(db, prevID)
	if err != nil {
		return err
	}
	if p.PlanChanged, err = planChangedSince(db, taskID, prevAt); err != nil {
		return err
	}

	seen := map[int64]bool{}
	for _, e := range p.Entries {
		it, ok := items[e.ItemSeq]
		if !ok {
			return fmt.Errorf("E-%d has no plan item %s", taskID, planitem.Label(e.ItemSeq))
		}
		if it.State != planitem.StateActive {
			return fmt.Errorf("%s is withdrawn; leave it out of the reconciliation", planitem.Label(e.ItemSeq))
		}
		if seen[e.ItemSeq] {
			return fmt.Errorf("%s has more than one entry", planitem.Label(e.ItemSeq))
		}
		seen[e.ItemSeq] = true
		if err := planitem.ValidateItemVerdict(it.Kind, it.PostLand, e.Verdict); err != nil {
			return fmt.Errorf("%s: %w", planitem.Label(e.ItemSeq), err)
		}
	}
	var missing []string
	for _, seq := range order {
		if items[seq].State == planitem.StateActive && !seen[seq] {
			missing = append(missing, planitem.Label(seq))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("no entry for %s; every active item needs exactly one", strings.Join(missing, ", "))
	}

	var nextQ int64
	if err := db.QueryRow("SELECT COALESCE(MAX(id), 0) + 1 FROM task_questions").Scan(&nextQ); err != nil {
		return fmt.Errorf("allocate question id: %w", err)
	}
	for i := range p.Entries {
		e := &p.Entries[i]
		it := items[e.ItemSeq]
		qkind := planitem.QuestionKind(e.Verdict)
		if qkind == "" && it.Origin == planitem.OriginGrown && !it.ApprovedAt.Valid {
			qkind = planitem.QuestionMore
		}
		prev, hasPrev := earlier[e.ItemSeq]
		q, err := settleEntry(e, it, qkind, prev, hasPrev, &nextQ)
		if err != nil {
			return err
		}
		if q != nil {
			p.Questions = append(p.Questions, *q)
		}
	}

	if p.PlanDigest, err = monitor.PlanDigest(db, taskID); err != nil {
		return err
	}
	if err := db.QueryRow(
		"SELECT COALESCE(MAX(seq), 0) + 1 FROM task_reconciliations WHERE task_id = ?", taskID,
	).Scan(&p.Seq); err != nil {
		return fmt.Errorf("allocate reconciliation number: %w", err)
	}
	if len(p.Questions) > 0 {
		if err := db.QueryRow(
			"SELECT COALESCE(MAX(series), 0) + 1 FROM task_questions WHERE task_id = ?", taskID,
		).Scan(&p.Series); err != nil {
			return fmt.Errorf("allocate question series: %w", err)
		}
	}
	return nil
}

// settleEntry decides, for one entry, which question settles it — the rules
// PrepareReconciled documents — and returns the question to file, or nil when
// none is filed. qkind is the discrepancy kind, "" for none; prev is the item's
// earlier entry, which a post-land reconcile takes from the item's previous
// post-land row (E-2444). The entry's input-only fields are cleared.
func settleEntry(e *ReconciliationEntry, it *itemRow, qkind string, prev earlierEntry, hasPrev bool, nextQ *int64) (*ReconcileQuestion, error) {
	label := planitem.Label(e.ItemSeq)
	if qkind == "" {
		if e.AnswerHolds || e.Reask {
			return nil, fmt.Errorf("%s is not a discrepancy, so answer_holds and reask do not apply", label)
		}
		e.Reason = ""
		return nil, nil
	}
	if planitem.IsDiscrepancy(e.Verdict) && strings.TrimSpace(e.Note) == "" {
		return nil, fmt.Errorf("%s is %s; its note must say how", label, e.Verdict)
	}
	if e.AnswerHolds && e.Reask {
		return nil, fmt.Errorf("%s: say answer_holds or reask, not both", label)
	}
	sameOpen := hasPrev && prev.Verdict == e.Verdict && prev.QuestionID != 0 && prev.QuestionStatus == questionstatus.Open
	sameSettled := hasPrev && prev.Verdict == e.Verdict && prev.QuestionID != 0 && prev.QuestionStatus != questionstatus.Open
	reaskNote := ""
	switch {
	case sameOpen:
		if e.AnswerHolds || e.Reask {
			return nil, fmt.Errorf("%s: EQ-%d is still open, so there is no earlier answer to keep or re-ask", label, prev.QuestionID)
		}
		e.QuestionID = prev.QuestionID
		e.AnswerHolds, e.Reason = false, ""
		return nil, nil
	case sameSettled:
		if !e.AnswerHolds && !e.Reask {
			return nil, fmt.Errorf("%s has an earlier answer (EQ-%d); say answer_holds = true with why it still holds, or reask = true with what changed", label, prev.QuestionID)
		}
		if strings.TrimSpace(e.Reason) == "" || strings.ContainsAny(e.Reason, "\n\r") {
			return nil, fmt.Errorf("%s: give a one-line reason with answer_holds or reask", label)
		}
		if e.AnswerHolds {
			e.QuestionID = prev.QuestionID
			e.HoldsReason = e.Reason
			e.AnswerHolds, e.Reason = false, ""
			return nil, nil
		}
		reaskNote = fmt.Sprintf(" Asking again: %s. Earlier answer (EQ-%d): %s", e.Reason, prev.QuestionID, prev.Answer)
	default:
		if e.AnswerHolds || e.Reask {
			return nil, fmt.Errorf("%s has no earlier answer with the same verdict, so answer_holds and reask do not apply", label)
		}
	}
	text := fmt.Sprintf("%s (%s) %s: %s", label, qkind, it.Statement, verdictPhrase(e.Verdict, it.Origin))
	if strings.TrimSpace(e.Note) != "" {
		text += " — " + strings.TrimSpace(e.Note)
	}
	text += reaskNote
	e.QuestionID = *nextQ
	q := &ReconcileQuestion{ID: *nextQ, Question: text, Kind: qkind}
	*nextQ++
	e.AnswerHolds, e.Reask, e.Reason = false, false, ""
	return q, nil
}

func verdictPhrase(verdict, origin string) string {
	switch verdict {
	case planitem.VerdictNotKept:
		return "the work did what this exclusion rules out"
	case planitem.VerdictNotDone:
		return "not done"
	case planitem.VerdictBlocked:
		return "blocked"
	case planitem.VerdictDifferently:
		return "done differently"
	}
	if origin == planitem.OriginGrown {
		return "done beyond the approved plan; keep it?"
	}
	return verdict
}

func execTaskReconciled(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	if err := applyTaskReconciled(db, evt); err != nil {
		return nil, err
	}
	return &ExecuteResult{}, nil
}

func replayTaskReconciled(db *sql.DB, evt *Event, _ *ProjectResult) error {
	return applyTaskReconciled(db, evt)
}

func applyTaskReconciled(db dbQuerier, evt *Event) error {
	var p TaskReconciledPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("events: unmarshal %s payload: %w", evt.Kind, err)
	}
	taskID, err := entityTaskID(evt)
	if err != nil {
		return err
	}
	if p.Seq <= 0 || p.Fingerprint == "" || p.PlanDigest == "" {
		return fmt.Errorf("events: %s: missing number or fingerprint", evt.Kind)
	}
	if err := requireLiveTask(db, taskID); err != nil {
		return err
	}
	ts := kairosToISO(evt.TS)
	session := actorSession(evt)
	for _, q := range p.Questions {
		if q.ID <= 0 || p.Series <= 0 || strings.TrimSpace(q.Question) == "" {
			return fmt.Errorf("events: %s: malformed question", evt.Kind)
		}
		if err := planitem.ValidateQuestionKind(q.Kind); err != nil {
			return fmt.Errorf("events: %s: %w", evt.Kind, err)
		}
		if _, err := db.Exec(
			`INSERT INTO task_questions
			     (id, task_id, series, question, status, asked_by_session,
			      created_at, updated_at, kind)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			q.ID, taskID, p.Series, q.Question, questionstatus.Open, session, ts, ts, q.Kind,
		); err != nil {
			return fmt.Errorf("events: insert question EQ-%d: %w", q.ID, err)
		}
	}
	planChanged := 0
	if p.PlanChanged {
		planChanged = 1
	}
	res, err := db.Exec(
		`INSERT INTO task_reconciliations
		     (task_id, seq, fingerprint, plan_digest, head, plan_changed, session_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		taskID, p.Seq, p.Fingerprint, p.PlanDigest, nullIfEmpty(p.Head), planChanged, session, ts)
	if err != nil {
		return fmt.Errorf("events: insert reconciliation of E-%d: %w", taskID, err)
	}
	recID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("events: reconciliation id: %w", err)
	}
	for _, e := range p.Entries {
		var qid any
		if e.QuestionID > 0 {
			qid = e.QuestionID
		}
		if _, err := db.Exec(
			`INSERT INTO task_reconciliation_entries
			     (reconciliation_id, item_seq, verdict, note, question_id, holds_reason)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			recID, e.ItemSeq, e.Verdict, nullIfEmpty(e.Note), qid, nullIfEmpty(e.HoldsReason),
		); err != nil {
			return fmt.Errorf("events: insert entry %s: %w", planitem.Label(e.ItemSeq), err)
		}
	}
	return nil
}
