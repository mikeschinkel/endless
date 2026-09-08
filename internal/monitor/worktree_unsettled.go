package monitor

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mikeschinkel/endless/internal/faults"
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
// Vocabulary is ED-1540's: settled = clean AND fully in the base branch;
// unsettled = modified (uncommitted working-tree changes) OR unlanded (commits
// not in the base branch).
//
// Deliberately distinct from WorktreeAnomalies (worktree_anomalies.go), which
// answers a different question — "is this worktree in a state a handoff should
// narrate?" — and by design stays silent on commits ahead of the base branch.
// This probe must report exactly what the ◆ reports, including the auto-managed
// churn the anomaly path filters out, or it would explain a marker the user
// isn't seeing.
//
// E-1940 changed two things about the verdict itself, in opposite directions:
//
//   - It fails CLOSED. A probe that could not run used to return the all-clear,
//     rendered byte-identically to a verified-clean worktree, on the surface
//     whose whole job is answering "is my work safe?". Now it is undetermined,
//     which marks the row and records a fault. The reaper always failed closed;
//     this is the display catching up to it.
//   - It credited the recorded landing, because a rebasing land rewrites every
//     commit's SHA and the old `<base>..HEAD` count reported landed work as
//     unlanded forever.
//
// E-2087 replaced that second half. The count now comes from a CONTENT
// comparison (worktree_unlanded.go) that sees a rebased — and even a
// conflict-resolved — copy for what it is, so the database credit it used as a
// stand-in is gone and this probe is pure git again.

// UnsettledDetail is the breakdown behind one worktree's unsettled state: which
// files are modified (split into user work vs endless's own auto-managed files)
// and how many commits are unlanded, plus the probe-failure flags that make the
// verdict undetermined rather than clean.
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

	// Base is the resolved default branch the unlanded count is measured
	// against (DefaultBranch, E-1940). Empty when resolution failed.
	Base string

	// UnlandedCount is the number of commits on this branch whose CONTENT has
	// not reached Base — work that genuinely has not landed, as opposed to work
	// a rebasing land re-hashed (E-2087).
	UnlandedCount int
	// UnlandedLog holds up to unlandedLogLimit "<short-sha> <subject>" lines for
	// display; UnlandedCount, not len(UnlandedLog), drives the predicate. These
	// are the unlanded commits themselves, not a sample of the branch — the
	// content comparison identifies them individually, so it costs nothing to
	// name the right ones.
	UnlandedLog []string

	// Branch is the worktree's current branch ("" when detached).
	Branch string

	// LookupErr, BaseErr, StatusErr and UnlandedErr record a probe that could
	// not run: resolving the task's worktree, resolving the default branch,
	// `git status`, and the unlanded-commit comparison respectively. Any of them
	// makes the verdict UNDETERMINED, which Unsettled() reports as unsettled —
	// "I could not tell" must never render as "you are clear" (E-1940).
	LookupErr   string
	BaseErr     string
	StatusErr   string
	UnlandedErr string
}

// unlandedLogLimit caps the commit subjects carried for display. The count is
// always exact; this only bounds the sample rendered under it.
const unlandedLogLimit = 20

// IsUndetermined reports that some probe could not run, so this worktree's
// state is unknown rather than known-clean. Kept distinct from Unsettled() —
// which folds it in — so `task unsettled <id>` can say WHICH it is; the list
// view collapses the two, the detail view explains them.
func (d UnsettledDetail) IsUndetermined() bool {
	return d.LookupErr != "" || d.BaseErr != "" || d.StatusErr != "" || d.UnlandedErr != ""
}

// Unsettled reports whether this worktree needs the user's attention: it has
// real work outstanding, or its state could not be established.
//
// The undetermined case is checked FIRST and answers true. That widens ◆ from
// "you have work to land" to "look at this task — unlanded or uncheckable",
// which is a deliberate trade (E-1940): a row that renders identically to a
// verified-clean one is the exact failure this predicate exists to remove, and
// marking the row is what says WHICH task is unverifiable without opening the
// fault.
func (d UnsettledDetail) Unsettled() bool {
	if d.IsUndetermined() {
		return true
	}
	if !d.HasWorktree {
		return false
	}
	if len(d.Modified)+len(d.AutoManaged) > 0 {
		return true
	}
	return d.UnlandedCount > 0
}

// IsModified reports the uncommitted-changes sub-state (ED-1540 "modified").
func (d UnsettledDetail) IsModified() bool {
	return len(d.Modified)+len(d.AutoManaged) > 0
}

// IsUnlanded reports the commits-not-in-the-base-branch sub-state (ED-1540
// "unlanded"). False when the count could not be established — that is the
// undetermined state, not the unlanded one.
func (d UnsettledDetail) IsUnlanded() bool {
	return d.UnlandedErr == "" && d.BaseErr == "" && d.UnlandedCount > 0
}

// UndeterminedReason names the probe that could not run and why, or "" when
// every probe ran. The first failure wins: the probes short-circuit, so a later
// one did not get the chance to fail.
func (d UnsettledDetail) UndeterminedReason() string {
	switch {
	case d.LookupErr != "":
		return "worktree lookup failed: " + d.LookupErr
	case d.BaseErr != "":
		return "default branch unresolved: " + d.BaseErr
	case d.StatusErr != "":
		return "git status failed: " + d.StatusErr
	case d.UnlandedErr != "":
		return "unlanded commits could not be counted: " + d.UnlandedErr
	}
	return ""
}

// Reason renders the one-line summary used by the list view: the sub-states that
// are true, or "settled" when none are. Kept here so the Go probe and every
// caller phrase the verdict identically.
func (d UnsettledDetail) Reason() string {
	if !d.HasWorktree && !d.IsUndetermined() {
		return "no worktree"
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
	if d.IsUndetermined() {
		parts = append(parts, "undetermined ("+d.UndeterminedReason()+")")
	}
	if len(parts) == 0 {
		return "settled"
	}
	return strings.Join(parts, " + ")
}

// WorktreeUnsettledAt returns the verdict for a worktree using ONLY the probes
// the ◆ predicate runs. This is the hot path: AnnotateSessionStatusUnsettled
// calls it once per row on every flat `session status` render, so it must not pay
// for detail nobody is going to read.
//
// It reads no database (E-1766, restored by E-2087): everything the verdict
// needs is in the repository, so the probe answers the same way inside a
// self-dev worktree whose sandbox has no task row as it does anywhere else.
func WorktreeUnsettledAt(worktreePath string) UnsettledDetail {
	return worktreeUnsettledAt(worktreePath, false)
}

// WorktreeUnsettledDetailAt returns the verdict PLUS the display enrichment
// (the worktree's current branch) that `task unsettled <id>` renders. One extra
// git call per worktree, so it is reserved for the surfaces that show the
// breakdown — never the per-row marker.
func WorktreeUnsettledDetailAt(worktreePath string) UnsettledDetail {
	return worktreeUnsettledAt(worktreePath, true)
}

// worktreeUnsettledAt is the shared core. It runs exactly the probes the ◆
// predicate runs, in the same order; the enrich flag adds display-only calls
// that never affect the verdict.
func worktreeUnsettledAt(worktreePath string, enrich bool) UnsettledDetail {
	d := UnsettledDetail{
		HasWorktree:  worktreePath != "",
		WorktreePath: worktreePath,
	}
	if !d.HasWorktree {
		return d
	}

	// Probe 1 — uncommitted changes. Partitioned for display only: BOTH halves
	// count toward unsettled, matching the predicate's unfiltered `status` test.
	out, gerr := runGit(worktreePath, "status", "--porcelain")
	if gerr != nil {
		d.StatusErr = firstLine(out, gerr)
		recordProbeFault(d, "git status --porcelain", d.StatusErr)
		return d
	}
	for _, p := range statusPaths(out) {
		if isAutoManagedPath(p) {
			d.AutoManaged = append(d.AutoManaged, p)
		} else {
			d.Modified = append(d.Modified, p)
		}
	}

	// Probe 2 — commits whose content has not reached the base branch. Two
	// corrections over the original `main..HEAD`: the base is resolved rather
	// than hardcoded (E-1940), and the comparison is by content rather than by
	// SHA, so a commit a rebasing land re-hashed is seen to be in (E-2087).
	base, berr := DefaultBranch(worktreePath)
	if berr != nil {
		d.BaseErr = berr.Error()
		recordDefaultBranchFault(d, berr)
		return d
	}
	d.Base = base

	commits, uerr := unlandedCommits(worktreePath, base)
	if uerr != nil {
		d.UnlandedErr = uerr.Error()
		recordProbeFault(d, probeCommand(uerr), d.UnlandedErr)
		return d
	}
	d.UnlandedCount = len(commits)
	// The comparison names the unlanded commits as a side effect of finding
	// them, so the log costs no extra git call and both entry points carry it.
	// Only the sample is bounded; the count above is exact.
	if len(commits) > unlandedLogLimit {
		commits = commits[:unlandedLogLimit]
	}
	d.UnlandedLog = commits

	if !enrich {
		return d
	}

	// Display-only enrichment. A failure here is silent: it must never change
	// the verdict, only the detail rendered under it.
	if br, berr := runGit(worktreePath, "symbolic-ref", "--short", "--quiet", "HEAD"); berr == nil {
		d.Branch = strings.TrimSpace(br)
	}

	return d
}

// recordProbeFault reports a git probe that could not run. Deduped on (worktree,
// probe) so `session monitor`, which re-probes every row every two seconds,
// raises one incident with a rising occurrence count rather than thousands.
func recordProbeFault(d UnsettledDetail, command, detail string) {
	faults.Record(faults.Fault{
		Code:        faults.ErrCodeWorktreeProbeFailed,
		Source:      "worktree:unsettled",
		Fingerprint: d.WorktreePath + "\x00" + command,
		Summary: fmt.Sprintf("%s: %s failed for its worktree",
			worktreeTaskLabel(d.WorktreePath), command),
		Detail: detail,
		Fields: map[string]any{
			"task":     worktreeTaskLabel(d.WorktreePath),
			"worktree": d.WorktreePath,
			"command":  command,
			"stderr":   detail,
		},
	})
}

// recordDefaultBranchFault reports that no default branch could be resolved.
// Fingerprinted on the worktree alone: there is one resolver, so a second code
// path failing on the same directory is the same incident.
func recordDefaultBranchFault(d UnsettledDetail, err error) {
	faults.Record(faults.Fault{
		Code:        faults.ErrCodeDefaultBranchUnresolved,
		Source:      "worktree:unsettled",
		Fingerprint: d.WorktreePath,
		Summary: fmt.Sprintf("%s: the repository's default branch could not be resolved",
			worktreeTaskLabel(d.WorktreePath)),
		Detail: err.Error(),
		Fields: map[string]any{
			"task":     worktreeTaskLabel(d.WorktreePath),
			"worktree": d.WorktreePath,
			"command":  "monitor.DefaultBranch",
			"error":    err.Error(),
		},
	})
}

// worktreeTaskLabel renders the task a worktree belongs to as "E-NNNN", or the
// directory's base name when it does not follow the convention. Every fault
// summary opens with it: the incident list is read to find out WHICH task is
// unverifiable, and a bare path buries that.
func worktreeTaskLabel(worktreePath string) string {
	if m := worktreeDirRe.FindStringSubmatch(filepath.Base(worktreePath)); m != nil {
		return "E-" + m[1]
	}
	return filepath.Base(worktreePath)
}

// TaskWorktreeUnsettledDetail resolves a task's worktree from the DB and returns
// its verdict (no display enrichment — this backs the per-row ◆ marker). A task
// with no worktree yields HasWorktree=false, which Unsettled() reports as
// settled: there is nothing to land.
//
// A lookup that ERRORS is a different answer from one that finds nothing: it
// means the project path could not be resolved, so whether there is unlanded
// work is unknown. That records as LookupErr and reads as undetermined —
// before E-1940 it was indistinguishable from "no worktree, all clear".
func TaskWorktreeUnsettledDetail(projectID, taskID int64) UnsettledDetail {
	wt, err := WorktreePathForTask(projectID, taskID)
	if err != nil {
		d := UnsettledDetail{LookupErr: err.Error()}
		faults.Record(faults.Fault{
			Code: faults.ErrCodeWorktreeProbeFailed,
			// The one fault producer here that KNOWS its project rather than
			// inheriting the process's (E-1960). The lookup that just failed was
			// for this project's task, and the caller is often a machine-wide
			// view running in some other project's directory — so ambient
			// resolution would file the failure under the wrong project, which is
			// worse than filing it under none.
			ProjectID:   projectID,
			Source:      "worktree:unsettled",
			Fingerprint: fmt.Sprintf("task:%d\x00lookup", taskID),
			Summary:     fmt.Sprintf("E-%d: worktree lookup failed", taskID),
			Detail:      err.Error(),
			Fields: map[string]any{
				"task":    fmt.Sprintf("E-%d", taskID),
				"project": projectID,
				"command": "monitor.WorktreePathForTask",
				"error":   err.Error(),
			},
		})
		return d
	}
	if wt == "" {
		return UnsettledDetail{}
	}
	return worktreeUnsettledAt(wt, false)
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
