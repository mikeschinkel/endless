package sessionquerycmd

import (
	"database/sql"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

// bgAgentSeed is one sessions row for the list-bg-agents tests. epicID/taskID
// are pointers so a row can leave active_epic_id / active_task_id NULL.
type bgAgentSeed struct {
	sessionID string
	shortID   string
	state     string
	kindID    int64
	epicID    *int64
	taskID    *int64
	startedAt string
}

func i64(v int64) *int64 { return &v }

// seedBgAgentsDB writes an endless.db with the schema applied, a project at
// projectPath, a fixed set of epic/child tasks, and the given session rows.
func seedBgAgentsDB(t *testing.T, cfgDir, projectPath string, rows []bgAgentSeed) {
	t.Helper()
	dbPath := filepath.Join(cfgDir, "endless.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO projects (id, name, path) VALUES (1, 'proj-seed', ?)",
		projectPath,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	// Tasks referenced by active_epic_id / active_task_id: two epics and a few
	// children, so the title join has something to return and FKs resolve.
	for _, ts := range []struct {
		id    int64
		title string
	}{
		{100, "Epic Alpha"}, {101, "Child One"}, {102, "Child Two"},
		{200, "Epic Beta"}, {201, "Child Beta-One"},
	} {
		if _, err := db.Exec(
			"INSERT INTO tasks (id, project_id, title, status) VALUES (?, 1, ?, 'ready')",
			ts.id, ts.title,
		); err != nil {
			t.Fatalf("seed task %d: %v", ts.id, err)
		}
	}
	for _, r := range rows {
		// short_id is UNIQUE; tmux rows leave it NULL (multiple NULLs are
		// allowed) rather than '' (which would collide across rows).
		var shortID any
		if r.shortID != "" {
			shortID = r.shortID
		}
		if _, err := db.Exec(
			`INSERT INTO sessions
			   (session_id, project_id, platform, state, active_task_id, active_epic_id,
			    kind_id, short_id, started_at, last_activity)
			 VALUES (?, 1, 'claude', ?, ?, ?, ?, ?, ?, ?)`,
			r.sessionID, r.state, r.taskID, r.epicID, r.kindID, shortID,
			r.startedAt, r.startedAt,
		); err != nil {
			t.Fatalf("seed session %s: %v", r.sessionID, err)
		}
	}
}

type bgAgentRow struct {
	ID        int64  `json:"id"`
	ShortID   string `json:"short_id"`
	TaskID    *int64 `json:"task_id"`
	Title     string `json:"title"`
	StartedAt string `json:"started_at"`
}

type bgAgentResult struct {
	Scope  string       `json:"scope"`
	EpicID *int64       `json:"epic_id"`
	Agents []bgAgentRow `json:"agents"`
}

// kind_id 2 = background, 1 = tmux (per schema session_kinds seed).
const (
	kindTmux = int64(1)
	kindBg   = int64(2)
)

// fixtureRows is the shared session mix: two working bg agents under epic 100,
// one under epic 200, an ended bg agent under 100, a tmux session under 100, a
// coordinator (tmux) session whose active_epic_id is 100, and a coordinator
// with NULL active_epic_id.
func fixtureRows() []bgAgentSeed {
	return []bgAgentSeed{
		{"bg-1", "aaa11111", "working", kindBg, i64(100), i64(101), "2026-06-23T10:00:00"},
		{"bg-2", "bbb22222", "working", kindBg, i64(100), i64(102), "2026-06-23T11:00:00"},
		{"bg-3", "ccc33333", "working", kindBg, i64(200), i64(201), "2026-06-23T12:00:00"},
		{"bg-ended", "ddd44444", "ended", kindBg, i64(100), i64(101), "2026-06-23T09:00:00"},
		{"tmux-under-epic", "", "working", kindTmux, i64(100), i64(101), "2026-06-23T08:00:00"},
		{"coord-epic", "", "working", kindTmux, i64(100), i64(100), "2026-06-23T07:00:00"},
		{"coord-noepic", "", "working", kindTmux, nil, i64(101), "2026-06-23T06:00:00"},
	}
}

func runBgAgents(t *testing.T, cfgDir string, args ...string) bgAgentResult {
	t.Helper()
	bin := endlessGoBin(t)
	full := append([]string{"--config-dir", cfgDir, "session-query", "list-bg-agents"}, args...)
	out, err := exec.Command(bin, full...).Output()
	if err != nil {
		t.Fatalf("binary exec failed (args %v): %v\nstdout: %s", args, err, out)
	}
	var got bgAgentResult
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode json: %v\nraw: %s", err, out)
	}
	return got
}

// TestListBgAgents_ByEpic: --epic-id filters to working bg agents under that
// epic, oldest-first, excluding ended/tmux rows and other epics.
func TestListBgAgents_ByEpic(t *testing.T) {
	cfgDir := t.TempDir()
	seedBgAgentsDB(t, cfgDir, t.TempDir(), fixtureRows())

	got := runBgAgents(t, cfgDir, "--epic-id", "100")
	if got.Scope != "epic" || got.EpicID == nil || *got.EpicID != 100 {
		t.Fatalf("scope/epic wrong: %+v", got)
	}
	if len(got.Agents) != 2 {
		t.Fatalf("got %d agents, want 2: %+v", len(got.Agents), got.Agents)
	}
	if got.Agents[0].ShortID != "aaa11111" || got.Agents[1].ShortID != "bbb22222" {
		t.Errorf("wrong order/agents: %+v", got.Agents)
	}
	if got.Agents[0].Title != "Child One" {
		t.Errorf("title join wrong: got %q, want %q", got.Agents[0].Title, "Child One")
	}
}

// TestListBgAgents_BySession: --session-id auto-resolves the caller's
// active_epic_id, then filters as --epic-id would.
func TestListBgAgents_BySession(t *testing.T) {
	cfgDir := t.TempDir()
	seedBgAgentsDB(t, cfgDir, t.TempDir(), fixtureRows())

	// coord-epic is the 6th seeded row → sessions.id 6 (insertion order).
	var coordID int64
	withSeedDB(t, cfgDir, func(db *sql.DB) {
		if err := db.QueryRow(
			"SELECT id FROM sessions WHERE session_id = 'coord-epic'",
		).Scan(&coordID); err != nil {
			t.Fatalf("lookup coord session: %v", err)
		}
	})

	got := runBgAgents(t, cfgDir, "--session-id", strconv.FormatInt(coordID, 10))
	if got.Scope != "epic" || got.EpicID == nil || *got.EpicID != 100 {
		t.Fatalf("scope/epic wrong: %+v", got)
	}
	if len(got.Agents) != 2 {
		t.Fatalf("got %d agents, want 2: %+v", len(got.Agents), got.Agents)
	}
}

// TestListBgAgents_BySession_NoEpic: a caller whose active_epic_id is NULL
// yields epic_id null and an empty list (Python renders the guidance error).
func TestListBgAgents_BySession_NoEpic(t *testing.T) {
	cfgDir := t.TempDir()
	seedBgAgentsDB(t, cfgDir, t.TempDir(), fixtureRows())

	var coordID int64
	withSeedDB(t, cfgDir, func(db *sql.DB) {
		if err := db.QueryRow(
			"SELECT id FROM sessions WHERE session_id = 'coord-noepic'",
		).Scan(&coordID); err != nil {
			t.Fatalf("lookup coord session: %v", err)
		}
	})

	got := runBgAgents(t, cfgDir, "--session-id", strconv.FormatInt(coordID, 10))
	if got.Scope != "epic" || got.EpicID != nil {
		t.Fatalf("want scope=epic epic_id=null, got %+v", got)
	}
	if len(got.Agents) != 0 {
		t.Errorf("want 0 agents for null epic, got %d", len(got.Agents))
	}
}

// TestListBgAgents_All: --all drops the epic filter but stays project-scoped —
// every working bg agent in the project (excludes ended + tmux), all 3 epics.
func TestListBgAgents_All(t *testing.T) {
	cfgDir := t.TempDir()
	projectPath := t.TempDir()
	seedBgAgentsDB(t, cfgDir, projectPath, fixtureRows())

	got := runBgAgents(t, cfgDir, "--all", "--project-root", projectPath)
	if got.Scope != "all" || got.EpicID != nil {
		t.Fatalf("want scope=all epic_id=null, got %+v", got)
	}
	if len(got.Agents) != 3 {
		t.Fatalf("got %d agents, want 3 (bg-1,bg-2,bg-3): %+v", len(got.Agents), got.Agents)
	}
}

// TestListBgAgents_RequiresExactlyOneScope: zero or multiple scope selectors
// is a usage error (non-zero exit).
func TestListBgAgents_RequiresExactlyOneScope(t *testing.T) {
	cfgDir := t.TempDir()
	seedBgAgentsDB(t, cfgDir, t.TempDir(), nil)
	bin := endlessGoBin(t)

	for _, args := range [][]string{
		{}, // none
		{"--epic-id", "100", "--all", "--project-root", "/tmp/x"}, // two
	} {
		full := append([]string{"--config-dir", cfgDir, "session-query", "list-bg-agents"}, args...)
		if out, err := exec.Command(bin, full...).CombinedOutput(); err == nil {
			t.Errorf("expected non-zero exit for args %v, got success\nout: %s", args, out)
		}
	}
}

// withSeedDB opens the seeded endless.db read-side for assertions.
func withSeedDB(t *testing.T, cfgDir string, fn func(*sql.DB)) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(cfgDir, "endless.db"))
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	defer db.Close()
	fn(db)
}
