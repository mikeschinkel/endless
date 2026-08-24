package monitor

import (
	"fmt"
	"path"
	"strings"
)

// E-1758 — worktree anomaly core.
//
// WorktreeAnomalies is the single definition of "a genuine handoff anomaly" in
// a task worktree, consumed by two surfaces so they can never disagree:
//   - `endless worktree check` (agent handoff) via the
//     `session-query worktree-anomalies` Go subcommand.
//   - `endless session status` — the ◆ marker expands to this breakdown for the
//     focal worktree.
//
// An EMPTY result is the representation of "clean": both surfaces print nothing.
// Only real anomalies are reported. Explicitly NOT anomalies (they stay silent):
//   - commits ahead of main (the expected pre-land state, not a divergence to fix)
//   - git tags (endless never creates them)
//
// This is a deliberate sibling of taskWorktreeUnsettled, not a replacement: the ◆
// marker keeps its coarser unsettled (modified-or-unlanded) meaning for the
// navigation view (E-1701). A ◆ with no anomaly breakdown is a clean-but-unlanded
// worktree — the normal, expected state before land.

// AutoManagedStatusGlobs are the endless-owned paths that `git status` may show
// as modified inside a worktree but which are NOT user work, so they never count
// as an anomaly. These are the files `endless worktree land` auto-commits on the
// user's behalf; ambient churn in them alone still means "clean" for handoff.
//
// Mirrors src/endless/worktree_cmd.py AUTO_COMMIT_GLOBS — keep the two in sync.
// (The companion .endless/worktree.json/.lock are gitignored and
// .claude/settings.json is skip-worktree'd, so none of those ever surface in
// `git status`; only these globs can.)
var AutoManagedStatusGlobs = []string{
	".endless/db-ledger/*.jsonl",
	".endless/verbs.jsonl",
	".endless/LESSONS.md",
}

// AnomalyKind is the closed set of worktree anomaly categories. Not DB-backed
// (purely an in-memory render contract), so it follows the house int-const enum
// shape without the task_types-style integrity check.
type AnomalyKind int

const (
	AnomalyUncommitted    AnomalyKind = 1 // untracked/modified USER files remain
	AnomalyDetachedHead   AnomalyKind = 2 // HEAD is detached
	AnomalyBranchMismatch AnomalyKind = 3 // HEAD is not on the companion's branch
	AnomalyPrunable       AnomalyKind = 4 // git marks the worktree prunable/locked
)

// String returns the machine slug for the kind.
func (k AnomalyKind) String() string {
	switch k {
	case AnomalyUncommitted:
		return "uncommitted"
	case AnomalyDetachedHead:
		return "detached-head"
	case AnomalyBranchMismatch:
		return "branch-mismatch"
	case AnomalyPrunable:
		return "prunable"
	default:
		return fmt.Sprintf("AnomalyKind(%d)", int(k))
	}
}

// WorktreeAnomaly is one detected anomaly: its kind plus a terse human detail.
type WorktreeAnomaly struct {
	Kind   AnomalyKind
	Detail string
}

// Line renders the anomaly as a single terse line shared by both surfaces
// (the `worktree check` output and the `session status` focal detail).
func (a WorktreeAnomaly) Line() string {
	return fmt.Sprintf("%s: %s", a.Kind, a.Detail)
}

// WorktreeAnomaliesAt returns the genuine handoff anomalies for a worktree,
// given its path and the repo main checkout — with NO DB read (E-1766). It backs
// the `session-query worktree-anomalies` subcommand, whose Python caller
// (`endless worktree check`) already resolves both paths from cwd; handing them
// straight to the DB-free inspection core removes the round-trip that formerly
// resolved the task's project and worktree from the DB. In a self-dev worktree
// that lookup routed to the per-worktree sandbox (which lacks the task row) and
// errored; path-based resolution behaves identically in self-dev and consumer
// projects. An empty projectRoot disables only the repo-level prunable probe.
func WorktreeAnomaliesAt(projectRoot, worktreePath string) []WorktreeAnomaly {
	return worktreeAnomaliesAt(projectRoot, worktreePath)
}

// WorktreeAnomalies returns the genuine handoff anomalies for the task's
// worktree, or an empty slice when clean (or when there is no worktree). It is
// best-effort: any git error on a given probe skips that probe rather than
// failing — the surfaces must never lie or block because a git call hiccuped.
func WorktreeAnomalies(projectID, taskID int64) []WorktreeAnomaly {
	wt, err := WorktreePathForTask(projectID, taskID)
	if err != nil || wt == "" {
		return nil
	}
	root, rerr := ProjectPath(projectID)
	if rerr != nil {
		// No repo root → skip the prunable probe (empty root disables it) but
		// still report the per-worktree anomalies we can compute from wt alone.
		root = ""
	}
	return worktreeAnomaliesAt(root, wt)
}

// worktreeAnomaliesAt is the path-based inspection core shared by all callers:
// given the repo root and a worktree dir it runs the git probes and returns the
// anomaly list. Kept free of DB/path-resolution so it is unit-testable with a
// stubbed runGit. An empty projectRoot disables only the repo-level prunable
// probe. Best-effort throughout: a failing probe is skipped, never fatal.
func worktreeAnomaliesAt(projectRoot, wt string) []WorktreeAnomaly {
	var anomalies []WorktreeAnomaly

	// 1. Uncommitted/untracked USER files — auto-managed churn partitioned out.
	if out, gerr := runGit(wt, "status", "--porcelain"); gerr == nil {
		if user := userStatusPaths(out); len(user) > 0 {
			anomalies = append(anomalies, WorktreeAnomaly{
				Kind:   AnomalyUncommitted,
				Detail: uncommittedDetail(user),
			})
		}
	}

	// 2. Detached HEAD, or HEAD on a branch other than the companion's.
	// `symbolic-ref --short --quiet HEAD` exits non-zero on a detached HEAD.
	branch, berr := runGit(wt, "symbolic-ref", "--short", "--quiet", "HEAD")
	branch = strings.TrimSpace(branch)
	switch {
	case berr != nil || branch == "":
		anomalies = append(anomalies, WorktreeAnomaly{
			Kind:   AnomalyDetachedHead,
			Detail: "HEAD is detached",
		})
	default:
		if comp, cerr := ReadWorktreeCompanion(wt); cerr == nil &&
			comp.Branch != "" && comp.Branch != branch {
			anomalies = append(anomalies, WorktreeAnomaly{
				Kind:   AnomalyBranchMismatch,
				Detail: fmt.Sprintf("on %s, expected %s", branch, comp.Branch),
			})
		}
	}

	// 3. The worktree is prunable or locked per git's own bookkeeping (a leaked
	// or stale-locked checkout). Read from the repo-level worktree list.
	if projectRoot != "" {
		if detail := worktreePrunableDetail(projectRoot, wt); detail != "" {
			anomalies = append(anomalies, WorktreeAnomaly{
				Kind:   AnomalyPrunable,
				Detail: detail,
			})
		}
	}

	return anomalies
}

// userStatusPaths parses `git status --porcelain` output and returns the paths
// that are user work — i.e. NOT matched by AutoManagedStatusGlobs. Rename/copy
// entries ("R"/"C") carry an " -> newpath"; the destination path is what git
// tracks, so that is what we test.
func userStatusPaths(porcelain string) []string {
	var user []string
	for _, ln := range strings.Split(porcelain, "\n") {
		if len(ln) < 4 {
			continue
		}
		p := ln[3:]
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+len(" -> "):]
		}
		p = strings.Trim(p, "\"")
		if !isAutoManagedPath(p) {
			user = append(user, p)
		}
	}
	return user
}

// isAutoManagedPath reports whether a repo-relative path is one of endless's own
// auto-managed files (AutoManagedStatusGlobs).
func isAutoManagedPath(rel string) bool {
	for _, glob := range AutoManagedStatusGlobs {
		if ok, err := path.Match(glob, rel); err == nil && ok {
			return true
		}
	}
	return false
}

// uncommittedDetail renders a terse count-plus-sample summary of user paths.
func uncommittedDetail(paths []string) string {
	n := len(paths)
	noun := "files"
	if n == 1 {
		noun = "file"
	}
	sample := paths
	if len(sample) > 3 {
		sample = sample[:3]
	}
	tail := ""
	if len(paths) > len(sample) {
		tail = ", …"
	}
	return fmt.Sprintf("%d uncommitted/untracked user %s (%s%s)",
		n, noun, strings.Join(sample, ", "), tail)
}

// worktreePrunableDetail inspects `git worktree list --porcelain` and returns a
// non-empty detail string when the block for wt is marked prunable or locked.
// Best-effort: any git error yields "" (no anomaly claimed).
func worktreePrunableDetail(projectRoot, wt string) string {
	out, err := runGit(projectRoot, "worktree", "list", "--porcelain")
	if err != nil {
		return ""
	}
	// Porcelain output is blank-line-separated blocks; each starts with
	// "worktree <path>", and may include "locked [reason]" and/or "prunable
	// [reason]" attribute lines.
	inTarget := false
	for _, ln := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(ln, "worktree "):
			inTarget = strings.TrimSpace(strings.TrimPrefix(ln, "worktree ")) == wt
		case inTarget && strings.HasPrefix(ln, "locked"):
			return "worktree is locked"
		case inTarget && strings.HasPrefix(ln, "prunable"):
			return "worktree is prunable (stale/leaked)"
		}
	}
	return ""
}
