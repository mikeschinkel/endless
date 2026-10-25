package triagejob

import (
	"database/sql"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
)

// TestRealEffectsSession_KeepsPaneUnderServerAlias (E-2428): a session bound
// under an earlier id of the running tmux server keeps its pane, so triage can
// message it, once that id is recorded as an alias; without the alias the pane
// names another server and is dropped.
func TestRealEffectsSession_KeepsPaneUnderServerAlias(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err = schema.Migrate(db); err != nil {
		t.Fatalf("schema: %v", err)
	}
	t.Cleanup(monitor.SetTestDB(db))
	t.Cleanup(monitor.SetTestTmuxObservation("0ae20eaa", map[string]string{"%22": "2.1.220"}))

	pid, err := monitor.SeedPaneProcess(db, "a6d29860", "%22")
	if err != nil {
		t.Fatalf("seed pane: %v", err)
	}
	res, err := db.Exec(
		`INSERT INTO sessions (session_id, platform, state, process_id, last_activity)
		 VALUES ('sess-restamped', 'claude', 'idle', ?, '2026-10-24T00:00:00')`, pid,
	)
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	id, _ := res.LastInsertId()
	fx := realEffects{db: db}

	s, err := fx.session(id)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if s.pane != "" {
		t.Errorf("without an alias, pane = %q, want dropped", s.pane)
	}

	if _, err = db.Exec(
		`INSERT INTO tmux_server_aliases (alias_uuid, server_uuid, recorded_at)
		 VALUES ('a6d29860', '0ae20eaa', '2026-10-24T00:00:00')`,
	); err != nil {
		t.Fatalf("seed alias: %v", err)
	}
	if err = monitor.RefreshLiveness(); err != nil {
		t.Fatalf("RefreshLiveness: %v", err)
	}
	s, err = fx.session(id)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if s.pane != "%22" || !s.live {
		t.Errorf("under an alias: pane %q, live %v; want %%22, live", s.pane, s.live)
	}
}
