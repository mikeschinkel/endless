package sessionquerycmd

import (
	"database/sql"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
)

// seedLiveSessionsDB writes an endless.db at $cfgDir/endless.db with
// schema applied, one project at the given path, and a configurable mix
// of sessions for that project. Used by the list-live binary tests.
func seedLiveSessionsDB(t *testing.T, cfgDir, projectPath string, sessions []sessionSeed) {
	t.Helper()
	if err := monitor.FreeTestTempTree(cfgDir); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(cfgDir, "endless.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	defer db.Close()
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := db.Exec(
		"INSERT INTO projects (id, name, path) VALUES (1, 'proj-seed', ?)",
		projectPath,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	for _, s := range sessions {
		if _, err := db.Exec(
			`INSERT INTO sessions (session_id, project_id, platform, state, process_id, last_activity)
		 VALUES (?, 1, 'claude', ?, ?, '2026-05-20T00:00:00')`,
			s.sessionID, s.state, seedPane(t, db, s.process)); err != nil {
			t.Fatalf("seed session %s: %v", s.sessionID, err)
		}
	}
}

type sessionSeed struct {
	sessionID, state, process string
}

// TestListLive_BinaryReturnsLiveSessionsForProject pins the happy path:
// given a registered project and a mix of live + ended sessions, the
// list-live verb returns the live ones as a JSON array.
func TestListLive_BinaryReturnsLiveSessionsForProject(t *testing.T) {
	cfgDir := t.TempDir()
	// projectPath must exist and be absolute — list-live uses
	// monitor.ProjectIDForPath which walks up looking for a registered
	// project; a TempDir guarantees a path that's never coincidentally
	// matched by another row.
	projectPath := t.TempDir()
	seedLiveSessionsDB(t, cfgDir, projectPath, []sessionSeed{
		{"live-A", "working", "%5"},
		{"live-B", "idle", "%6"},
		{"dead-C", "ended", "%7"},
	})

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--db-dir", cfgDir,
		"session-query", "list-live", "--project-root", projectPath)
	// Use Output (stdout only); ProjectIDForPath's auto-register path
	// emits an info log to stderr that would otherwise corrupt the JSON
	// decode.
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("binary exec failed: %v\nstdout: %s", err, out)
	}
	// E-1668 wraps array payloads as {"_answered_from": …, "rows": [...]} so a
	// result with NO rows can still name the database it came from.
	var got struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode json output: %v\nraw: %s", err, out)
	}
	if len(got.Rows) != 2 {
		t.Errorf("got %d live sessions, want 2; raw: %s", len(got.Rows), out)
	}
}

// TestListLive_BinaryUnregisteredRootReturnsEmptyArray pins the
// "unknown project path" contract: documented behavior is empty JSON
// array (not error), so Python callers can treat "no project" and "no
// live sessions" uniformly.
func TestListLive_BinaryUnregisteredRootReturnsEmptyArray(t *testing.T) {
	cfgDir := t.TempDir()
	// Seed a project at one path but query for a different one.
	registered := t.TempDir()
	unregistered := t.TempDir()
	seedLiveSessionsDB(t, cfgDir, registered, []sessionSeed{
		{"live-A", "working", "%5"},
	})

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--db-dir", cfgDir,
		"session-query", "list-live", "--project-root", unregistered)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("binary exec failed for unregistered root: %v\nstdout: %s", err, out)
	}
	// ProjectIDForPath auto-registers unknown paths as anonymous, so the
	// unregistered path becomes its own project with no sessions; the
	// list is empty either way.
	var got struct {
		Rows []map[string]any `json:"rows"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode json: %v\nraw: %s", err, out)
	}
	if len(got.Rows) != 0 {
		t.Errorf("got %d sessions for unregistered root, want 0; raw: %s", len(got.Rows), out)
	}
}

// TestListLive_BinaryMissingFlagExitsNonZero pins the input-validation
// contract: omitting --project-root is a usage error.
func TestListLive_BinaryMissingFlagExitsNonZero(t *testing.T) {
	cfgDir := t.TempDir()
	seedLiveSessionsDB(t, cfgDir, t.TempDir(), nil)

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--db-dir", cfgDir,
		"session-query", "list-live")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when --project-root is omitted, got success\nout: %s", out)
	}
	_ = out
}

// TestListLive_AllProjects is E-2358's contract for a lookup by id: with
// --all-projects the verb lists every project's live sessions, each row naming
// its own project's path, and the two flags are exclusive. The scoped form is
// unchanged, for listings.
func TestListLive_AllProjects(t *testing.T) {
	cfgDir := t.TempDir()
	rootA := t.TempDir()
	rootB := t.TempDir()
	seedLiveSessionsDB(t, cfgDir, rootA, []sessionSeed{{"in-a", "working", "%5"}})
	db, err := sql.Open("sqlite", filepath.Join(cfgDir, "endless.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(
		"INSERT INTO projects (id, name, path) VALUES (2, 'proj-b', ?)", rootB,
	); err != nil {
		t.Fatalf("seed project b: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process_id, last_activity)
		 VALUES ('in-b', 2, 'claude', 'working', ?, '2026-05-20T00:00:00')`,
		seedPane(t, db, "%6")); err != nil {
		t.Fatalf("seed session in-b: %v", err)
	}
	db.Close()

	bin := endlessGoBin(t)
	rows := func(args ...string) map[string]string {
		t.Helper()
		out, err := exec.Command(bin, append([]string{"--db-dir", cfgDir, "session-query", "list-live"}, args...)...).Output()
		if err != nil {
			t.Fatalf("list-live %v: %v\nstdout: %s", args, err, out)
		}
		var got struct {
			Rows []struct {
				SessionID   string `json:"session_id"`
				ProjectPath string `json:"project_path"`
			} `json:"rows"`
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Fatalf("decode: %v\nraw: %s", err, out)
		}
		paths := map[string]string{}
		for _, r := range got.Rows {
			paths[r.SessionID] = r.ProjectPath
		}
		return paths
	}
	resolve := func(p string) string {
		t.Helper()
		r, err := filepath.EvalSymlinks(p)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	all := rows("--all-projects")
	if len(all) != 2 || all["in-a"] != resolve(rootA) || all["in-b"] != resolve(rootB) {
		t.Errorf("--all-projects: got %v, want in-a→%s, in-b→%s", all, resolve(rootA), resolve(rootB))
	}
	scoped := rows("--project-root", rootA)
	if len(scoped) != 1 || scoped["in-a"] != resolve(rootA) {
		t.Errorf("--project-root: got %v, want only in-a", scoped)
	}

	out, err := exec.Command(bin, "--db-dir", cfgDir, "session-query", "list-live",
		"--all-projects", "--project-root", rootA).CombinedOutput()
	if err == nil {
		t.Fatalf("both flags accepted\nout: %s", out)
	}
}
