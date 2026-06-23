// Tests for active_epic_id population on interactive task claim/release
// (E-1624). execTaskClaimed must set sessions.active_epic_id to the nearest
// type='epic' ancestor of the claimed task (the task itself if it is an epic,
// NULL if none), and execTaskReleased must clear it alongside active_task_id.
package events

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
	"github.com/mikeschinkel/endless/internal/tasktype"
)

// newClaimTestDB stands up a schema-applied SQLite DB with a seeded project.
func newClaimTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, path, status, created_at, updated_at)
		 VALUES (1, 'test', '/tmp/test', 'active', '2026-06-23T00:00:00', '2026-06-23T00:00:00')`,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return db
}

// seedClaimSession inserts one live session row (sessions.id == id).
func seedClaimSession(t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, state, started_at)
		 VALUES (?, ?, 1, 'working', '2026-06-23T00:00:00')`,
		id, "sess-"+strconv.FormatInt(id, 10),
	); err != nil {
		t.Fatalf("seed session %d: %v", id, err)
	}
}

// claimEvent builds a task.claimed event for taskID by sessionID (sessions.id).
func claimEvent(t *testing.T, taskID, sessionID int64) *Event {
	t.Helper()
	payload, err := json.Marshal(TaskClaimedPayload{SessionID: sessionID})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       1,
		TS:      "2026-06-23T00:00:00",
		Kind:    KindTaskClaimed,
		Project: "test",
		Entity:  EntityRef{Type: EntityTask, ID: strconv.FormatInt(taskID, 10)},
		Payload: payload,
	}
}

// releaseEvent builds a task.released event for taskID by sessionID.
func releaseEvent(t *testing.T, taskID, sessionID int64) *Event {
	t.Helper()
	payload, err := json.Marshal(TaskReleasedPayload{SessionID: sessionID})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       1,
		TS:      "2026-06-23T00:00:01",
		Kind:    KindTaskReleased,
		Project: "test",
		Entity:  EntityRef{Type: EntityTask, ID: strconv.FormatInt(taskID, 10)},
		Payload: payload,
	}
}

// sessionActive reads a session's active_task_id and active_epic_id.
func sessionActive(t *testing.T, db *sql.DB, sessionID int64) (taskID, epicID sql.NullInt64) {
	t.Helper()
	if err := db.QueryRow(
		"SELECT active_task_id, active_epic_id FROM sessions WHERE id = ?", sessionID,
	).Scan(&taskID, &epicID); err != nil {
		t.Fatalf("read session %d: %v", sessionID, err)
	}
	return taskID, epicID
}

// TestClaim_ChildOfEpicSetsEpicID: claiming a child of an epic sets
// active_epic_id to the epic and active_task_id to the child.
func TestClaim_ChildOfEpicSetsEpicID(t *testing.T) {
	db := newClaimTestDB(t)
	seedClaimSession(t, db, 42)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "in_progress")
	seedTask(t, db, 100, ptr(1), int(tasktype.TaskTypeTask), "ready")

	if _, err := execTaskClaimed(db, claimEvent(t, 100, 42)); err != nil {
		t.Fatalf("execTaskClaimed: %v", err)
	}

	taskID, epicID := sessionActive(t, db, 42)
	if !taskID.Valid || taskID.Int64 != 100 {
		t.Errorf("active_task_id = %v, want 100", taskID)
	}
	if !epicID.Valid || epicID.Int64 != 1 {
		t.Errorf("active_epic_id = %v, want 1 (epic ancestor)", epicID)
	}
}

// TestClaim_NestedChildResolvesNearestEpic: a grand-child under epic(1) ->
// epic(2) -> task(100) resolves to the NEAREST epic (2).
func TestClaim_NestedChildResolvesNearestEpic(t *testing.T) {
	db := newClaimTestDB(t)
	seedClaimSession(t, db, 42)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "in_progress")
	seedTask(t, db, 2, ptr(1), int(tasktype.TaskTypeEpic), "in_progress")
	seedTask(t, db, 100, ptr(2), int(tasktype.TaskTypeTask), "ready")

	if _, err := execTaskClaimed(db, claimEvent(t, 100, 42)); err != nil {
		t.Fatalf("execTaskClaimed: %v", err)
	}

	_, epicID := sessionActive(t, db, 42)
	if !epicID.Valid || epicID.Int64 != 2 {
		t.Errorf("active_epic_id = %v, want 2 (nearest epic ancestor)", epicID)
	}
}

// TestClaim_StandaloneTaskClearsEpicID: claiming a task with no epic ancestor
// leaves active_epic_id NULL.
func TestClaim_StandaloneTaskNullEpicID(t *testing.T) {
	db := newClaimTestDB(t)
	seedClaimSession(t, db, 42)
	seedTask(t, db, 100, nil, int(tasktype.TaskTypeTask), "ready")

	if _, err := execTaskClaimed(db, claimEvent(t, 100, 42)); err != nil {
		t.Fatalf("execTaskClaimed: %v", err)
	}

	taskID, epicID := sessionActive(t, db, 42)
	if !taskID.Valid || taskID.Int64 != 100 {
		t.Errorf("active_task_id = %v, want 100", taskID)
	}
	if epicID.Valid {
		t.Errorf("active_epic_id = %v, want NULL (no epic ancestor)", epicID)
	}
}

// TestClaim_EpicDirectlyResolvesToSelf: claiming an epic directly sets
// active_epic_id to the epic's own id (depth-0 inclusion).
func TestClaim_EpicDirectlyResolvesToSelf(t *testing.T) {
	db := newClaimTestDB(t)
	seedClaimSession(t, db, 42)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "in_progress")

	if _, err := execTaskClaimed(db, claimEvent(t, 1, 42)); err != nil {
		t.Fatalf("execTaskClaimed: %v", err)
	}

	taskID, epicID := sessionActive(t, db, 42)
	if !taskID.Valid || taskID.Int64 != 1 {
		t.Errorf("active_task_id = %v, want 1", taskID)
	}
	if !epicID.Valid || epicID.Int64 != 1 {
		t.Errorf("active_epic_id = %v, want 1 (epic resolves to self)", epicID)
	}
}

// TestClaim_ClearsStaleEpicFromPriorClaim: a second claim of a standalone task
// clears the active_epic_id left by a prior epic-descendant claim.
func TestClaim_ClearsStaleEpicFromPriorClaim(t *testing.T) {
	db := newClaimTestDB(t)
	seedClaimSession(t, db, 42)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "in_progress")
	seedTask(t, db, 100, ptr(1), int(tasktype.TaskTypeTask), "ready")
	seedTask(t, db, 200, nil, int(tasktype.TaskTypeTask), "ready")

	if _, err := execTaskClaimed(db, claimEvent(t, 100, 42)); err != nil {
		t.Fatalf("claim epic child: %v", err)
	}
	if _, epicID := sessionActive(t, db, 42); !epicID.Valid || epicID.Int64 != 1 {
		t.Fatalf("setup: active_epic_id = %v, want 1", epicID)
	}

	// Claim a standalone task — the stale epic id must be cleared.
	if _, err := execTaskClaimed(db, claimEvent(t, 200, 42)); err != nil {
		t.Fatalf("claim standalone: %v", err)
	}
	taskID, epicID := sessionActive(t, db, 42)
	if !taskID.Valid || taskID.Int64 != 200 {
		t.Errorf("active_task_id = %v, want 200", taskID)
	}
	if epicID.Valid {
		t.Errorf("active_epic_id = %v, want NULL after re-claim of standalone", epicID)
	}
}

// TestRelease_ClearsBothTaskAndEpic: releasing an epic-descendant claim NULLs
// both active_task_id and active_epic_id.
func TestRelease_ClearsBothTaskAndEpic(t *testing.T) {
	db := newClaimTestDB(t)
	seedClaimSession(t, db, 42)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "in_progress")
	seedTask(t, db, 100, ptr(1), int(tasktype.TaskTypeTask), "ready")

	if _, err := execTaskClaimed(db, claimEvent(t, 100, 42)); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := execTaskReleased(db, releaseEvent(t, 100, 42)); err != nil {
		t.Fatalf("release: %v", err)
	}

	taskID, epicID := sessionActive(t, db, 42)
	if taskID.Valid {
		t.Errorf("active_task_id = %v, want NULL after release", taskID)
	}
	if epicID.Valid {
		t.Errorf("active_epic_id = %v, want NULL after release", epicID)
	}
}
