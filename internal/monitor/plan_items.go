package monitor

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/mikeschinkel/endless/internal/planitem"
	"github.com/mikeschinkel/endless/internal/questionstatus"
)

// PlanItem is one task_plan_items row (E-2280), with what the latest
// reconciliation said about it and the question that settles it.
type PlanItem struct {
	Seq               int64   `json:"seq"`
	Label             string  `json:"label"`
	SortOrder         int64   `json:"sort_order"`
	Kind              string  `json:"kind"`
	Statement         string  `json:"statement"`
	Verify            *string `json:"verify"`
	Detail            *string `json:"detail"`
	Origin            string  `json:"origin"`
	State             string  `json:"state"`
	ApprovedStatement *string `json:"approved_statement"`
	ApprovedKind      *string `json:"approved_kind"`
	ApprovedAt        *string `json:"approved_at"`
	CreatedAt         string  `json:"created_at"`
	UpdatedAt         string  `json:"updated_at"`

	// From the latest reconciliation's entry for this item, when there is one.
	Verdict        *string `json:"verdict"`
	Note           *string `json:"note"`
	HoldsReason    *string `json:"holds_reason"`
	QuestionID     *int64  `json:"question_id"`
	QuestionStatus *string `json:"question_status"`
	QuestionKind   *string `json:"question_kind"`
	Answer         *string `json:"answer"`
	AnsweredBy     *string `json:"answered_by"`
	CloseReason    *string `json:"close_reason"`
}

// Reconciliation is the header of one task_reconciliations row.
type Reconciliation struct {
	ID          int64   `json:"id"`
	Seq         int64   `json:"seq"`
	Fingerprint string  `json:"fingerprint"`
	Head        *string `json:"head"`
	PlanChanged bool    `json:"plan_changed"`
	CreatedAt   string  `json:"created_at"`
}

// PlanItems reads a task's items in display order, withdrawn ones included
// when all is set.
func PlanItems(taskID int64, all bool) ([]PlanItem, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	return planItems(db, taskID, all)
}

func planItems(db *sql.DB, taskID int64, all bool) ([]PlanItem, error) {
	rec, err := latestReconciliation(db, taskID)
	if err != nil {
		return nil, err
	}
	var recID int64
	if rec != nil {
		recID = rec.ID
	}
	query := `SELECT i.seq, i.sort_order, i.kind, i.statement, i.verify, i.detail,
	                 i.origin, i.state, i.approved_statement, i.approved_kind, i.approved_at,
	                 i.created_at, i.updated_at,
	                 e.verdict, e.note, e.holds_reason, e.question_id,
	                 q.status, q.kind, q.answer, q.answered_by, q.reason
	            FROM task_plan_items i
	            LEFT JOIN task_reconciliation_entries e
	                   ON e.reconciliation_id = ? AND e.item_seq = i.seq
	            LEFT JOIN task_questions q ON q.id = e.question_id
	           WHERE i.task_id = ?`
	args := []any{recID, taskID}
	if !all {
		query += " AND i.state = ?"
		args = append(args, planitem.StateActive)
	}
	query += " ORDER BY i.sort_order, i.seq"
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("read plan items of E-%d: %w", taskID, err)
	}
	defer rows.Close()
	out := []PlanItem{}
	for rows.Next() {
		var it PlanItem
		if err := rows.Scan(&it.Seq, &it.SortOrder, &it.Kind, &it.Statement, &it.Verify,
			&it.Detail, &it.Origin, &it.State, &it.ApprovedStatement, &it.ApprovedKind,
			&it.ApprovedAt, &it.CreatedAt, &it.UpdatedAt,
			&it.Verdict, &it.Note, &it.HoldsReason, &it.QuestionID,
			&it.QuestionStatus, &it.QuestionKind, &it.Answer, &it.AnsweredBy, &it.CloseReason); err != nil {
			return nil, fmt.Errorf("scan plan item: %w", err)
		}
		it.Label = planitem.Label(it.Seq)
		out = append(out, it)
	}
	return out, rows.Err()
}

// LatestReconciliation returns a task's newest reconciliation, or nil.
func LatestReconciliation(taskID int64) (*Reconciliation, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	return latestReconciliation(db, taskID)
}

func latestReconciliation(db *sql.DB, taskID int64) (*Reconciliation, error) {
	var r Reconciliation
	var changed int
	err := db.QueryRow(
		`SELECT id, seq, fingerprint, head, plan_changed, created_at
		   FROM task_reconciliations WHERE task_id = ? ORDER BY seq DESC LIMIT 1`, taskID,
	).Scan(&r.ID, &r.Seq, &r.Fingerprint, &r.Head, &changed, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read reconciliation of E-%d: %w", taskID, err)
	}
	r.PlanChanged = changed != 0
	return &r, nil
}

// MootQuestion is an open discrepancy question whose item is withdrawn: the
// plan no longer has the thing it asks about.
type MootQuestion struct {
	ID      int64 `json:"id"`
	ItemSeq int64 `json:"item_seq"`
}

// MootQuestions lists a task's open discrepancy questions on withdrawn items —
// what a re-approval closes.
func MootQuestions(taskID int64) ([]MootQuestion, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT DISTINCT q.id, e.item_seq
		   FROM task_questions q
		   JOIN task_reconciliation_entries e ON e.question_id = q.id
		   JOIN task_reconciliations r ON r.id = e.reconciliation_id AND r.task_id = q.task_id
		   JOIN task_plan_items i ON i.task_id = q.task_id AND i.seq = e.item_seq
		  WHERE q.task_id = ? AND q.status = ? AND q.kind IS NOT NULL AND i.state = ?
		  ORDER BY q.id`,
		taskID, questionstatus.Open, planitem.StateWithdrawn)
	if err != nil {
		return nil, fmt.Errorf("read moot questions of E-%d: %w", taskID, err)
	}
	defer rows.Close()
	out := []MootQuestion{}
	for rows.Next() {
		var m MootQuestion
		if err := rows.Scan(&m.ID, &m.ItemSeq); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// Querier is the read half of *sql.DB, so PlanDigest runs both inside the
// event executor's write lock and from a plain read.
type Querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// PlanDigest hashes the parts of a task's item set a reconciliation is made
// against: every item's number, kind, statement, origin and state, and its
// baseline. A reconciliation records it; the gate recomputes it. Content, not a
// timestamp: the ledger's clock is second-resolution, and a change made in the
// same second as a reconciliation must still make it stale.
func PlanDigest(db Querier, taskID int64) (string, error) {
	rows, err := db.Query(
		`SELECT seq, kind, statement, origin, state,
		        COALESCE(approved_statement, ''), COALESCE(approved_kind, ''),
		        approved_at IS NOT NULL
		   FROM task_plan_items WHERE task_id = ? ORDER BY seq`, taskID)
	if err != nil {
		return "", fmt.Errorf("read plan items of E-%d: %w", taskID, err)
	}
	defer rows.Close()
	h := sha256.New()
	for rows.Next() {
		var seq int64
		var kind, statement, origin, state, aStatement, aKind string
		var approved bool
		if err := rows.Scan(&seq, &kind, &statement, &origin, &state, &aStatement, &aKind, &approved); err != nil {
			return "", err
		}
		fmt.Fprintf(h, "%d\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%t\n",
			seq, kind, statement, origin, state, aStatement, aKind, approved)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// PlanItemsTOML renders a task's active items in the `--items-file` format, the
// content of its `items.toml` mirror (E-2280). Empty when the task has no
// active item. One renderer, here, for both the CLI's write and the sweep's
// repair, so the two can never disagree about the bytes.
func PlanItemsTOML(taskID int64) (string, error) {
	db, err := DB()
	if err != nil {
		return "", err
	}
	items, err := planItems(db, taskID, false)
	if err != nil {
		return "", err
	}
	return renderItemsTOML(taskID, items), nil
}

func renderItemsTOML(taskID int64, items []PlanItem) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Plan items for E-%d: a mirror of the database. Do not edit this file;\n", taskID)
	fmt.Fprintf(&b, "# copy it, edit the copy, and load it with\n")
	fmt.Fprintf(&b, "#   endless task update E-%d --items-file <copy>\n", taskID)
	for _, it := range items {
		fmt.Fprintf(&b, "\n[[item]]\nid = %s\nkind = %s\nstatement = %s\n",
			tomlString(it.Label), tomlString(it.Kind), tomlString(it.Statement))
		if it.Verify != nil && *it.Verify != "" {
			fmt.Fprintf(&b, "verify = %s\n", tomlString(*it.Verify))
		}
		if it.Detail != nil && *it.Detail != "" {
			fmt.Fprintf(&b, "detail = %s\n", tomlString(*it.Detail))
		}
	}
	return b.String()
}

// tomlString renders s as a TOML basic string.
func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// TasksWithPlanItems lists the live tasks in a project that have any item —
// the tasks whose `items.toml` mirror the sweep keeps current.
func TasksWithPlanItems(projectID int64) ([]int64, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT DISTINCT i.task_id FROM task_plan_items i
		   JOIN live_tasks t ON t.id = i.task_id
		  WHERE t.project_id = ? ORDER BY 1`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// PlanReadiness is what approval and spawn need to know about a task's items:
// whether its type is gated, and whether it has an active deliverable.
type PlanReadiness struct {
	TaskID       int64  `json:"task_id"`
	Type         string `json:"type"`
	Status       string `json:"status"`
	Gated        bool   `json:"gated"`
	Items        int    `json:"items"`
	Deliverables int    `json:"deliverables"`
	Unapproved   bool   `json:"unapproved"`
}

// ReadPlanReadiness reads one task's PlanReadiness. Unapproved is true when the
// item set differs from its baseline (or has none) — what re-approval changes.
func ReadPlanReadiness(taskID int64) (PlanReadiness, error) {
	r := PlanReadiness{TaskID: taskID}
	db, err := DB()
	if err != nil {
		return r, err
	}
	err = db.QueryRow(
		`SELECT COALESCE(tt.slug, ''), t.status FROM live_tasks t
		   LEFT JOIN task_types tt ON tt.id = t.type_id WHERE t.id = ?`, taskID,
	).Scan(&r.Type, &r.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("no task E-%d", taskID)
	}
	if err != nil {
		return r, err
	}
	r.Gated = planitem.IsGatedType(r.Type)
	var unapproved int
	err = db.QueryRow(
		`SELECT count(*),
		        COALESCE(SUM(state = ? AND kind = ?), 0),
		        COALESCE(SUM(CASE
		            WHEN state = ? AND (approved_at IS NULL OR statement != approved_statement
		                                OR kind != approved_kind) THEN 1
		            WHEN state = ? AND approved_at IS NOT NULL THEN 1
		            ELSE 0 END), 0)
		   FROM task_plan_items WHERE task_id = ?`,
		planitem.StateActive, planitem.KindDeliverable,
		planitem.StateActive, planitem.StateWithdrawn, taskID,
	).Scan(&r.Items, &r.Deliverables, &unapproved)
	r.Unapproved = unapproved > 0
	return r, err
}
