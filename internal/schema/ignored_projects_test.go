package schema_test

import (
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/mikeschinkel/endless/internal/schema"
)

// migrateIn builds a database at dir/endless.db, writing cfg beside it first
// when non-empty, and returns the projects rows the migrations left behind.
func migrateIn(t *testing.T, dir, cfg string) map[string]string {
	t.Helper()
	if cfg != "" {
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "endless.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err := schema.Migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	rows, err := db.Query("SELECT path, name || '|' || status FROM projects")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var p, v string
		if err := rows.Scan(&p, &v); err != nil {
			t.Fatal(err)
		}
		got[p] = v
	}
	var live int
	if err := db.QueryRow("SELECT count(*) FROM live_projects").Scan(&live); err != nil {
		t.Fatalf("live_projects: %v", err)
	}
	if live != 0 {
		t.Errorf("live_projects lists %d ignored rows, want 0", live)
	}
	return got
}

// TestMigrate15_ImportsTheSiblingConfigIgnoreList pins E-2251's one-time
// conversion: each `ignore` entry in the config.json beside the database
// becomes an ignored row named by its stored path.
func TestMigrate15_ImportsTheSiblingConfigIgnoreList(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	realHome, _ := filepath.EvalSymlinks(home)
	outside, _ := filepath.EvalSymlinks(t.TempDir())

	got := migrateIn(t, t.TempDir(),
		`{"ignore": ["~/Projects/clients", "`+filepath.Join(realHome, "Projects", "go")+`", "`+outside+`", "", "relative/x"]}`)

	want := map[string]string{
		"~/Projects/clients": "~/Projects/clients|ignored",
		"~/Projects/go":      "~/Projects/go|ignored",
		outside:              outside + "|ignored",
	}
	if len(got) != len(want) {
		keys := make([]string, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Fatalf("rows = %v, want %v", keys, want)
	}
	for p, v := range want {
		if got[p] != v {
			t.Errorf("row %s = %q, want %q", p, got[p], v)
		}
	}
}

// TestMigrate15_NoSiblingConfigImportsNothing pins the guard that keeps a
// sandbox, test or projection database from importing the user's list: only a
// database with a config.json beside it reads one.
func TestMigrate15_NoSiblingConfigImportsNothing(t *testing.T) {
	if got := migrateIn(t, t.TempDir(), ""); len(got) != 0 {
		t.Errorf("rows = %v, want none", got)
	}
}
