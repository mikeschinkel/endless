package monitor

import (
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
)

// seedForgettable inserts a registered project at dir with a slug and identity,
// as register writes one, and returns its stored path.
func seedForgettable(t *testing.T, db *sql.DB, id int64, name, dir string) string {
	t.Helper()
	stored, err := StoredProjectPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(
		`INSERT INTO projects (id, name, path, slug, identity) VALUES (?, ?, ?, ?, ?)`,
		id, name, stored, name, "root-"+name,
	); err != nil {
		t.Fatalf("seed project %s: %v", name, err)
	}
	return stored
}

// TestForgetProject_TombstonesAndKeepsWhatItHeld pins E-2349: the row is kept,
// marked forgotten, out of live_projects, and what it held is in
// forgotten_from.
func TestForgetProject_TombstonesAndKeepsWhatItHeld(t *testing.T) {
	db := withTestDB(t)
	dir := tempProjectRoot(t)
	stored := seedForgettable(t, db, 70, "e-2206", dir)

	result, err := ForgetProject(db, dir)
	if err != nil {
		t.Fatalf("ForgetProject: %v", err)
	}
	if result.ID != 70 || result.From.Name != "e-2206" || result.From.Path != stored {
		t.Errorf("result = %+v, want id 70 holding e-2206 at %s", result, stored)
	}

	var name, path, status string
	var slug, identity, from sql.NullString
	if err = db.QueryRow(
		`SELECT name, path, status, slug, identity, forgotten_from FROM projects WHERE id = 70`,
	).Scan(&name, &path, &status, &slug, &identity, &from); err != nil {
		t.Fatalf("row 70 should still exist: %v", err)
	}
	if status != ProjectStatusForgotten || name != "forgotten-70" ||
		path != "/dev/null/endless-forgotten-70" || slug.Valid || identity.Valid {
		t.Errorf("tombstone = (%q, %q, %q, slug %v, identity %v)", name, path, status, slug, identity)
	}
	var held ForgottenFrom
	if err = json.Unmarshal([]byte(from.String), &held); err != nil {
		t.Fatalf("forgotten_from %q: %v", from.String, err)
	}
	if held.Name != "e-2206" || held.Path != stored || held.Slug != "e-2206" ||
		held.Identity != "root-e-2206" || held.ForgottenAt == "" {
		t.Errorf("forgotten_from = %+v", held)
	}

	var live int
	if err = db.QueryRow(`SELECT count(*) FROM live_projects WHERE id = 70`).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 0 {
		t.Error("a forgotten row is still in live_projects")
	}
}

// TestForgetProject_FreesPathNameSlugAndIdentity: a new registration of the
// same checkout, under the same name, slug and identity, is accepted.
func TestForgetProject_FreesPathNameSlugAndIdentity(t *testing.T) {
	db := withTestDB(t)
	dir := tempProjectRoot(t)
	stored := seedForgettable(t, db, 71, "go-cfgstore", dir)
	if _, err := ForgetProject(db, dir); err != nil {
		t.Fatalf("ForgetProject: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, path, slug, identity) VALUES (72, 'go-cfgstore', ?, 'go-cfgstore', 'root-go-cfgstore')`,
		stored,
	); err != nil {
		t.Fatalf("re-registering a forgotten project's checkout: %v", err)
	}
	v, err := ResolveDirectory(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if v.ProjectID != 72 {
		t.Errorf("directory resolves to project %d, want the new row 72", v.ProjectID)
	}
}

// TestForgetProject_RefusesWithLiveTasks: tasks still reference the row, so
// the caller removes them first; removed tasks do not count.
func TestForgetProject_RefusesWithLiveTasks(t *testing.T) {
	db := withTestDB(t)
	dir := tempProjectRoot(t)
	seedForgettable(t, db, 73, "busy", dir)
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, status) VALUES (900, 73, 't', 'unplanned')`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := ForgetProject(db, dir); !errors.Is(err, ErrForgetHasTasks) {
		t.Fatalf("err = %v, want ErrForgetHasTasks", err)
	}
	if _, err := db.Exec(`UPDATE tasks SET removed = 1 WHERE id = 900`); err != nil {
		t.Fatal(err)
	}
	if _, err := ForgetProject(db, dir); err != nil {
		t.Errorf("with its only task removed: %v", err)
	}
}

// TestForgetProject_NoRow refuses a directory nothing is registered at.
func TestForgetProject_NoRow(t *testing.T) {
	db := withTestDB(t)
	if _, err := ForgetProject(db, tempProjectRoot(t)); !errors.Is(err, ErrNoProject) {
		t.Errorf("err = %v, want ErrNoProject", err)
	}
}
