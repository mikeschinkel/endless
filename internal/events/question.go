package events

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/questionstatus"
)

// Executor and replay for task_questions (E-2176).
//
// The live path and the rebuild path call the SAME apply functions below. Two
// hand-kept copies of one mutation is how the executor and the projector came to
// disagree about `notes` (E-1531 §6); a table born after that finding should not
// repeat it.

// PreAllocateQuestions takes the write lock and reserves what a
// task.questions_asked event needs before it is written to the ledger: the
// task's next series number and the first of n consecutive question ids.
//
// Both are allocated in the same BEGIN IMMEDIATE transaction that later runs the
// INSERT, so two sessions asking questions on one task cannot interleave the
// read and the write — SQLite serializes writers, and the database enforces the
// invariant rather than a convention. No dispenser table is needed.
//
// Usage mirrors PreAllocateTaskID.
func PreAllocateQuestions(taskID int64, n int) (series, firstID int64, execAndCommit func(*Event, DerivedEmitter) (*ExecuteResult, error), rollback func(), err error) {
	if n <= 0 {
		return 0, 0, nil, nil, fmt.Errorf("events: task.questions_asked needs at least one question")
	}
	db, err := monitor.DB()
	if err != nil {
		return 0, 0, nil, nil, fmt.Errorf("events: db connection: %w", err)
	}

	if _, err := db.Exec("BEGIN IMMEDIATE"); err != nil {
		return 0, 0, nil, nil, fmt.Errorf("events: begin immediate: %w", err)
	}
	doRollback := func() {
		db.Exec("ROLLBACK")
	}

	if err := requireLiveTask(db, taskID); err != nil {
		doRollback()
		return 0, 0, nil, nil, err
	}
	if err := db.QueryRow(
		"SELECT COALESCE(MAX(series), 0) + 1 FROM task_questions WHERE task_id = ?",
		taskID,
	).Scan(&series); err != nil {
		doRollback()
		return 0, 0, nil, nil, fmt.Errorf("events: allocate question series: %w", err)
	}
	if err := db.QueryRow(
		"SELECT COALESCE(MAX(id), 0) + 1 FROM task_questions",
	).Scan(&firstID); err != nil {
		doRollback()
		return 0, 0, nil, nil, fmt.Errorf("events: allocate question id: %w", err)
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

	return series, firstID, doExecAndCommit, doRollback, nil
}

// requireLiveTask refuses a question on a task that does not exist or was
// removed. live_tasks, not tasks: a removed task is retained only so its id is
// never re-minted, and nothing should be asked of it.
func requireLiveTask(db dbQuerier, taskID int64) error {
	var n int
	if err := db.QueryRow(
		"SELECT count(*) FROM live_tasks WHERE id = ?", taskID,
	).Scan(&n); err != nil {
		return fmt.Errorf("events: look up task E-%d: %w", taskID, err)
	}
	if n == 0 {
		return fmt.Errorf("events: task E-%d not found", taskID)
	}
	return nil
}

func execTaskQuestionsAsked(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	if err := applyTaskQuestionsAsked(db, evt); err != nil {
		return nil, err
	}
	return &ExecuteResult{}, nil
}

func replayTaskQuestionsAsked(db *sql.DB, evt *Event, _ *ProjectResult) error {
	return applyTaskQuestionsAsked(db, evt)
}

func applyTaskQuestionsAsked(db dbQuerier, evt *Event) error {
	var p TaskQuestionsAskedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("events: unmarshal task.questions_asked payload: %w", err)
	}
	taskID, err := strconv.ParseInt(evt.Entity.ID, 10, 64)
	if err != nil || taskID <= 0 {
		return fmt.Errorf("events: task.questions_asked: bad task id %q", evt.Entity.ID)
	}
	if p.Series <= 0 {
		return fmt.Errorf("events: task.questions_asked: series must be positive, got %d", p.Series)
	}
	if len(p.Questions) == 0 {
		return fmt.Errorf("events: task.questions_asked: no questions")
	}
	if err := requireLiveTask(db, taskID); err != nil {
		return err
	}

	var asker any
	if evt.Actor.SessionID != "" {
		if sid, err := strconv.ParseInt(evt.Actor.SessionID, 10, 64); err == nil {
			asker = sid
		}
	}
	// Timestamps come from the event, never now(): replay must reproduce the
	// row the live path wrote, and the event's ts is the one value both share.
	ts := kairosToISO(evt.TS)

	for _, q := range p.Questions {
		if q.ID <= 0 {
			return fmt.Errorf("events: task.questions_asked: question id must be positive, got %d", q.ID)
		}
		if strings.TrimSpace(q.Question) == "" {
			return fmt.Errorf("events: task.questions_asked: question EQ-%d is empty", q.ID)
		}
		if _, err := db.Exec(
			`INSERT INTO task_questions
			     (id, task_id, series, question, status, asked_by_session,
			      created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			q.ID, taskID, p.Series, q.Question, questionstatus.Open, asker, ts, ts,
		); err != nil {
			return fmt.Errorf("events: insert question EQ-%d: %w", q.ID, err)
		}
	}
	return nil
}

func execTaskQuestionResolved(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	if err := applyTaskQuestionResolved(db, evt); err != nil {
		return nil, err
	}
	return &ExecuteResult{}, nil
}

func replayTaskQuestionResolved(db *sql.DB, evt *Event, _ *ProjectResult) error {
	return applyTaskQuestionResolved(db, evt)
}

// validateQuestionResolution checks the payload's own shape, before any row is
// read: a known target status other than open; an answer with its answerer, and
// no reason, when the target is `answered`; a reason, and no answer, for every
// other target. The reason is required on every closing move without exception —
// a question closed with no stated reason cannot be reviewed, and the asker
// cannot tell what to ask instead.
func validateQuestionResolution(p TaskQuestionResolvedPayload) error {
	if err := questionstatus.Validate(p.Status); err != nil {
		return err
	}
	if p.Status == questionstatus.Open {
		return errors.New("a question cannot be resolved back to open; ask it again in a new series")
	}
	if p.Status == questionstatus.Answered {
		if strings.TrimSpace(p.Answer) == "" {
			return errors.New("an answered question needs a non-empty answer")
		}
		if p.Reason != "" {
			return errors.New("an answered question takes no reason; the answer is the record")
		}
		return questionstatus.ValidateAnsweredBy(p.AnsweredBy)
	}
	if p.Answer != "" || p.AnsweredBy != "" {
		return fmt.Errorf("status %q takes no answer or answered_by", p.Status)
	}
	if strings.TrimSpace(p.Reason) == "" {
		return fmt.Errorf("status %q requires a reason", p.Status)
	}
	return nil
}

func applyTaskQuestionResolved(db dbQuerier, evt *Event) error {
	var p TaskQuestionResolvedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("events: unmarshal task_question.resolved payload: %w", err)
	}
	if err := validateQuestionResolution(p); err != nil {
		return fmt.Errorf("events: resolve question EQ-%s: %w", evt.Entity.ID, err)
	}

	from := questionstatus.From(p.Status)
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(from)), ",")
	args := []any{p.Status, nullIfEmpty(p.Answer), nullIfEmpty(p.AnsweredBy),
		nullIfEmpty(p.Reason), kairosToISO(evt.TS), evt.Entity.ID}
	for _, s := range from {
		args = append(args, s)
	}

	// Only `answered` writes answer/answered_by, and only the other moves write
	// reason. Superseding an answered question keeps the answer and adds the
	// reason, so the record of what was decided survives the fold into the plan.
	res, err := db.Exec(
		`UPDATE task_questions
		    SET status = ?,
		        answer = COALESCE(?, answer),
		        answered_by = COALESCE(?, answered_by),
		        reason = ?,
		        updated_at = ?
		  WHERE id = ? AND status IN (`+placeholders+`)`,
		args...,
	)
	if err != nil {
		return fmt.Errorf("events: resolve question EQ-%s: %w", evt.Entity.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var status string
		if err := db.QueryRow(
			"SELECT status FROM task_questions WHERE id = ?", evt.Entity.ID,
		).Scan(&status); err != nil {
			return fmt.Errorf("events: resolve question EQ-%s: not found", evt.Entity.ID)
		}
		return fmt.Errorf("events: resolve question EQ-%s: cannot move from %q to %q",
			evt.Entity.ID, status, p.Status)
	}
	return nil
}

// PrecheckTaskQuestionResolved refuses an illegal resolution BEFORE the event is
// appended to the ledger. The update path writes the ledger first and executes
// second, so without this a refused move would leave a line in the permanent
// record that every replay then fails on. The executor keeps its own guard —
// this closes the common case, it does not replace the check under the lock.
func PrecheckTaskQuestionResolved(evt *Event) error {
	var p TaskQuestionResolvedPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("task_question.resolved: invalid payload: %w", err)
	}
	if err := validateQuestionResolution(p); err != nil {
		return fmt.Errorf("EQ-%s: %w", evt.Entity.ID, err)
	}
	db, err := monitor.DB()
	if err != nil {
		return fmt.Errorf("db connection: %w", err)
	}
	var status string
	if err := db.QueryRow(
		"SELECT status FROM task_questions WHERE id = ?", evt.Entity.ID,
	).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("no question EQ-%s", evt.Entity.ID)
		}
		return fmt.Errorf("look up EQ-%s: %w", evt.Entity.ID, err)
	}
	if !questionstatus.CanMove(status, p.Status) {
		return fmt.Errorf("EQ-%s is %s; it cannot become %s", evt.Entity.ID, status, p.Status)
	}
	return nil
}
