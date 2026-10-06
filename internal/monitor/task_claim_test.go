package monitor

import "testing"

// TestGetTaskClaim: a task's status and its newest bound session, with
// liveness read off the session's state; no session and no task each read as
// zero values rather than errors.
func TestGetTaskClaim(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/tmp/p")
	seedProjectStatusTask(t, db, 1, 1, "underway")
	seedProjectStatusTask(t, db, 2, 1, "confirmed")
	seedProjectStatusTask(t, db, 3, 1, "ready")
	one, two := int64(1), int64(2)
	seedProjectStatusSession(t, db, 10, 1, "working", &one)
	seedProjectStatusSession(t, db, 20, 1, "working", &two)
	seedProjectStatusSession(t, db, 21, 1, "ended", &two)

	for _, tc := range []struct {
		id     int64
		status string
		sess   int64
		live   bool
	}{
		{1, "underway", 10, true},
		{2, "confirmed", 21, false}, // the newest binding wins
		{3, "ready", 0, false},
		{99, "", 0, false},
	} {
		c, err := GetTaskClaim(tc.id)
		if err != nil {
			t.Fatalf("GetTaskClaim(%d): %v", tc.id, err)
		}
		if c.Status != tc.status || c.SessionID != tc.sess || c.SessionLive() != tc.live {
			t.Errorf("GetTaskClaim(%d) = %+v live=%v, want %s/%d/%v",
				tc.id, c, c.SessionLive(), tc.status, tc.sess, tc.live)
		}
	}
}
