package monitor

import (
	"database/sql"
	"errors"
	"os"
)

// This file holds the reads behind document mirrors (E-2137) — the `.md` files
// that project a task's plan / outcome / analysis, and a decision's body, into
// the repository so a human can read them on github.com without a database.
//
// The reads live in monitor with every other read; where the files GO is
// internal/docmirror's business, and what to do about a file that disagrees
// with its row is internal/docsweep's. Keeping the three apart is what lets the
// hook depend on the path convention without depending on SQLite.

// ProjectRef is a registered project: its row id and its resolved filesystem
// root. Callers that write into a project's checkout need both — the id to ask
// what belongs to it, the root to know where to put it.
type ProjectRef struct {
	ID   int64
	Root string
}

// ActiveProjects returns every registered active project that still exists on
// disk, ordered by id.
//
// A project whose directory has gone — moved, deleted, on an unmounted volume —
// is skipped rather than reported: that is a condition for the project-path
// repair to resolve, and a background sweep must not raise it per pass.
func ActiveProjects() ([]ProjectRef, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		"SELECT id, path FROM projects WHERE status = 'active' ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var projects []ProjectRef
	for rows.Next() {
		var id int64
		var stored string
		if err = rows.Scan(&id, &stored); err != nil {
			return nil, err
		}
		// The column is STORED form (normally `~/...`); everything below this point
		// hands the path to git and the filesystem (E-2011).
		resolved, rerr := ResolvedProjectPath(stored)
		if rerr != nil || resolved == "" {
			continue
		}
		if fi, serr := os.Stat(resolved); serr != nil || !fi.IsDir() {
			continue
		}
		projects = append(projects, ProjectRef{ID: id, Root: resolved})
	}
	return projects, rows.Err()
}

// DocRow is one row's worth of mirrored content: the entity id, the content
// name it came from (empty for a decision, whose body has one home), and the
// content itself.
type DocRow struct {
	ID      int64
	Name    string
	Content string
}

// TaskDocRows returns every task content row of a project's live tasks, one
// DocRow per (task, name).
//
// Only non-empty content is returned, and that is a rule the sweep depends on
// rather than an optimization. Regenerating a mirror from its content can never
// lose anything WHEN THERE IS SOMETHING TO REGENERATE; rewriting a file from
// empty content would replace it with nothing. task_content holds no empty rows
// by construction (E-1531); the filter below says so anyway, because the sweep's
// safety should not rest on a write path it cannot see.
func TaskDocRows(projectID int64) ([]DocRow, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`
		SELECT c.task_id, c.name, c.content
		  FROM task_content c
		  JOIN live_tasks t ON t.id = c.task_id
		 WHERE t.project_id = ? AND c.content != ''
		 ORDER BY 1, 2`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanDocRows(rows)
}

// DecisionDocRows returns every non-empty decision body in a project. Name is
// empty: a decision's mirror has one source and naming it would suggest a
// choice that does not exist.
func DecisionDocRows(projectID int64) ([]DocRow, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		"SELECT id, '', description FROM decisions "+
			"WHERE project_id = ? AND COALESCE(description, '') != '' ORDER BY id",
		projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanDocRows(rows)
}

func scanDocRows(rows *sql.Rows) ([]DocRow, error) {
	var out []DocRow
	for rows.Next() {
		var r DocRow
		if err := rows.Scan(&r.ID, &r.Name, &r.Content); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DecisionBody returns the `decisions.description` content for a decision id —
// the column `.endless/decisions/ED-NNNN.md` mirrors.
//
// Returns an empty string and no error when the row or the body is absent: to
// every caller here "no body" and "no such decision" have the same consequence
// (there is nothing a mirror should say), and distinguishing them would only
// invite a caller to treat a deleted decision as a failure to read one.
func DecisionBody(decisionID int64) (string, error) {
	db, err := DB()
	if err != nil {
		return "", err
	}
	var body string
	err = db.QueryRow(
		"SELECT COALESCE(description, '') FROM decisions WHERE id = ?",
		decisionID,
	).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return body, nil
}
