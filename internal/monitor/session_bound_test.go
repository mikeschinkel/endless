package monitor

import (
	"database/sql"
	"testing"

	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
)

// relationFor returns the session_tasks relation the session with GUID guid
// holds for taskID, or Relation(0) when there is no row.
func relationFor(t *testing.T, db *sql.DB, guid string, taskID int64) sessiontaskrelation.Relation {
	t.Helper()
	var rel sql.NullInt64
	err := db.QueryRow(
		`SELECT st.relation_id FROM session_tasks st
		   JOIN sessions s ON s.id = st.session_id
		  WHERE s.session_id = ? AND st.task_id = ?`,
		guid, taskID,
	).Scan(&rel)
	if err == sql.ErrNoRows {
		return 0
	}
	if err != nil {
		t.Fatalf("read relation for %s/%d: %v", guid, taskID, err)
	}
	return sessiontaskrelation.Relation(rel.Int64)
}

// TestBindSessionToTask_RecordsOwnershipRelation is P11 of E-2291: the
// SessionStart cwd auto-bind writes the session's ownership row itself, in the
// same transaction as its sessions upsert — `claimed` for an underway task
// (spawn claimed it for this session) and `bound` for a pre-work one (a primed
// session reading the task in). Before E-2291 it wrote no row at all.
func TestBindSessionToTask_RecordsOwnershipRelation(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   sessiontaskrelation.Relation
	}{
		{"underway", sessiontaskrelation.RelationClaimed},
		{"unplanned", sessiontaskrelation.RelationBound},
		{"submitted", sessiontaskrelation.RelationBound},
		{"ready", sessiontaskrelation.RelationBound},
		{"revisit", sessiontaskrelation.RelationBound},
	} {
		t.Run(tc.status, func(t *testing.T) {
			db := withTestDB(t)
			seedProject(t, db, 1, "p", "/tmp/p")
			seedTask(t, db, 42, 1, "task", tc.status)
			t.Setenv("TMUX_PANE", "")

			if err := TouchSession("uuid-1", "claude", "", 1); err != nil {
				t.Fatalf("touch: %v", err)
			}
			if err := BindSessionToTask("uuid-1", 1, 42); err != nil {
				t.Fatalf("bind: %v", err)
			}
			if got := relationFor(t, db, "uuid-1", 42); got != tc.want {
				t.Errorf("relation = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestBindSessionToTask_LeavesStrongerRowAlone pins that the auto-bind writes
// through the ladder: a resume of a session that already claimed its task (now
// back at a pre-work status, say) keeps `claimed`.
func TestBindSessionToTask_LeavesStrongerRowAlone(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedTask(t, db, 42, 1, "task", "underway")
	t.Setenv("TMUX_PANE", "")

	if err := TouchSession("uuid-1", "claude", "", 1); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if err := BindSessionToTask("uuid-1", 1, 42); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if _, err := db.Exec(`UPDATE tasks SET status = 'ready' WHERE id = 42`); err != nil {
		t.Fatalf("reset status: %v", err)
	}
	if err := BindSessionToTask("uuid-1", 1, 42); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if got := relationFor(t, db, "uuid-1", 42); got != sessiontaskrelation.RelationClaimed {
		t.Errorf("relation = %s, want claimed (a bind must not demote a claim)", got)
	}
}

// TestSessionStatus_BoundTaskIsAnchorOnly is P12 of E-2291: claimed and bound
// are the anchor relations, so a session's bound task is its anchor row —
// listed once in the focal view, and never in the no-goal view as a second row.
func TestSessionStatus_BoundTaskIsAnchorOnly(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	const bound, touched = 2291, 2405
	snTask(t, db, bound, 1, "unplanned", "now", "")
	snTask(t, db, touched, 1, "ready", "now", "")
	snSession(t, db, 970, 1, bound, "working")
	snSessionTaskRel(t, db, 970, bound, int64(sessiontaskrelation.RelationBound))
	snSessionTaskRel(t, db, 970, touched, int64(sessiontaskrelation.RelationTouched))

	rows, err := SessionStatusRows(bound, 0, false)
	if err != nil {
		t.Fatalf("SessionStatusRows: %v", err)
	}
	n := 0
	for _, r := range rows {
		if r.ID == bound {
			n++
			if !r.IsFocal {
				t.Errorf("bound task row should be the focal anchor: %+v", r)
			}
		}
	}
	if n != 1 {
		t.Errorf("bound task appears %d times in the focal view, want 1", n)
	}

	noGoal, err := SessionStatusRowsForSession(970, false)
	if err != nil {
		t.Fatalf("SessionStatusRowsForSession: %v", err)
	}
	if _, ok := snRowByID(noGoal, bound); ok {
		t.Errorf("bound task %d must not appear in the no-goal view", bound)
	}
	if _, ok := snRowByID(noGoal, touched); !ok {
		t.Errorf("touched task %d should appear in the no-goal view", touched)
	}
}
