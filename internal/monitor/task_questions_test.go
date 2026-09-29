package monitor

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

// questionsTestDB opens an in-memory DB with the schema applied and two
// projects, so the project filter and the database-wide read are both
// exercisable.
func questionsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err = schema.Migrate(db); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err = db.Exec(
		`INSERT INTO projects (id, name, path) VALUES
		   (1, 'alpha', '/tmp/alpha'),
		   (2, 'beta',  '/tmp/beta')`,
	); err != nil {
		t.Fatalf("seed projects: %v", err)
	}
	return db
}

// seedQuestionsTask inserts one task.
func seedQuestionsTask(
	t *testing.T,
	db *sql.DB,
	id, projectID int64,
	title, status, createdAt string,
	parent *int64,
) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, status, created_at, parent_id)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, projectID, title, status, createdAt, parent,
	); err != nil {
		t.Fatalf("seed task E-%d: %v", id, err)
	}
}

func TestTaskQuestions_FiltersAndOrder(t *testing.T) {
	db := questionsTestDB(t)
	seedQuestionsTask(t, db, 10, 1, "alpha task", "ready", "2026-08-01T00:00:00", nil)
	seedQuestionsTask(t, db, 11, 2, "beta task", "ready", "2026-08-01T00:00:00", nil)
	seedQuestionsTask(t, db, 12, 1, "removed task", "ready", "2026-08-01T00:00:00", nil)
	if _, err := db.Exec(`UPDATE tasks SET removed = 1 WHERE id = 12`); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO task_questions
	    (id, task_id, series, question, status, answer, answered_by, reason) VALUES
	    (1, 10, 1, 'a1', 'answered', 'yes', 'user', NULL),
	    (2, 10, 2, 'a2', 'open', NULL, NULL, NULL),
	    (3, 11, 1, 'b1', 'open', NULL, NULL, NULL),
	    (4, 12, 1, 'gone', 'open', NULL, NULL, NULL),
	    (5, 10, 1, 'a1b', 'open', NULL, NULL, NULL),
	    (6, 10, 3, 'a3', 'withdrawn', NULL, NULL, 'moot')`); err != nil {
		t.Fatalf("seed questions: %v", err)
	}

	ids := func(f TaskQuestionFilter) []int64 {
		t.Helper()
		qs, err := taskQuestions(db, f)
		if err != nil {
			t.Fatalf("taskQuestions(%+v): %v", f, err)
		}
		out := []int64{}
		for _, q := range qs {
			out = append(out, q.ID)
		}
		return out
	}
	cases := []struct {
		name string
		f    TaskQuestionFilter
		want []int64
	}{
		{"every open question, removed task excluded", TaskQuestionFilter{}, []int64{5, 2, 3}},
		{"one task, open", TaskQuestionFilter{TaskID: 10}, []int64{5, 2}},
		{"one task, all", TaskQuestionFilter{TaskID: 10, All: true}, []int64{1, 5, 2, 6}},
		{"one project", TaskQuestionFilter{Project: "beta"}, []int64{3}},
	}
	for _, tc := range cases {
		got := ids(tc.f)
		if len(got) != len(tc.want) {
			t.Errorf("%s: ids = %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: ids = %v, want %v", tc.name, got, tc.want)
				break
			}
		}
	}

	qs, _ := taskQuestions(db, TaskQuestionFilter{TaskID: 10, All: true})
	if q := qs[0]; q.TaskTitle != "alpha task" || q.Project != "alpha" ||
		q.Answer == nil || *q.Answer != "yes" || q.AnsweredBy == nil || *q.AnsweredBy != "user" {
		t.Errorf("EQ-1 = %+v", q)
	}
	if qs[1].Answer != nil {
		t.Errorf("open question carries an answer: %+v", qs[1])
	}
	if q := qs[3]; q.Reason == nil || *q.Reason != "moot" || q.Answer != nil {
		t.Errorf("EQ-6 = %+v, want reason moot and no answer", q)
	}
}
