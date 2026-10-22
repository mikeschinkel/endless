package monitor

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Forgetting a mistaken registration (E-2349).
//
// `endless project forget` makes Endless stop knowing a project without
// touching the project's own files: unlike unregister it writes nothing to its
// .endless/config.json and records no ignored row. The row is tombstoned, not
// deleted, for the reason ED-1547 gives for tasks: rows that name a project id
// without a foreign key must never point at a re-minted one.
//
// The tombstone frees everything a new registration of the same checkout would
// collide with. name and path carry table-level UNIQUE constraints, so they are
// rewritten rather than skipped by a partial index: the name to
// forgotten-<id>, which is not a name a project can be registered under, and
// the path to one under /dev/null, under which no directory can exist, so no
// lookup by path and no walk up a directory's ancestors can ever match it.
// identity and slug become NULL, which their unique indexes admit any number
// of. What the row held is kept in forgotten_from.

// ProjectStatusForgotten marks a tombstoned row. live_projects excludes it.
const ProjectStatusForgotten = "forgotten"

// ErrForgetHasTasks refuses forgetting a project whose tasks still reference
// it; the caller removes them first, or ignores the project instead.
var ErrForgetHasTasks = errors.New("project still has tasks")

// ForgottenFrom is what a tombstoned row held, kept as projects.forgotten_from.
type ForgottenFrom struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	Slug        string `json:"slug,omitempty"`
	Identity    string `json:"identity,omitempty"`
	Status      string `json:"status"`
	ForgottenAt string `json:"forgotten_at"`
}

// ForgetResult reports a forget: the row's id and what it held.
type ForgetResult struct {
	ID   int64         `json:"id"`
	From ForgottenFrom `json:"forgotten_from"`
}

// ForgetProject tombstones the row registered at dir. Refused with
// ErrNoProject when no row is registered there, and with ErrForgetHasTasks
// when live tasks still belong to it.
func ForgetProject(db *sql.DB, dir string) (ForgetResult, error) {
	var r ForgetResult
	stored, err := StoredProjectPath(dir)
	if err != nil {
		return r, err
	}
	var slug, identity sql.NullString
	err = db.QueryRow(
		"SELECT id, name, path, status, slug, identity FROM projects WHERE path = ?",
		stored,
	).Scan(&r.ID, &r.From.Name, &r.From.Path, &r.From.Status, &slug, &identity)
	if errors.Is(err, sql.ErrNoRows) {
		return r, fmt.Errorf("%w: nothing is registered at %s", ErrNoProject, stored)
	}
	if err != nil {
		return r, err
	}
	r.From.Slug, r.From.Identity = slug.String, identity.String

	var tasks int
	if err = db.QueryRow(
		"SELECT count(*) FROM live_tasks WHERE project_id = ?", r.ID,
	).Scan(&tasks); err != nil {
		return r, err
	}
	if tasks > 0 {
		return r, fmt.Errorf("%w: '%s' at %s has %d task(s)",
			ErrForgetHasTasks, r.From.Name, stored, tasks)
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	r.From.ForgottenAt = now
	from, err := json.Marshal(r.From)
	if err != nil {
		return r, err
	}
	tombstone := fmt.Sprintf("forgotten-%d", r.ID)
	if _, err = db.Exec(
		`UPDATE projects
		    SET status = ?, name = ?, path = ?, slug = NULL, identity = NULL,
		        forgotten_from = ?, updated_at = ?
		  WHERE id = ?`,
		ProjectStatusForgotten, tombstone, "/dev/null/endless-"+tombstone,
		string(from), now, r.ID,
	); err != nil {
		return r, fmt.Errorf("forgetting %s: %w", stored, err)
	}
	return r, nil
}
