//go:build ignore

// E-1754: one-shot backfill of committed document-mirror files for
// pre-existing task/decision content.
//
// E-1747 mirrors tasks.{text,outcome,analysis} and decision bodies to committed
// .endless/{plans,outcomes,analyses,decisions}/*.md files, but only for content
// written from then on. Rows already carrying those fields in the DB (and
// existing decisions, and any task whose text never got a plan file) have no
// mirror yet. This program fills those gaps.
//
// Run it FROM the E-1754 worktree:
//
//	cd <endless>/.endless/worktrees/e-1754
//	go run .endless/migrations/e-1754-backfill-doc-mirrors.go
//
// It reads the REAL endless DB (PinMainDB routes to ~/.config/endless/endless.db
// and satisfies the E-1429 self-dev-worktree gate; the E-1754 sandbox is never
// consulted), then for the worktree's project writes any ABSENT mirror file into
// the worktree's .endless/<subdir>/ and makes ONE commit on the worktree branch.
// The files ride to main via the normal `endless worktree land E-1754`.
//
// Gap-fill only: a mirror is written solely when its target file is absent, so
// the program never overwrites an existing file (no drift reconciliation, no
// rewrite of the plan files already on main) and a second run is a pure no-op.
//
// The //go:build ignore tag keeps this one-off package main out of
// `go build/vet/test ./...`; naming the file explicitly to `go run`/`go build`
// still compiles it (same idiom as internal/schema/changes/e-NNN-*.go). It does
// NOT live under internal/schema/changes/ — that is the DDL-via-runner home the
// land dispatcher auto-applies; this is a read-only + files + git one-off, so it
// sits beside the other committed endless artifacts under .endless/.
package main

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// docField maps a source DB column to the committed-mirror subdir and the
// E-/ED- prefix its files carry. Mirrors E-1747's _TASK_DOC_FIELDS
// (worktree_cmd.py) plus the decision body.
type docField struct {
	column string // DB column holding the document body
	subdir string // .endless/<subdir>/
	prefix string // "E-" for tasks, "ED-" for decisions
}

var taskFields = []docField{
	{column: "text", subdir: "plans", prefix: "E-"},
	{column: "outcome", subdir: "outcomes", prefix: "E-"},
	{column: "analysis", subdir: "analyses", prefix: "E-"},
}

var decisionField = docField{column: "description", subdir: "decisions", prefix: "ED-"}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "e-1754-backfill: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	wt, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve cwd: %w", err)
	}
	if _, err := os.Stat(filepath.Join(wt, ".endless")); err != nil {
		return fmt.Errorf("cwd %q has no .endless/ — run this from the E-1754 worktree", wt)
	}

	// PinMainDB: read the REAL DB (~/.config/endless), never the sandbox, and
	// satisfy the E-1429 gate that would otherwise refuse a DB open from inside
	// a self-dev worktree. Must precede the first DB() use.
	monitor.PinMainDB()
	db, err := monitor.DB()
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}

	projectID, found, err := monitor.ProjectIDForPath(wt)
	if err != nil {
		return fmt.Errorf("resolve project for %q: %w", wt, err)
	}
	if !found {
		return fmt.Errorf("no registered project found for %q; refusing to auto-register", wt)
	}

	var written []string

	taskWritten, taskSkipped, err := backfillTasks(db, projectID, wt, &written)
	if err != nil {
		return err
	}
	decWritten, decSkipped, err := backfillDecisions(db, projectID, wt, &written)
	if err != nil {
		return err
	}

	fmt.Printf("Backfill summary (project id %d):\n", projectID)
	fmt.Printf("  tasks:     %d written, %d skipped (present/empty)\n", taskWritten, taskSkipped)
	fmt.Printf("  decisions: %d written, %d skipped (present/empty)\n", decWritten, decSkipped)

	if len(written) == 0 {
		fmt.Println("Nothing to backfill — every mirror already present. No commit made.")
		return nil
	}

	subject := fmt.Sprintf("Endless: backfill doc mirrors (%d files)", len(written))
	if err := commitFiles(wt, written, subject); err != nil {
		return err
	}
	fmt.Printf("Committed %d mirror files on the worktree branch: %q\n", len(written), subject)
	return nil
}

// backfillTasks writes any absent plan/outcome/analysis mirror for every task in
// the project. Returns (written, skipped) file counts and appends written
// repo-relative paths to *written.
func backfillTasks(db *sql.DB, projectID int64, wt string, written *[]string) (int, int, error) {
	rows, err := db.Query(
		`SELECT id, COALESCE(text,''), COALESCE(outcome,''), COALESCE(analysis,'')
		   FROM tasks WHERE project_id = ? ORDER BY id`,
		projectID,
	)
	if err != nil {
		return 0, 0, fmt.Errorf("query tasks: %w", err)
	}
	defer rows.Close()

	var wc, sc int
	for rows.Next() {
		var id int64
		var text, outcome, analysis string
		if err := rows.Scan(&id, &text, &outcome, &analysis); err != nil {
			return 0, 0, fmt.Errorf("scan task row: %w", err)
		}
		byColumn := map[string]string{"text": text, "outcome": outcome, "analysis": analysis}
		for _, f := range taskFields {
			w, err := writeMirror(wt, f, id, byColumn[f.column], written)
			if err != nil {
				return 0, 0, err
			}
			if w {
				wc++
			} else {
				sc++
			}
		}
	}
	return wc, sc, rows.Err()
}

// backfillDecisions writes any absent decision-body mirror for every decision in
// the project.
func backfillDecisions(db *sql.DB, projectID int64, wt string, written *[]string) (int, int, error) {
	rows, err := db.Query(
		`SELECT id, COALESCE(description,'') FROM decisions WHERE project_id = ? ORDER BY id`,
		projectID,
	)
	if err != nil {
		return 0, 0, fmt.Errorf("query decisions: %w", err)
	}
	defer rows.Close()

	var wc, sc int
	for rows.Next() {
		var id int64
		var body string
		if err := rows.Scan(&id, &body); err != nil {
			return 0, 0, fmt.Errorf("scan decision row: %w", err)
		}
		w, err := writeMirror(wt, decisionField, id, body, written)
		if err != nil {
			return 0, 0, err
		}
		if w {
			wc++
		} else {
			sc++
		}
	}
	return wc, sc, rows.Err()
}

// writeMirror writes <wt>/.endless/<subdir>/<prefix><id>.md when the content is
// non-empty AND the file is absent (gap-fill: never overwrites). Returns true
// when a file was written, appending its repo-relative path to *written. The raw
// column value is written verbatim, matching E-1747's write-time mirror.
func writeMirror(wt string, f docField, id int64, content string, written *[]string) (bool, error) {
	if strings.TrimSpace(content) == "" {
		return false, nil
	}
	relPath := filepath.Join(".endless", f.subdir, fmt.Sprintf("%s%d.md", f.prefix, id))
	absPath := filepath.Join(wt, relPath)
	if _, err := os.Stat(absPath); err == nil {
		return false, nil // present → gap-fill skips
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat %q: %w", relPath, err)
	}
	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return false, fmt.Errorf("mkdir for %q: %w", relPath, err)
	}
	if err := os.WriteFile(absPath, []byte(content), 0o644); err != nil {
		return false, fmt.Errorf("write %q: %w", relPath, err)
	}
	*written = append(*written, relPath)
	return true, nil
}

// commitFiles stages and commits exactly the given repo-relative paths on the
// worktree branch in ONE commit. Plain `git -C` — NOT events.commitPaths, whose
// ensureMainCheckout refuses linked worktrees. `git add` is required before
// `commit -o` because commit's pathspec cannot resolve untracked files; `-o`
// (--only) scopes the commit to exactly these paths, ignoring unrelated dirt.
func commitFiles(wt string, paths []string, subject string) error {
	addArgs := append([]string{"-C", wt, "add", "--"}, paths...)
	if out, err := exec.Command("git", addArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %w: %s", err, strings.TrimSpace(string(out)))
	}
	commitArgs := []string{"-C", wt, "commit"}
	for _, p := range paths {
		commitArgs = append(commitArgs, "-o", p)
	}
	commitArgs = append(commitArgs, "-m", subject)
	if out, err := exec.Command("git", commitArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("git commit: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
