package monitor

import (
	"context"
	"errors"
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
//
// E-2128 split the probe in two along the cost line, which is what ED-1589
// requires of any display that repaints on a timer:
//
//   - The ◆ path (WorktreeUnsettledAt) READS the exact content verdict from the
//     cache one background job writes (unlanded_cache.go) and computes nothing.
//     A miss is its own answer — UnlandedKnown is false and the row renders `~`
//     (not yet determined) — never a verdict derived from something else. That
//     removes a measured 584ms per row per two-second tick.
//   - The on-demand path (WorktreeUnsettledDetailAt) computes on a miss and
//     stores what it found, because a direct question deserves a real answer.
//
// `git status --porcelain` stays LIVE on both paths and is never cached:
// filesystem state changes without any ref moving, so it has no honest cache
// key. It is 15-27ms, three orders of magnitude below the comparison it now sits
// beside.

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

	// UnlandedKnown reports that the unlanded verdict above was ESTABLISHED —
	// read from the cache, computed here, or vacuous because there is no worktree
	// (E-2128). False means nothing has computed it yet, which is distinct from
	// both "zero unlanded" and UnlandedErr's "the probe failed": no probe ran, so
	// there is nothing to report and nobody to blame.
	//
	// It exists because the ◆ path is now cache-only. Collapsing an uncomputed
	// verdict into either ◆ or a blank is the lie ED-1589 was written about — a
	// missing answer used to render byte-identically to a verified-clean worktree.
	UnlandedKnown bool

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

	// Interrupted marks the probe above as KILLED rather than failed — its git
	// child took SIGINT, which is what Ctrl-C in the pane does to every process in
	// the foreground group, `session monitor` and its in-flight probes alike
	// (E-2113). The verdict is unchanged: an interrupted probe established
	// nothing, so this is still undetermined and the row still marks. It changes
	// only what is SAID about it — no incident is recorded, and the reason reads
	// "interrupted" rather than "failed", which on the one surface whose job is
	// explaining the marker was a false statement about a healthy worktree.
	Interrupted bool
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

// UnsettledKnown reports whether Unsettled() is an ANSWER rather than a
// placeholder. The row-level question, as distinct from UnlandedKnown's
// field-level one — and deliberately wider than it, because two cases have a
// known verdict without any cached commit answer:
//
//   - A task with no worktree. There is nothing to land and no git ran, so the
//     row resolves to ⊙ or blank exactly as it always did.
//   - A DIRTY worktree. `git status --porcelain` stays live, so modifications are
//     visible on every tick without the cache; a modified working tree is known
//     to be unsettled whatever the commits have done.
//
// A failed probe also counts as known: it is the undetermined state E-1940
// introduced, which marks the row ◆ and records a fault. "The probe failed" and
// "no probe ran" are different facts with different remedies, and `~` belongs to
// the second alone.
//
// So `~` means specifically: this worktree is CLEAN, and whether its commits
// reached the base has not been computed yet.
func (d UnsettledDetail) UnsettledKnown() bool {
	switch {
	case !d.HasWorktree:
		return true
	case d.IsUndetermined():
		return true
	case d.IsModified():
		return true
	}
	return d.UnlandedKnown
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
		// No subprocess involved, so there is nothing here to have been signalled.
		return "worktree lookup failed: " + d.LookupErr
	case d.BaseErr != "":
		if d.Interrupted {
			return "default branch resolution interrupted"
		}
		return "default branch unresolved: " + d.BaseErr
	case d.StatusErr != "":
		if d.Interrupted {
			return "git status interrupted"
		}
		return "git status failed: " + d.StatusErr
	case d.UnlandedErr != "":
		if d.Interrupted {
			return "unlanded commit count interrupted"
		}
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
	// Said plainly rather than folded into "settled". This can only be reached
	// from the cache-only path, which no surface renders Reason() from today — but
	// a type that would answer "settled" to a question nobody has computed is one
	// wrong call site away from being the E-1940 bug again.
	if !d.UnlandedKnown && !d.IsUndetermined() && d.HasWorktree {
		parts = append(parts, "unlanded state not yet computed")
	}
	if len(parts) == 0 {
		return "settled"
	}
	return strings.Join(parts, " + ")
}

// unlandedMode selects how worktreeUnsettledAt obtains the unlanded verdict.
// It is the ED-1589 boundary expressed in the type system: which mode a caller
// picks is the whole of its licence to be slow.
type unlandedMode int

const (
	// unlandedCacheOnly reads the cache and NEVER computes, so a miss answers
	// "not yet determined". Every repainting display is on this mode.
	unlandedCacheOnly unlandedMode = iota
	// unlandedComputeOnMiss computes the exact comparison when the cache has no
	// answer, and stores what it computed so the next reader gets it free.
	unlandedComputeOnMiss
)

// WorktreeUnsettledAt returns the verdict for a worktree using ONLY the probes
// the ◆ predicate runs, and CACHE-ONLY for the expensive half. This is the hot
// path: AnnotateSessionStatusUnsettled calls it once per row on every flat
// `session status` render, and `session monitor` re-renders every two seconds, so
// it must neither pay for detail nobody is going to read nor compute an answer a
// background job owns (ED-1589).
//
// A worktree whose unlanded verdict is not in the cache comes back with
// UnlandedKnown false and no error: "nothing has computed this" is a third
// answer beside settled and unsettled, and it is the `~` the column renders.
// Nothing here resolves the default branch either — the base NAME comes from the
// cache's own watermark — so the resolver is off the display path entirely.
//
// It reads no database (E-1766, restored by E-2087): everything the verdict
// needs is in the repository, so the probe answers the same way inside a
// self-dev worktree whose sandbox has no task row as it does anywhere else.
func WorktreeUnsettledAt(ctx context.Context, worktreePath string) UnsettledDetail {
	return worktreeUnsettledAt(ctx, worktreePath, unlandedCacheOnly, false)
}

// WorktreeUnsettledDetailAt returns the verdict PLUS the display enrichment
// (the worktree's current branch) that `task unsettled <id>` renders, and
// COMPUTES the unlanded comparison when the cache has no answer. One extra git
// call per worktree for the enrichment and up to a full content comparison for
// the verdict, so it is reserved for the surfaces a person asked — never the
// per-row marker.
func WorktreeUnsettledDetailAt(ctx context.Context, worktreePath string) UnsettledDetail {
	return worktreeUnsettledAt(ctx, worktreePath, unlandedComputeOnMiss, true)
}

// worktreeUnsettledAt is the shared core. It runs exactly the probes the ◆
// predicate runs, in the same order; mode decides whether the unlanded verdict
// may be computed, and the enrich flag adds display-only calls that never affect
// the verdict.
func worktreeUnsettledAt(ctx context.Context, worktreePath string, mode unlandedMode, enrich bool) UnsettledDetail {
	d := UnsettledDetail{
		HasWorktree:  worktreePath != "",
		WorktreePath: worktreePath,
	}
	if !d.HasWorktree {
		// Vacuously known: nothing to land, and no git ran to be uncertain about.
		d.UnlandedKnown = true
		return d
	}

	// Probe 1 — uncommitted changes, LIVE on every path. Partitioned for display
	// only: BOTH halves count toward unsettled, matching the predicate's
	// unfiltered `status` test.
	out, gerr := runGit(ctx, worktreePath, "status", "--porcelain")
	if gerr != nil {
		d.StatusErr = firstLine(out, gerr)
		d.Interrupted = errors.Is(gerr, ErrGitInterrupted)
		recordProbeFault(d, "git status --porcelain", d.StatusErr, gerr)
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
	// SHA, so a commit a rebasing land re-hashed is seen to be in (E-2087). Since
	// E-2128 it is answered from the cache, and only computed when the caller's
	// mode licenses it.
	lookup := cachedUnlanded(ctx, worktreePath)
	d.Base = lookup.Base
	if lookup.Known {
		d.applyUnlanded(lookup.Commits)
		goto enrichment
	}
	if mode == unlandedCacheOnly {
		// Not an error, and deliberately not a fault: no probe ran, so there is
		// nothing to report. The row says so with `~` and the job fills it in.
		goto enrichment
	}
	{
		base, berr := DefaultBranch(ctx, worktreePath)
		if berr != nil {
			d.BaseErr = berr.Error()
			d.Interrupted = errors.Is(berr, ErrGitInterrupted)
			recordDefaultBranchFault(d, berr)
			return d
		}
		d.Base = base

		commits, uerr := computeUnlandedAndCache(ctx, worktreePath, base)
		if uerr != nil {
			d.UnlandedErr = uerr.Error()
			d.Interrupted = errors.Is(uerr, ErrGitInterrupted)
			recordProbeFault(d, probeCommand(uerr), d.UnlandedErr, uerr)
			return d
		}
		d.applyUnlanded(commits)
	}

enrichment:
	if !enrich {
		return d
	}

	// Display-only enrichment. A failure here is silent: it must never change
	// the verdict, only the detail rendered under it.
	if br, berr := runGit(ctx, worktreePath, "symbolic-ref", "--short", "--quiet", "HEAD"); berr == nil {
		d.Branch = strings.TrimSpace(br)
	}

	return d
}

// applyUnlanded records an established unlanded verdict: the exact count, and a
// bounded sample of the commits behind it.
//
// The cap applies HERE and nowhere else, which is what lets the cache store the
// full list (see unlandedCache.writeEntry): the count has to stay exact — the
// detail view says how many more there are than it printed — and a cache that
// stored only the sample would silently cap the count for exactly the worktrees
// that have drifted furthest.
func (d *UnsettledDetail) applyUnlanded(commits []string) {
	d.UnlandedKnown = true
	d.UnlandedCount = len(commits)
	if len(commits) > unlandedLogLimit {
		commits = commits[:unlandedLogLimit]
	}
	d.UnlandedLog = commits
}

// recordProbeFault reports a git probe that could not run. Deduped on (worktree,
// probe) so `session monitor`, which re-probes every row every two seconds,
// raises one incident with a rising occurrence count rather than thousands.
//
// err is the failure behind detail, carried separately because detail is a
// rendered string that has already lost the error chain. The guard lives HERE
// rather than at the call sites so a probe added later cannot forget it
// (E-2113).
func recordProbeFault(d UnsettledDetail, command, detail string, err error) {
	if errors.Is(err, ErrGitInterrupted) {
		// Killed, not failed. The probe said nothing about this worktree, so there
		// is nothing to report — and the process taking the signal is on its way
		// out, so there is nobody left to report it to.
		return
	}
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
	if errors.Is(err, ErrGitInterrupted) {
		// Killed, not failed — see recordProbeFault. Unreachable through
		// DefaultBranch today, which collapses every git error into
		// ErrDefaultBranchUnresolved; the guard is on the recorder rather than the
		// caller so it holds however the resolver's error handling changes.
		return
	}
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
// before E-1940 it was indistinguishable from "no worktree, all clear". It is
// undetermined, NOT "not yet computed": the ◆ still marks the row and the fault
// is still recorded, because a lookup that failed is a failure and `~` is
// reserved for a question nothing has asked yet (E-2128).
func TaskWorktreeUnsettledDetail(ctx context.Context, projectID, taskID int64) UnsettledDetail {
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
		return UnsettledDetail{UnlandedKnown: true}
	}
	return worktreeUnsettledAt(ctx, wt, unlandedCacheOnly, false)
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
