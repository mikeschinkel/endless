package monitor

import (
	"database/sql"
	"fmt"

	"github.com/mikeschinkel/endless/internal/questionstatus"
)

// TaskQuestion is one task_questions row (E-2176), joined to the task it
// belongs to so a project-wide listing can say which task each question parks.
type TaskQuestion struct {
	ID             int64   `json:"id"`
	TaskID         int64   `json:"task_id"`
	TaskTitle      string  `json:"task_title"`
	Project        string  `json:"project"`
	Series         int64   `json:"series"`
	Question       string  `json:"question"`
	Answer         *string `json:"answer"`
	Status         string  `json:"status"`
	AnsweredBy     *string `json:"answered_by"`
	Reason         *string `json:"reason"`
	AskedBySession *int64  `json:"asked_by_session"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
}

// TaskQuestionFilter narrows TaskQuestions. The zero value is every open
// question in every project — the feed an attention surface reads.
type TaskQuestionFilter struct {
	TaskID  int64  // 0: every task
	Project string // "": every project
	All     bool   // false: open questions only
}

// TaskQuestions reads questions against the global monitor DB.
func TaskQuestions(f TaskQuestionFilter) ([]TaskQuestion, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	return taskQuestions(db, f)
}

// taskQuestions is the db-taking core, split out for tests.
//
// It reads through live_tasks, so a removed task's questions drop out of every
// listing along with the task — the same rule every other task read follows.
func taskQuestions(db *sql.DB, f TaskQuestionFilter) ([]TaskQuestion, error) {
	query := `SELECT q.id, q.task_id, t.title, p.name, q.series, q.question,
	                 q.answer, q.status, q.answered_by, q.reason, q.asked_by_session,
	                 q.created_at, q.updated_at
	            FROM task_questions q
	            JOIN live_tasks t ON t.id = q.task_id
	            JOIN projects p ON p.id = t.project_id
	           WHERE 1 = 1`
	var args []any
	if f.TaskID != 0 {
		query += " AND q.task_id = ?"
		args = append(args, f.TaskID)
	}
	if f.Project != "" {
		query += " AND p.name = ?"
		args = append(args, f.Project)
	}
	if !f.All {
		query += " AND q.status = ?"
		args = append(args, questionstatus.Open)
	}
	query += " ORDER BY q.task_id ASC, q.series ASC, q.id ASC"

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("read task questions: %w", err)
	}
	defer rows.Close()

	out := []TaskQuestion{}
	for rows.Next() {
		var q TaskQuestion
		if err = rows.Scan(&q.ID, &q.TaskID, &q.TaskTitle, &q.Project, &q.Series,
			&q.Question, &q.Answer, &q.Status, &q.AnsweredBy, &q.Reason, &q.AskedBySession,
			&q.CreatedAt, &q.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan task question: %w", err)
		}
		out = append(out, q)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("read task questions: %w", err)
	}
	return out, nil
}

// QuestionTarget names the task, and the project it belongs to, that a question
// command acts on: the project decides which ledger the event is written to.
type QuestionTarget struct {
	TaskID  int64  `json:"task_id"`
	Project string `json:"project"`
	// Status is the question's current status; empty when the target was named
	// by task rather than by question.
	Status string `json:"status,omitempty"`
}

// ResolveQuestionTarget looks up a live task by id (taskID != 0) or the live
// task that owns a question (questionID != 0). Exactly one must be given.
func ResolveQuestionTarget(taskID, questionID int64) (QuestionTarget, error) {
	db, err := DB()
	if err != nil {
		return QuestionTarget{}, err
	}
	return resolveQuestionTarget(db, taskID, questionID)
}

func resolveQuestionTarget(db *sql.DB, taskID, questionID int64) (QuestionTarget, error) {
	var tgt QuestionTarget
	switch {
	case (taskID == 0) == (questionID == 0):
		return tgt, fmt.Errorf("name exactly one of a task or a question")
	case taskID != 0:
		err := db.QueryRow(
			`SELECT t.id, p.name FROM live_tasks t JOIN projects p ON p.id = t.project_id
			  WHERE t.id = ?`, taskID,
		).Scan(&tgt.TaskID, &tgt.Project)
		if err == sql.ErrNoRows {
			return tgt, fmt.Errorf("no task E-%d", taskID)
		}
		return tgt, err
	default:
		err := db.QueryRow(
			`SELECT t.id, p.name, q.status FROM task_questions q
			   JOIN live_tasks t ON t.id = q.task_id
			   JOIN projects p ON p.id = t.project_id
			  WHERE q.id = ?`, questionID,
		).Scan(&tgt.TaskID, &tgt.Project, &tgt.Status)
		if err == sql.ErrNoRows {
			return tgt, fmt.Errorf("no question EQ-%d", questionID)
		}
		return tgt, err
	}
}
