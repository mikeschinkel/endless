// Package docsweep keeps a project's document mirrors on main correct and in
// the right place (E-2137).
//
// # What a mirror is, and what can go wrong with it
//
// `.endless/tasks/e-NNNN/plan.md` is a projection of `tasks.plan`. The column is
// the source of truth; the file exists so a human can read the plan on
// github.com without a database. `endless task update` writes both — the column
// through the event pipeline, the file straight onto the main checkout.
//
// Two things can leave the file wrong, and this sweep fixes both:
//
//  1. **It is in the old place.** Mirrors used to live in
//     `.endless/{plans,outcomes,analyses}/E-NNNN.md`, four hundred and thirty-two
//     of them in Endless's own repository. More keep arriving: a worktree created
//     before this landed runs its own older copy of Endless, keeps writing to the
//     old path on its own branch, and delivers the file to main whenever it lands
//     — which may be weeks from now. A one-time rename could not see those. A
//     sweep collects each one on its next pass.
//
//  2. **Its content drifted from the column.** The mirror write is best-effort by
//     design: the database write has already happened and is authoritative, so a
//     failed commit warns and continues rather than reporting failure for work
//     that partly succeeded (the reasoning E-1474 settled for `land`). Something
//     has to come back for it, and this is that something.
//
// # Why this can never lose content
//
// The sweep only ever writes a file from a NON-EMPTY column — monitor.TaskDocRows
// returns no row for an empty one. "Regenerating a derived file is free" holds
// only when there is something to regenerate; rewriting a file from an empty
// column would replace content with nothing. So a task whose outcome was cleared
// keeps its outcome.md until someone deletes it deliberately, which is the
// conservative half of the same rule.
//
// Relocation follows from that: a legacy file is moved, not deleted, whenever
// nothing sits at the new path. It is removed only once the new path holds a
// file, at which point the content exists in two places and one of them is wrong.
//
// # Why it is Go and not a subprocess
//
// The same reason internal/unlandedjob is Go: nothing here calls a model. The
// work is file reads, file writes and one `git commit`, all already in this
// binary. A subprocess would buy a fork and lose the lease's context deadline.
package docsweep

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mikeschinkel/endless/internal/docmirror"
	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// Result reports what one project's sweep did. Counts rather than paths because
// a converged repository reports zeroes forever and a first pass reports several
// hundred of each; the interesting number is how many, not which.
type Result struct {
	// Relocated is legacy-path mirrors moved into the consolidated layout.
	Relocated int

	// Rewritten is mirrors whose bytes did not match their column.
	Rewritten int

	// Created is mirrors that did not exist on main at all. On a first pass this
	// is large and expected: every mirror for a task with a worktree used to live
	// on that worktree's branch and never reached main.
	Created int

	// Superseded is legacy files removed because the consolidated path already
	// held a file.
	Superseded int
}

// Total is how many paths the sweep touched.
func (r Result) Total() int {
	return r.Relocated + r.Rewritten + r.Created + r.Superseded
}

// String renders a one-line summary for a log or a CLI.
func (r Result) String() string {
	return fmt.Sprintf(
		"%d relocated, %d rewritten, %d created, %d superseded",
		r.Relocated, r.Rewritten, r.Created, r.Superseded)
}

// Run sweeps every registered project. One project's failure does not abandon
// the others — the repositories are independent — so failures are collected and
// returned together.
//
// It does nothing at all from inside a self-dev worktree, and that guard is this
// job's alone rather than the runner's. The runner suppresses a worktree pinned
// to the REAL database; a worktree under `--db sandbox` runs jobs normally,
// because everything a sandbox does stays inside the sandbox. This is the one
// job whose output is a FILE IN ANOTHER TREE — it would read a sandbox's test
// content and write it into the developer's actual checkout — so it declines
// there on its own account.
//
// Returning nil rather than an error: a skip is not a failure, and a job that
// recorded a fault on every tick of an ordinary dev session would be pure noise.
func Run(ctx context.Context) error {
	if monitor.InSelfDevWorktree() {
		return nil
	}

	projects, err := monitor.ActiveProjects()
	if err != nil {
		return fmt.Errorf("enumerate projects: %w", err)
	}

	var failures []string
	for _, p := range projects {
		if _, serr := SweepProject(ctx, p); serr != nil {
			failures = append(failures, serr.Error())
		}
		if ctx.Err() != nil {
			// The lease expired or the process is going down. Stopping here is not a
			// failure: the pass is resumable by construction, because it recomputes
			// what it finds rather than continuing from a cursor.
			break
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

// SweepProject brings one project's mirrors into line and commits the result.
//
// The order is relocate-then-reconcile, and it matters: reconciling first would
// CREATE a file at the consolidated path for a task whose legacy file is still
// sitting there, turning every relocation into a supersede and losing the rename
// git would otherwise record.
//
// Everything is staged into one commit. Four hundred relocations as four hundred
// commits would bury every other commit on main, and the relocation is one act.
func SweepProject(ctx context.Context, project monitor.ProjectRef) (Result, error) {
	var result Result

	changed := make(map[string]bool)

	relocated, err := relocateLegacy(project.Root, changed, &result)
	if err != nil {
		return result, fmt.Errorf("%s: relocate: %w", project.Root, err)
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}

	if err = reconcile(project, changed, &result); err != nil {
		return result, fmt.Errorf("%s: reconcile: %w", project.Root, err)
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}

	if len(changed) == 0 {
		return result, nil
	}
	paths := sortedKeys(changed)
	if err = events.CommitDocPaths(project.Root, paths, commitSubject(relocated)); err != nil {
		return result, fmt.Errorf("%s: commit: %w", project.Root, err)
	}
	return result, nil
}

// commitSubject names what the commit did in its own terms. A pass that only
// relocated says so; a mixed pass says the more consequential half first.
func commitSubject(relocated bool) string {
	if relocated {
		return "Endless: consolidate document mirrors under .endless/tasks/"
	}
	return "Endless: reconcile document mirrors"
}

// relocateLegacy moves every `.endless/{plans,outcomes,analyses}/E-NNNN.md` into
// its task's own directory. Returns whether anything moved.
func relocateLegacy(root string, changed map[string]bool, result *Result) (bool, error) {
	moved := false

	for _, kind := range docmirror.TaskKinds {
		dir := filepath.Join(root, ".endless", kind.LegacyDir)
		entries, err := os.ReadDir(dir)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return moved, fmt.Errorf("read %s: %w", dir, err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			legacyRel := ".endless/" + kind.LegacyDir + "/" + e.Name()
			src, ok := docmirror.Resolve(legacyRel)
			if !ok {
				// Something else living in the directory — a README, a stray note.
				// Not ours to move.
				continue
			}
			newRel := docmirror.TaskDocPath(src.TaskID, kind.Stem)
			did, err := relocateOne(root, legacyRel, newRel, result)
			if err != nil {
				return moved, err
			}
			if did {
				moved = true
				changed[legacyRel] = true
				changed[newRel] = true
			}
		}
	}
	return moved, nil
}

// relocateOne moves one legacy file, or removes it when the consolidated path
// already holds one.
func relocateOne(root, legacyRel, newRel string, result *Result) (bool, error) {
	legacyAbs := filepath.Join(root, legacyRel)
	newAbs := filepath.Join(root, newRel)

	if _, err := os.Stat(newAbs); err == nil {
		// Both exist. The consolidated path is where every writer puts a mirror
		// now, so it is the current one; reconcile will correct its content from
		// the column in a moment either way. The legacy copy is the duplicate.
		if err = os.Remove(legacyAbs); err != nil {
			return false, fmt.Errorf("remove superseded %s: %w", legacyRel, err)
		}
		result.Superseded++
		return true, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("stat %s: %w", newRel, err)
	}

	if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
		return false, fmt.Errorf("create %s: %w", filepath.Dir(newRel), err)
	}
	if err := os.Rename(legacyAbs, newAbs); err != nil {
		return false, fmt.Errorf("move %s to %s: %w", legacyRel, newRel, err)
	}
	result.Relocated++
	return true, nil
}

// reconcile writes every mirror whose bytes do not match its column.
func reconcile(project monitor.ProjectRef, changed map[string]bool, result *Result) error {
	taskRows, err := monitor.TaskDocRows(project.ID)
	if err != nil {
		return fmt.Errorf("read task documents: %w", err)
	}
	for _, row := range taskRows {
		kind, ok := docmirror.KindByColumn(row.Column)
		if !ok {
			continue
		}
		rel := docmirror.TaskDocPath(row.ID, kind.Stem)
		if err = writeIfDifferent(project.Root, rel, row.Content, changed, result); err != nil {
			return err
		}
	}

	decisionRows, err := monitor.DecisionDocRows(project.ID)
	if err != nil {
		return fmt.Errorf("read decision bodies: %w", err)
	}
	for _, row := range decisionRows {
		rel := docmirror.DecisionDocPath(row.ID)
		if err = writeIfDifferent(project.Root, rel, row.Content, changed, result); err != nil {
			return err
		}
	}
	return nil
}

// writeIfDifferent writes content to rel when the file is missing or differs.
//
// Compared byte-for-byte rather than after trimming: the file IS the column, and
// a sweep that tolerated a trailing-newline difference would leave the two
// permanently, invisibly out of step.
func writeIfDifferent(
	root, rel, content string, changed map[string]bool, result *Result,
) error {
	abs := filepath.Join(root, rel)
	existing, err := os.ReadFile(abs)
	switch {
	case err == nil:
		if string(existing) == content {
			return nil
		}
		result.Rewritten++
	case errors.Is(err, os.ErrNotExist):
		result.Created++
	default:
		return fmt.Errorf("read %s: %w", rel, err)
	}

	if err = os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(rel), err)
	}
	if err = os.WriteFile(abs, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}
	changed[rel] = true
	return nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
