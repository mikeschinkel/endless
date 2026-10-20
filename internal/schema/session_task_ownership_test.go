package schema_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mikeschinkel/endless/internal/schema"
	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
)

// TestMigrate24_BackfillsOwnershipRows pins E-2291's backfill: every session
// holding a sessions.task_id gets a row for that task — `claimed` once the task
// has reached underway or later, `bound` while it is pre-work — upgrading a
// weaker row and leaving a stronger one alone.
func TestMigrate24_BackfillsOwnershipRows(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "endless.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err = schema.MigrateToContext(context.Background(), db, 23); err != nil {
		t.Fatalf("migrate to 23: %v", err)
	}

	const ts = "2026-10-01T00:00:00"
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// A behind database is not reseeded (MigrateToContext), so put back the
	// relation rows a version-23 database held, under their pre-E-2291 names.
	mustExec(`INSERT INTO session_task_relations (id, slug, label) VALUES
	          (1, 'claimed', 'Claimed'), (2, 'surfaced', 'Surfaced'), (3, 'revisited', 'Revisited'),
	          (4, 'referenced', 'Referenced'), (5, 'queued', 'Queued')`)
	mustExec(`INSERT INTO projects (id, name, path, status, created_at, updated_at)
	          VALUES (1, 'p', '/tmp/p', 'active', ?, ?)`, ts, ts)
	for id, status := range map[int64]string{
		10: "underway", 11: "confirmed", 12: "unplanned", 13: "ready",
		14: "submitted", 15: "underway", 16: "ready", 17: "unverified",
	} {
		mustExec(`INSERT INTO tasks (id, project_id, title, status, phase, created_at, updated_at)
		          VALUES (?, 1, 't', ?, 'now', ?, ?)`, id, status, ts, ts)
	}
	// session id → its task. 107 holds no task and must get no row.
	for sid, task := range map[int64]any{
		100: 10, 101: 11, 102: 12, 103: 13, 104: 14, 105: 15, 106: 16, 107: nil, 108: 17,
	} {
		mustExec(`INSERT INTO sessions (id, project_id, platform, state, task_id, started_at, last_activity)
		          VALUES (?, 1, 'claude', 'idle', ?, ?, ?)`, sid, task, ts, ts)
	}
	// Pre-existing rows: a weaker one to upgrade on each side of the status
	// rule, and a stronger one (a claim on a task now back at ready) to keep.
	mustExec(`INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at)
	          VALUES (101, 11, 3, ?, ?), (103, 13, 2, ?, ?), (106, 16, 1, ?, ?), (108, 17, NULL, ?, ?)`,
		ts, ts, ts, ts, ts, ts, ts, ts)

	if err = schema.Migrate(db); err != nil {
		t.Fatalf("migrate to latest: %v", err)
	}

	want := map[int64]sessiontaskrelation.Relation{
		100: sessiontaskrelation.RelationClaimed, // underway
		101: sessiontaskrelation.RelationClaimed, // confirmed, revisited upgraded
		102: sessiontaskrelation.RelationBound,   // unplanned
		103: sessiontaskrelation.RelationBound,   // ready, filed upgraded
		104: sessiontaskrelation.RelationBound,   // submitted
		105: sessiontaskrelation.RelationClaimed, // underway
		106: sessiontaskrelation.RelationClaimed, // ready, but already claimed
		108: sessiontaskrelation.RelationClaimed, // unverified, NULL filled in
	}
	rows, err := db.Query(`SELECT st.session_id, st.relation_id FROM session_tasks st
	                         JOIN sessions s ON s.id = st.session_id AND s.task_id = st.task_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[int64]sessiontaskrelation.Relation{}
	for rows.Next() {
		var sid, rel int64
		if err := rows.Scan(&sid, &rel); err != nil {
			t.Fatal(err)
		}
		got[sid] = sessiontaskrelation.Relation(rel)
	}
	if len(got) != len(want) {
		t.Errorf("ownership rows = %v, want %v", got, want)
	}
	for sid, rel := range want {
		if got[sid] != rel {
			t.Errorf("session %d: relation = %s, want %s", sid, got[sid], rel)
		}
	}
	if err := sessiontaskrelation.VerifyIntegrity(db); err != nil {
		t.Errorf("VerifyIntegrity after migrate: %v", err)
	}
}
