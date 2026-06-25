package events

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/mikeschinkel/endless/internal/schema"
	_ "modernc.org/sqlite"
)

func newLandingTestDB(t *testing.T) *sql.DB {
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
	// session_tasks (V9) lives outside schema.SQL; create it so the
	// executor's session-touch upsert succeeds when actor has a session.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS session_tasks (
		session_id INTEGER NOT NULL,
		task_id    INTEGER NOT NULL,
		created_at TEXT    NOT NULL,
		updated_at TEXT    NOT NULL,
		UNIQUE(session_id, task_id)
	)`); err != nil {
		t.Fatalf("create session_tasks: %v", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("set fks: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, path, status, created_at, updated_at)
		 VALUES (1, 'test', '/tmp/test', 'active', '2026-05-16T00:00:00', '2026-05-16T00:00:00')`,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, phase, status, type_id)
		 VALUES (1337, 1, 'probe', 'now', 'underway', 1)`,
	); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	return db
}

func landedEvent(t *testing.T, taskID int64, sessionID string) *Event {
	t.Helper()
	payload, err := json.Marshal(TaskLandedPayload{
		Branch:         "task/1337-stop-deleting-worktrees",
		MergeCommitSHA: "deadbeef",
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       1,
		TS:      "2026-05-20T12:00:00",
		Kind:    KindTaskLanded,
		Project: "test",
		Entity:  EntityRef{Type: EntityTask, ID: strconv.FormatInt(taskID, 10)},
		Actor: Actor{
			Kind:      ActorCLI,
			ID:        "user@host",
			SessionID: sessionID,
		},
		Payload: payload,
	}
}

func TestExecTaskLanded_InsertsRow(t *testing.T) {
	db := newLandingTestDB(t)
	evt := landedEvent(t, 1337, "")

	if _, err := execTaskLanded(db, evt); err != nil {
		t.Fatalf("execTaskLanded: %v", err)
	}

	var (
		taskID                       int64
		sessionID                    sql.NullInt64
		branch, sha, landedAt        string
	)
	err := db.QueryRow(
		`SELECT task_id, session_id, branch, merge_commit_sha, landed_at
		 FROM task_landings WHERE task_id = ?`,
		1337,
	).Scan(&taskID, &sessionID, &branch, &sha, &landedAt)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if taskID != 1337 {
		t.Errorf("task_id: got %d, want 1337", taskID)
	}
	if sessionID.Valid {
		t.Errorf("expected NULL session_id when actor has empty SessionID, got %d", sessionID.Int64)
	}
	if branch != "task/1337-stop-deleting-worktrees" {
		t.Errorf("branch: got %q", branch)
	}
	if sha != "deadbeef" {
		t.Errorf("merge_commit_sha: got %q", sha)
	}
	if landedAt == "" {
		t.Errorf("landed_at empty")
	}
}

func TestExecTaskLanded_PreservesSessionID(t *testing.T) {
	db := newLandingTestDB(t)
	// Seed the session row so the FK on task_landings.session_id holds.
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, started_at)
		 VALUES (42, 'sess-42', 1, '2026-05-20T11:00:00')`,
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	evt := landedEvent(t, 1337, "42")
	if _, err := execTaskLanded(db, evt); err != nil {
		t.Fatalf("execTaskLanded: %v", err)
	}

	var sessionID sql.NullInt64
	if err := db.QueryRow(
		"SELECT session_id FROM task_landings WHERE task_id = ?",
		1337,
	).Scan(&sessionID); err != nil {
		t.Fatalf("query: %v", err)
	}
	if !sessionID.Valid {
		t.Errorf("expected non-NULL session_id when actor.session_id is set")
	}
	if sessionID.Int64 != 42 {
		t.Errorf("session_id: got %d, want 42", sessionID.Int64)
	}
}

func TestExecTaskLanded_AppendsOnReland(t *testing.T) {
	db := newLandingTestDB(t)

	// First land
	first := landedEvent(t, 1337, "")
	if _, err := execTaskLanded(db, first); err != nil {
		t.Fatalf("first execTaskLanded: %v", err)
	}

	// Second land (re-land after a follow-up commit)
	second := landedEvent(t, 1337, "")
	secondPayload, _ := json.Marshal(TaskLandedPayload{
		Branch:         "task/1337-stop-deleting-worktrees",
		MergeCommitSHA: "feedface",
	})
	second.Payload = secondPayload
	second.TS = "2026-05-20T13:00:00"
	if _, err := execTaskLanded(db, second); err != nil {
		t.Fatalf("second execTaskLanded: %v", err)
	}

	var count int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM task_landings WHERE task_id = ?",
		1337,
	).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 landing rows after re-land, got %d", count)
	}
}

func TestTaskLandedKind_IsValid(t *testing.T) {
	if !ValidKinds[KindTaskLanded] {
		t.Fatalf("KindTaskLanded missing from ValidKinds map")
	}
}
