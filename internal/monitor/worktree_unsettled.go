package monitor

import (
	"fmt"
	"path/filepath"
	"strconv"
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
//   - It credits the recorded landing (task_landings.go). A rebasing land
//     rewrites every commit's SHA, so the old `<base>..HEAD` count reported
//     landed work as unlanded forever, and advised `worktree land` — which
//     would replay hundreds of stale commits.

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

	// LandedShas are the recorded landings credited against the branch — the
	// commits reachable from each are excluded from UnlandedCount, because a
	// rebasing land rewrote their SHAs and no git-only probe can see that they
	// are in. Empty means "nothing recorded", not "nothing landed".
	LandedShas []string

	// UnlandedCount is the number of commits on this branch that are neither on
	// Base nor covered by a recorded landing — i.e. work that genuinely has not
	// reached the base branch.
	UnlandedCount int
	// UnlandedLog holds up to unlandedLogLimit "<short-sha> <subject>" lines for
	// display only; UnlandedCount, not len(UnlandedLog), drives the predicate.
	UnlandedLog []string

	// Branch is the worktree's current branch ("" when detached).
	Branch string

	// LookupErr, BaseErr, StatusErr and RevListErr record a probe that could not
	// run: resolving the task's worktree, resolving the default branch, `git
	// status`, and `git rev-list` respectively. Any of them makes the verdict
	// UNDETERMINED, which Unsettled() reports as unsettled — "I could not tell"
	// must never render as "you are clear" (E-1940).
	LookupErr  string
	BaseErr    string
	StatusErr  string
	RevListErr string
}

// unlandedLogLimit caps the commit subjects carried for display. The count is
// always exact; this only bounds the sample rendered under it.
const unlandedLogLimit = 20

// IsUndetermined reports that some probe could not run, so this worktree's
// state is unknown rather than known-clean. Kept distinct from Unsettled() —
// which folds it in — so `task unsettled <id>` can say WHICH it is; the list
// view collapses the two, the detail view explains them.
func (d UnsettledDetail) IsUndetermined() bool {
	return d.LookupErr != "" || d.BaseErr != "" || d.StatusErr != "" || d.RevListErr != ""
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
	return d.RevListErr == "" && d.BaseErr == "" && d.UnlandedCount > 0
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
	case d.RevListErr != "":
		return "git rev-list failed: " + d.RevListErr
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
// The recorded landings are looked up best-effort from the `e-NNNN` directory
// name (landedShasForWorktreePath). That is a softening of E-1766's DB-free
// rule, not a reversal of it: the lookup is never authoritative, every failure
// is silent, and a miss only returns the count to its pre-E-1940 over-reporting
// — so the probe still behaves in a self-dev sandbox that has no task row, it
// just credits nothing there.
func WorktreeUnsettledAt(worktreePath string) UnsettledDetail {
	return worktreeUnsettledAt(worktreePath, landedShasForWorktreePath(worktreePath), false)
}

// WorktreeUnsettledDetailAt returns the verdict PLUS the display enrichment
// (unlanded commit subjects, current branch) that `task unsettled <id>` renders.
// Costs two extra git calls per worktree, so it is reserved for the surfaces
// that actually show the breakdown — never the per-row marker.
func WorktreeUnsettledDetailAt(worktreePath string) UnsettledDetail {
	return worktreeUnsettledAt(worktreePath, landedShasForWorktreePath(worktreePath), true)
}

// worktreeUnsettledAt is the shared core. It runs exactly the probes the ◆
// predicate runs, in the same order; the enrich flag adds display-only calls
// that never affect the verdict. landed carries the recorded landing SHAs to
// credit, which the caller resolves — the git logic here takes no database.
func worktreeUnsettledAt(worktreePath string, landed []string, enrich bool) UnsettledDetail {
	d := UnsettledDetail{
		HasWorktree:  worktreePath != "",
		WorktreePath: worktreePath,
		LandedShas:   landed,
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

	// Probe 2 — commits that have not reached the base branch. Two corrections
	// over the original `main..HEAD` (E-1940): the base is resolved rather than
	// hardcoded, and each recorded landing is excluded so a rebase-rewritten
	// SHA is not mistaken for work that never landed.
	base, berr := DefaultBranch(worktreePath)
	if berr != nil {
		d.BaseErr = berr.Error()
		recordDefaultBranchFault(d, berr)
		return d
	}
	d.Base = base

	out, gerr = runGit(worktreePath, unlandedRevListArgs(base, landed, "--count")...)
	if gerr != nil {
		d.RevListErr = firstLine(out, gerr)
		recordProbeFault(d, "git rev-list", d.RevListErr)
		return d
	}
	n, perr := strconv.Atoi(strings.TrimSpace(out))
	if perr != nil {
		d.RevListErr = "unparsable rev-list count: " + strings.TrimSpace(out)
		recordProbeFault(d, "git rev-list", d.RevListErr)
		return d
	}
	d.UnlandedCount = n

	if !enrich {
		return d
	}

	// Display-only enrichment. Failures here are silent: they must never change
	// the verdict, only the detail rendered under it.
	if d.UnlandedCount > 0 {
		args := append([]string{"log", "--oneline", "--no-decorate", "-n",
			strconv.Itoa(unlandedLogLimit)}, unlandedRangeArgs(base, landed)...)
		if lg, lerr := runGit(worktreePath, args...); lerr == nil {
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

// unlandedRevListArgs builds the `git rev-list` invocation that counts the
// branch's genuinely-unlanded commits. Exported to the reaper too, so the two
// surfaces cannot drift apart on what "unlanded" means.
func unlandedRevListArgs(base string, landed []string, extra ...string) []string {
	args := append([]string{"rev-list"}, extra...)
	return append(args, unlandedRangeArgs(base, landed)...)
}

// unlandedRangeArgs is the revision range itself: everything reachable from
// HEAD, minus the base branch, minus every recorded landing.
//
// `--ignore-missing` covers a landing SHA that is no longer an object here —
// a branch recreated from scratch, or a record-only landing naming a commit
// this clone never had. Without it one absent SHA makes git exit 128 and the
// whole probe fails. It cannot mask a bad BASE: DefaultBranch only ever returns
// a branch it verified resolves to a commit.
func unlandedRangeArgs(base string, landed []string) []string {
	args := []string{"--ignore-missing", "HEAD", "^" + base}
	for _, sha := range landed {
		args = append(args, "^"+sha)
	}
	return args
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
			Code:        faults.ErrCodeWorktreeProbeFailed,
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
	landed, lerr := LandedShasForTask(taskID)
	if lerr != nil {
		// Same softness as the path-based lookup: crediting nothing can only
		// over-report unlanded work, never hide it, so it is not a fault.
		landed = nil
	}
	return worktreeUnsettledAt(wt, landed, false)
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
