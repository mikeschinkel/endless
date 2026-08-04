package monitor

import (
	"fmt"
	"strconv"
	"strings"
)

// E-1865 — the explanation behind the ◆ marker.
//
// `endless session status` renders a bare ◆ for an unsettled worktree, but the
// two sub-states it collapses need OPPOSITE fixes: modified means commit-or-
// discard, unlanded means land. This file turns the boolean predicate into the
// breakdown that says which, so the marker and its explanation can never
// disagree — taskWorktreeUnsettled is now a thin wrapper over WorktreeUnsettledAt
// rather than a second implementation of the same git probes.
//
// Vocabulary is ED-1540's: settled = clean AND fully in main; unsettled =
// modified (uncommitted working-tree changes) OR unlanded (commits not in main).
//
// Deliberately distinct from WorktreeAnomalies (worktree_anomalies.go), which
// answers a different question — "is this worktree in a state a handoff should
// narrate?" — and by design stays silent on commits ahead of main. This probe
// must report exactly what the ◆ reports, including the auto-managed churn the
// anomaly path filters out, or it would explain a marker the user isn't seeing.

// UnsettledDetail is the breakdown behind one worktree's unsettled state: which
// files are modified (split into user work vs endless's own auto-managed files)
// and how many commits are unlanded, plus the fail-open error flags that make
// Unsettled() reproduce the original predicate exactly.
type UnsettledDetail struct {
	// HasWorktree is false when the task has no worktree — nothing to land.
	HasWorktree bool
	// WorktreePath is the inspected directory (empty when HasWorktree is false).
	WorktreePath string

	// Modified are repo-relative paths of uncommitted/untracked USER files.
	Modified []string
	// AutoManaged are the same, but matching AutoManagedStatusGlobs — endless's
	// own ledger churn, which `worktree land` commits on the user's behalf. They
	// still count toward unsettled (the ◆ predicate does not filter them), but
	// are reported separately so "◆ but only ledger churn" is legible at a glance.
	AutoManaged []string

	// UnlandedCount is `git rev-list main..HEAD --count`.
	UnlandedCount int
	// UnlandedLog holds up to unlandedLogLimit "<short-sha> <subject>" lines for
	// display only; UnlandedCount, not len(UnlandedLog), drives the predicate.
	UnlandedLog []string

	// Branch is the worktree's current branch ("" when detached).
	Branch string

	// StatusErr / RevListErr record a failed git probe. Both are fail-open: the
	// original predicate treats any git error as settled, because a view must
	// never block or lie because a git call hiccuped.
	StatusErr  string
	RevListErr string
}

// unlandedLogLimit caps the commit subjects carried for display. The count is
// always exact; this only bounds the sample rendered under it.
const unlandedLogLimit = 20

// Unsettled reports whether this worktree is unsettled. It reproduces the
// original taskWorktreeUnsettled logic exactly, INCLUDING its short-circuit
// order: a non-empty `git status` wins before rev-list is consulted, so a
// modified worktree whose rev-list probe fails is still unsettled. Any earlier
// git failure yields settled (fail-open).
func (d UnsettledDetail) Unsettled() bool {
	if !d.HasWorktree || d.StatusErr != "" {
		return false
	}
	if len(d.Modified)+len(d.AutoManaged) > 0 {
		return true
	}
	if d.RevListErr != "" {
		return false
	}
	return d.UnlandedCount > 0
}

// IsModified reports the uncommitted-changes sub-state (ED-1540 "modified").
func (d UnsettledDetail) IsModified() bool {
	return len(d.Modified)+len(d.AutoManaged) > 0
}

// IsUnlanded reports the commits-not-in-main sub-state (ED-1540 "unlanded").
func (d UnsettledDetail) IsUnlanded() bool {
	return d.RevListErr == "" && d.UnlandedCount > 0
}

// Reason renders the one-line summary used by the list view: the sub-states that
// are true, or "settled" when none are. Kept here so the Go probe and every
// caller phrase the verdict identically.
func (d UnsettledDetail) Reason() string {
	if !d.HasWorktree {
		return "no worktree"
	}
	if d.StatusErr != "" {
		return "settled (git status failed: " + d.StatusErr + ")"
	}
	var parts []string
	if n := len(d.Modified) + len(d.AutoManaged); n > 0 {
		switch {
		case len(d.Modified) == 0:
			parts = append(parts, fmt.Sprintf("modified (%d auto-managed only)", n))
		case len(d.AutoManaged) == 0:
			parts = append(parts, fmt.Sprintf("modified (%d %s)", n, plural(n, "file")))
		default:
			parts = append(parts, fmt.Sprintf("modified (%d %s, %d auto-managed)",
				n, plural(n, "file"), len(d.AutoManaged)))
		}
	}
	if d.IsUnlanded() {
		parts = append(parts, fmt.Sprintf("unlanded (%d %s)",
			d.UnlandedCount, plural(d.UnlandedCount, "commit")))
	}
	if len(parts) == 0 {
		return "settled"
	}
	return strings.Join(parts, " + ")
}

// WorktreeUnsettledAt returns the verdict for a worktree using ONLY the two git
// probes the ◆ predicate runs. This is the hot path: AnnotateSessionStatusUnsettled
// calls it once per row on every flat `session status` render, so it must not pay
// for detail nobody is going to read.
//
// No DB read — the same DB-free shape WorktreeAnomaliesAt adopted in E-1766, so
// it behaves identically in a self-dev worktree (whose DB routes to a
// per-worktree sandbox that lacks the task row) and in a consumer project.
func WorktreeUnsettledAt(worktreePath string) UnsettledDetail {
	return worktreeUnsettledAt(worktreePath, false)
}

// WorktreeUnsettledDetailAt returns the verdict PLUS the display enrichment
// (unlanded commit subjects, current branch) that `task unsettled <id>` renders.
// Costs two extra git calls per worktree, so it is reserved for the surfaces
// that actually show the breakdown — never the per-row marker.
func WorktreeUnsettledDetailAt(worktreePath string) UnsettledDetail {
	return worktreeUnsettledAt(worktreePath, true)
}

// worktreeUnsettledAt is the shared core. It runs exactly the two git probes the
// ◆ predicate runs, in the same order; the enrich flag adds display-only calls
// that never affect the verdict.
func worktreeUnsettledAt(worktreePath string, enrich bool) UnsettledDetail {
	d := UnsettledDetail{HasWorktree: worktreePath != "", WorktreePath: worktreePath}
	if !d.HasWorktree {
		return d
	}

	// Probe 1 — uncommitted changes. Partitioned for display only: BOTH halves
	// count toward unsettled, matching the predicate's unfiltered `status` test.
	out, gerr := runGit(worktreePath, "status", "--porcelain")
	if gerr != nil {
		d.StatusErr = firstLine(out, gerr)
		return d
	}
	for _, p := range statusPaths(out) {
		if isAutoManagedPath(p) {
			d.AutoManaged = append(d.AutoManaged, p)
		} else {
			d.Modified = append(d.Modified, p)
		}
	}

	// Probe 2 — commits not yet on main. `main` is hardcoded to match the
	// predicate (and the reaper); a task branched off anything else is out of
	// scope until that assumption is lifted repo-wide.
	out, gerr = runGit(worktreePath, "rev-list", "main..HEAD", "--count")
	if gerr != nil {
		d.RevListErr = firstLine(out, gerr)
	} else if n, perr := strconv.Atoi(strings.TrimSpace(out)); perr != nil {
		d.RevListErr = "unparsable rev-list count: " + strings.TrimSpace(out)
	} else {
		d.UnlandedCount = n
	}

	if !enrich {
		return d
	}

	// Display-only enrichment. Failures here are silent: they must never change
	// the verdict, only the detail rendered under it.
	if d.UnlandedCount > 0 {
		if lg, lerr := runGit(worktreePath, "log", "--oneline", "--no-decorate",
			"-n", strconv.Itoa(unlandedLogLimit), "main..HEAD"); lerr == nil {
			for _, ln := range strings.Split(strings.TrimSpace(lg), "\n") {
				if ln = strings.TrimSpace(ln); ln != "" {
					d.UnlandedLog = append(d.UnlandedLog, ln)
				}
			}
		}
	}
	if br, berr := runGit(worktreePath, "symbolic-ref", "--short", "--quiet", "HEAD"); berr == nil {
		d.Branch = strings.TrimSpace(br)
	}

	return d
}

// TaskWorktreeUnsettledDetail resolves a task's worktree from the DB and returns
// its verdict (no display enrichment — this backs the per-row ◆ marker). An
// unresolvable or absent worktree yields HasWorktree=false, which Unsettled()
// reports as settled: a task with no worktree has nothing to land.
func TaskWorktreeUnsettledDetail(projectID, taskID int64) UnsettledDetail {
	wt, err := WorktreePathForTask(projectID, taskID)
	if err != nil || wt == "" {
		return UnsettledDetail{}
	}
	return WorktreeUnsettledAt(wt)
}

// statusPaths parses `git status --porcelain` output into repo-relative paths.
// Sibling of userStatusPaths (worktree_anomalies.go), which returns only the
// non-auto-managed subset; this one keeps every path so the caller can partition
// them and still see the auto-managed half.
func statusPaths(porcelain string) []string {
	var paths []string
	for _, ln := range strings.Split(porcelain, "\n") {
		if len(ln) < 4 {
			continue
		}
		p := ln[3:]
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+len(" -> "):]
		}
		paths = append(paths, strings.Trim(p, "\""))
	}
	return paths
}

// plural returns noun with an "s" unless n is 1.
func plural(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

// firstLine renders a git failure compactly: the command's first output line
// when it produced one, else the error itself.
func firstLine(out string, err error) string {
	for _, ln := range strings.Split(out, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			return ln
		}
	}
	return err.Error()
}
