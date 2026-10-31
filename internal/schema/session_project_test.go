package schema_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mikeschinkel/endless/internal/schema"
)

// TestMigrate30_SessionProjectFollowsTask pins E-2358's backfill: every
// session holding a task is re-stamped with that task's project; a session
// with no task keeps the project it was created with.
func TestMigrate30_SessionProjectFollowsTask(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "endless.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err = schema.MigrateToContext(context.Background(), db, 29); err != nil {
		t.Fatalf("migrate to 29: %v", err)
	}

	const ts = "2026-10-01T00:00:00"
	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	mustExec(`INSERT INTO projects (id, name, path, status, created_at, updated_at) VALUES
	          (1, 'endless', '/tmp/endless', 'active', ?, ?),
	          (2, 'go-cfgstore', '/tmp/go-cfgstore', 'active', ?, ?)`, ts, ts, ts, ts)
	mustExec(`INSERT INTO tasks (id, project_id, title, status, phase, created_at, updated_at) VALUES
	          (10, 2, 'theirs', 'underway', 'now', ?, ?),
	          (11, 1, 'ours', 'underway', 'now', ?, ?)`, ts, ts, ts, ts)
	// 100: started in endless, works go-cfgstore's task — the ES-1339 case.
	// 101: already at its task's project. 102: no task. 103: no project at all.
	mustExec(`INSERT INTO sessions (id, project_id, platform, state, task_id, started_at, last_activity) VALUES
	          (100, 1, 'claude', 'working', 10, ?, ?),
	          (101, 1, 'claude', 'working', 11, ?, ?),
	          (102, 1, 'claude', 'idle', NULL, ?, ?),
	          (103, NULL, 'claude', 'idle', 10, ?, ?)`, ts, ts, ts, ts, ts, ts, ts, ts)

	if err = schema.Migrate(db); err != nil {
		t.Fatalf("migrate to latest: %v", err)
	}

	want := map[int64]int64{100: 2, 101: 1, 102: 1, 103: 2}
	for sid, project := range want {
		var got sql.NullInt64
		if err := db.QueryRow(`SELECT project_id FROM sessions WHERE id = ?`, sid).Scan(&got); err != nil {
			t.Fatalf("read session %d: %v", sid, err)
		}
		if got.Int64 != project {
			t.Errorf("session %d: project_id = %d, want %d", sid, got.Int64, project)
		}
	}
}
