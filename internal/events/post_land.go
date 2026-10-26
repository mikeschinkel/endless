package events

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mikeschinkel/endless/internal/planitem"
	"github.com/mikeschinkel/endless/internal/questionstatus"
	"github.com/mikeschinkel/endless/internal/reconcilegate"
)

// The post-land reconcile (E-2444, ED-1636): the verdicts a `landed` task's
// post-land items get once the land, migration and install they waited for
// have happened.
//
// It is its own event kind, not a second task.reconciled, because a full
// reconciliation would copy every settled verdict into the ledger and the
// database once per attempt. This one writes a row only for each item it
// names, keyed to the reconciliation that was in force at the land, and leaves
// that reconciliation and every other item's verdict as they were.
//
// The split is task.reconciled's: Prepare decides everything under the write
// lock — the attempt number, the question ids, which earlier question an entry
// settles on — and apply only writes what the payload says.

// TaskPostLandReconciledPayload records one post-land reconcile of the task
// named by the entity ref. ReconciliationSeq, Attempt, Series and every
// question id are allocated under the lock.
type TaskPostLandReconciledPayload struct {
	ReconciliationSeq int64                 `json:"reconciliation_seq"`
	Attempt           int64                 `json:"attempt"`
	Fingerprint       string                `json:"fingerprint"`
	Head              string                `json:"head,omitempty"`
	Entries           []ReconciliationEntry `json:"entries"`
	Series            int64                 `json:"series,omitempty"`
	Questions         []ReconcileQuestion   `json:"questions,omitempty"`
}

// latestPostLandEntries reads, for each item, its newest post-land row under a
// reconciliation, with the state of the question it was settled on — the
// "earlier entry" a post-land discrepancy is compared with.
func latestPostLandEntries(db dbQuerier, reconciliationID int64) (map[int64]earlierEntry, error) {
	out := map[int64]earlierEntry{}
	rows, err := db.Query(
		`SELECT e.item_seq, e.verdict, COALESCE(e.question_id, 0),
		        COALESCE(q.status, ''), COALESCE(q.answer, q.reason, '')
		   FROM task_post_land_entries e
		   LEFT JOIN task_questions q ON q.id = e.question_id
		  WHERE e.reconciliation_id = ?
		    AND e.attempt = (SELECT MAX(x.attempt) FROM task_post_land_entries x
		                      WHERE x.reconciliation_id = e.reconciliation_id
		                        AND x.item_seq = e.item_seq)`, reconciliationID)
	if err != nil {
		return nil, fmt.Errorf("read earlier post-land verdicts: %w", err)
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

// PreparePostLandReconciled validates a post-land reconcile and decides its
// questions. It refuses a task that is not `landed`, a branch with changes of
// its own since the land (code is a follow-up task's, not a post-land item's),
// an entry for an item that is not an active post-land item, and a verdict that
// does not fit. Entries may name any subset of the post-land items, but at
// least one: an item it leaves out keeps the verdict it had.
func PreparePostLandReconciled(db dbQuerier, taskID int64, p *TaskPostLandReconciledPayload) error {
	if p.ReconciliationSeq != 0 || p.Attempt != 0 || p.Series != 0 || len(p.Questions) != 0 {
		return errors.New("payload must not carry a number, a series or questions; they are allocated here")
	}
	facts, err := readTaskFacts(db, taskID)
	if err != nil {
		return err
	}
	if facts.Status != planitem.LandedStatus {
		return fmt.Errorf("E-%d is %s, not landed, so it has no post-land items to reconcile", taskID, facts.Status)
	}
	if p.Fingerprint != reconcilegate.EmptyFingerprint {
		return fmt.Errorf("E-%d's branch has changes outside .endless/ since the land; a post-land item produces no code, "+
			"so that work belongs in a follow-up task (`endless task add ... --cleans-up E-%d`)", taskID, taskID)
	}
	if len(p.Entries) == 0 {
		return errors.New("a post-land reconcile needs at least one entry")
	}
	var recID int64
	err = db.QueryRow(
		`SELECT id, seq FROM task_reconciliations WHERE task_id = ? ORDER BY seq DESC LIMIT 1`, taskID,
	).Scan(&recID, &p.ReconciliationSeq)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("E-%d has no reconciliation to record post-land verdicts against", taskID)
	}
	if err != nil {
		return fmt.Errorf("read reconciliation of E-%d: %w", taskID, err)
	}
	items, _, err := readItems(db, taskID)
	if err != nil {
		return err
	}
	earlier, err := latestPostLandEntries(db, recID)
	if err != nil {
		return err
	}

	seen := map[int64]bool{}
	for _, e := range p.Entries {
		label := planitem.Label(e.ItemSeq)
		it, ok := items[e.ItemSeq]
		if !ok {
			return fmt.Errorf("E-%d has no plan item %s", taskID, label)
		}
		if it.State != planitem.StateActive {
			return fmt.Errorf("%s is withdrawn; leave it out", label)
		}
		if !it.PostLand {
			return fmt.Errorf("%s is not a post-land item; a landed task's reconcile takes entries for post-land items only", label)
		}
		if seen[e.ItemSeq] {
			return fmt.Errorf("%s has more than one entry", label)
		}
		seen[e.ItemSeq] = true
		if err := planitem.ValidateItemVerdict(it.Kind, it.PostLand, e.Verdict); err != nil {
			return fmt.Errorf("%s: %w", label, err)
		}
	}

	var nextQ int64
	if err := db.QueryRow("SELECT COALESCE(MAX(id), 0) + 1 FROM task_questions").Scan(&nextQ); err != nil {
		return fmt.Errorf("allocate question id: %w", err)
	}
	for i := range p.Entries {
		e := &p.Entries[i]
		// A grown item's "more" question was settled before the land, so a
		// post-land verdict is a discrepancy only by its own verdict.
		prev, hasPrev := earlier[e.ItemSeq]
		q, err := settleEntry(e, items[e.ItemSeq], planitem.QuestionKind(e.Verdict), prev, hasPrev, &nextQ)
		if err != nil {
			return err
		}
		if q != nil {
			p.Questions = append(p.Questions, *q)
		}
	}
	if err := db.QueryRow(
		"SELECT COALESCE(MAX(attempt), 0) + 1 FROM task_post_land_entries WHERE reconciliation_id = ?", recID,
	).Scan(&p.Attempt); err != nil {
		return fmt.Errorf("allocate post-land attempt: %w", err)
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

func execTaskPostLandReconciled(db dbQuerier, evt *Event) (*ExecuteResult, error) {
	if err := applyTaskPostLandReconciled(db, evt); err != nil {
		return nil, err
	}
	return &ExecuteResult{}, nil
}

func replayTaskPostLandReconciled(db *sql.DB, evt *Event, _ *ProjectResult) error {
	return applyTaskPostLandReconciled(db, evt)
}

func applyTaskPostLandReconciled(db dbQuerier, evt *Event) error {
	var p TaskPostLandReconciledPayload
	if err := json.Unmarshal(evt.Payload, &p); err != nil {
		return fmt.Errorf("events: unmarshal %s payload: %w", evt.Kind, err)
	}
	taskID, err := entityTaskID(evt)
	if err != nil {
		return err
	}
	if p.ReconciliationSeq <= 0 || p.Attempt <= 0 || len(p.Entries) == 0 {
		return fmt.Errorf("events: %s: missing reconciliation, attempt or entries", evt.Kind)
	}
	if err := requireLiveTask(db, taskID); err != nil {
		return err
	}
	var recID int64
	if err := db.QueryRow(
		`SELECT id FROM task_reconciliations WHERE task_id = ? AND seq = ?`, taskID, p.ReconciliationSeq,
	).Scan(&recID); err != nil {
		return fmt.Errorf("events: %s: reconciliation R%d of E-%d: %w", evt.Kind, p.ReconciliationSeq, taskID, err)
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
	for _, e := range p.Entries {
		var qid any
		if e.QuestionID > 0 {
			qid = e.QuestionID
		}
		if _, err := db.Exec(
			`INSERT INTO task_post_land_entries
			     (reconciliation_id, attempt, item_seq, verdict, note, question_id,
			      holds_reason, head, session_id, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			recID, p.Attempt, e.ItemSeq, e.Verdict, nullIfEmpty(e.Note), qid,
			nullIfEmpty(e.HoldsReason), nullIfEmpty(p.Head), session, ts,
		); err != nil {
			return fmt.Errorf("events: insert post-land entry %s: %w", planitem.Label(e.ItemSeq), err)
		}
	}
	return nil
}
