package monitor

import (
	"database/sql"
	"testing"
)

// seedUnansweredQuestions gives task 101 an open question, task 102 only an
// answered and a withdrawn one, and task 103 none: only the first is waiting on
// the user (E-2280).
func seedUnansweredQuestions(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO task_questions
	    (id, task_id, series, question, status, answer, answered_by, reason) VALUES
	    (1, 101, 1, 'open', 'open', NULL, NULL, NULL),
	    (2, 102, 1, 'answered', 'answered', 'yes', 'user', NULL),
	    (3, 102, 2, 'withdrawn', 'withdrawn', NULL, NULL, 'moot')`); err != nil {
		t.Fatalf("seed questions: %v", err)
	}
}

func TestSessionStatusRows_Unanswered(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	const focal = 100
	snTask(t, db, focal, 1, "underway", "now", "")
	for _, id := range []int64{101, 102, 103} {
		snTask(t, db, id, 1, "submitted", "now", "")
	}
	snSession(t, db, 1, 1, focal, "working")
	for _, id := range []int64{focal, 101, 102, 103} {
		snSessionTask(t, db, 1, id)
	}
	seedUnansweredQuestions(t, db)

	rows, err := SessionStatusRows(focal, 0, true)
	if err != nil {
		t.Fatalf("SessionStatusRows: %v", err)
	}
	for id, want := range map[int64]bool{101: true, 102: false, 103: false} {
		r, ok := snRowByID(rows, id)
		if !ok {
			t.Fatalf("row %d missing", id)
		}
		if r.Unanswered != want {
			t.Errorf("task %d: Unanswered = %v, want %v", id, r.Unanswered, want)
		}
	}
}

func TestProjectStatusRows_Unanswered(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	for _, id := range []int64{101, 102, 103} {
		seedProjectStatusTask(t, db, id, 1, "ready")
	}
	seedUnansweredQuestions(t, db)

	by := map[int64]ProjectStatusRow{}
	for _, r := range projectStatusRows(t, 1, nowNext) {
		by[r.TaskID] = r
	}
	for id, want := range map[int64]bool{101: true, 102: false, 103: false} {
		if by[id].Unanswered != want {
			t.Errorf("task %d: Unanswered = %v, want %v", id, by[id].Unanswered, want)
		}
	}
}
