package monitor

import (
	"database/sql"
	"testing"
)

// seedTypedTask inserts a task with an explicit type_id (1=task..4=epic) and an
// optional parent, so the epic-ancestor walk can be exercised.
func seedTypedTask(t *testing.T, db *sql.DB, id, projectID int64, typeID int64, parentID *int64) {
	t.Helper()
	if _, err := db.Exec(
		"INSERT INTO tasks (id, project_id, parent_id, title, status, type_id) VALUES (?, ?, ?, ?, 'underway', ?)",
		id, projectID, parentID, "t", typeID,
	); err != nil {
		t.Fatalf("seed typed task id=%d: %v", id, err)
	}
}

func bgRow(t *testing.T, db *sql.DB, id int64) (sessionID sql.NullString, shortID sql.NullString, kindID int64, activeTask sql.NullInt64, activeEpic sql.NullInt64) {
	t.Helper()
	err := db.QueryRow(
		"SELECT session_id, short_id, kind_id, active_task_id, active_epic_id FROM sessions WHERE id=?", id,
	).Scan(&sessionID, &shortID, &kindID, &activeTask, &activeEpic)
	if err != nil {
		t.Fatalf("read session id=%d: %v", id, err)
	}
	return
}

func TestRecordBgAgentSession_NoEpicAncestor(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/p")
	seedTask(t, db, 10, 1, "standalone", "underway") // default type (task)

	id, err := RecordBgAgentSession(10, "abc123")
	if err != nil {
		t.Fatalf("RecordBgAgentSession: %v", err)
	}

	sess, short, kind, task, epic := bgRow(t, db, id)
	if sess.Valid {
		t.Errorf("session_id = %q, want NULL", sess.String)
	}
	if short.String != "abc123" {
		t.Errorf("short_id = %q, want abc123", short.String)
	}
	if kind != 2 {
		t.Errorf("kind_id = %d, want 2 (background)", kind)
	}
	if task.Int64 != 10 {
		t.Errorf("active_task_id = %d, want 10", task.Int64)
	}
	if epic.Valid {
		t.Errorf("active_epic_id = %d, want NULL (no epic ancestor)", epic.Int64)
	}
}

func TestRecordBgAgentSession_ResolvesEpicAncestor(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/p")
	// epic 100 → child 110 → grandchild 120
	seedTypedTask(t, db, 100, 1, 4, nil) // epic
	p100 := int64(100)
	seedTypedTask(t, db, 110, 1, 1, &p100) // task under epic
	p110 := int64(110)
	seedTypedTask(t, db, 120, 1, 2, &p110) // bug under task under epic

	id, err := RecordBgAgentSession(120, "deep")
	if err != nil {
		t.Fatalf("RecordBgAgentSession: %v", err)
	}
	_, _, _, _, epic := bgRow(t, db, id)
	if !epic.Valid || epic.Int64 != 100 {
		t.Errorf("active_epic_id = %v, want 100 (nearest epic ancestor)", epic)
	}
}

func TestRecordBgAgentSession_DispatchEpicItself(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/p")
	seedTypedTask(t, db, 200, 1, 4, nil) // epic dispatched directly

	id, err := RecordBgAgentSession(200, "self")
	if err != nil {
		t.Fatalf("RecordBgAgentSession: %v", err)
	}
	_, _, _, task, epic := bgRow(t, db, id)
	if task.Int64 != 200 {
		t.Errorf("active_task_id = %d, want 200", task.Int64)
	}
	if !epic.Valid || epic.Int64 != 200 {
		t.Errorf("active_epic_id = %v, want 200 (the epic is its own nearest epic ancestor)", epic)
	}
}

func TestDecorateBgSession(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p", "/p")
	seedTask(t, db, 10, 1, "t", "underway")
	id, err := RecordBgAgentSession(10, "handle1")
	if err != nil {
		t.Fatalf("RecordBgAgentSession: %v", err)
	}

	// First decoration attaches the UUID.
	rows, err := DecorateBgSession("handle1", "uuid-real")
	if err != nil {
		t.Fatalf("DecorateBgSession: %v", err)
	}
	if rows != 1 {
		t.Fatalf("rows affected = %d, want 1", rows)
	}
	sess, _, _, _, _ := bgRow(t, db, id)
	if sess.String != "uuid-real" {
		t.Errorf("session_id = %q, want uuid-real", sess.String)
	}

	// Re-decoration is a no-op (row already has a session_id).
	rows, err = DecorateBgSession("handle1", "uuid-other")
	if err != nil {
		t.Fatalf("DecorateBgSession (2nd): %v", err)
	}
	if rows != 0 {
		t.Errorf("rows affected on re-decorate = %d, want 0", rows)
	}

	// Unknown short id matches nothing.
	rows, err = DecorateBgSession("nope", "uuid-x")
	if err != nil {
		t.Fatalf("DecorateBgSession (unknown): %v", err)
	}
	if rows != 0 {
		t.Errorf("rows affected for unknown short_id = %d, want 0", rows)
	}
}
