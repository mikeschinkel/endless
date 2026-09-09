package monitor

import (
	"errors"
	"strings"
)

// E-2095 — "has this task's work reached the base branch?"
//
// Task status answers what was DECIDED about a piece of work. It is routinely
// read as "is the code on the default branch", and it answers neither that nor
// the question of whether the record is still authoritative. The measured cost:
// E-1115 sat `assumed` with its fix on an unlanded branch, so the same bug was
// found and fixed a second time fourteen days later as E-1395.
//
// This file answers the landedness half. It is deliberately a SIBLING of
// worktree_unsettled.go rather than a caller of it, because the two ask
// different questions of the same repository:
//
//   - `task unsettled` asks about a WORKTREE: is this directory clean, and is
//     everything it has committed on the base branch? Endless's own bookkeeping
//     commits count there, because they are work the worktree still owes.
//   - this asks about a TASK: did the code this task describes reach the base
//     branch? A branch whose only outstanding commits are Endless's own plan and
//     analysis mirrors has no CODE outstanding, and reporting it as unlanded
//     would bury the handful of real cases under one row per task branch —
//     measured on this repository, 61 branches reporting versus 3 that hold
//     source.
//
// It takes a BRANCH NAME rather than a worktree path, and that is the other
// half of the difference. ED-1587 made a task's branch a pure function of its
// id (`task/<id>`), so a task with no worktree — reaped, or dropped by hand —
// can still be asked about. Nothing here reads `task_landings.branch`; E-2108
// retired that column precisely because the name is now derivable.

// bookkeepingDir is the directory Endless owns inside a tracked project. A
// commit confined to it is Endless's own record-keeping — the plan and analysis
// mirrors, the ledger, the verb cache — not the task's code.
//
// Not a glob list. AutoManagedStatusGlobs is narrower on purpose (it names the
// files `worktree land` auto-commits for you), and the question here is broader:
// whether a commit changed anything the PROJECT is made of. The whole directory
// is the honest boundary, and it holds for any tracked project rather than for
// this repository's layout.
const bookkeepingDir = ".endless"

// Landedness is one task's answer, keyed to the branch it was asked about.
//
// The zero value is not "landed" — it is "no branch, so nothing was measured",
// which callers must render as "never landed", never as "nothing outstanding"
// (E-1940's rule: an answer that could not be obtained must not look like a
// clean one).
type Landedness struct {
	// Branch is the branch this answer is about, derived from the task id.
	Branch string
	// BranchExists reports whether that branch is present in the repository. A
	// false here with no error means the work is unmeasurable, not landed.
	BranchExists bool
	// Base is the resolved default branch the comparison ran against. Empty
	// when resolution failed.
	Base string

	// UnlandedCount is the number of commits on Branch that touch project
	// source and have no counterpart on Base.
	UnlandedCount int
	// UnlandedLog holds up to unlandedLogLimit "<short-sha> <subject>" lines
	// for display. UnlandedCount is exact; this is bounded.
	UnlandedLog []string

	// BaseErr is set when the repository's default branch could not be
	// resolved, and ProbeErr when a git command in the comparison failed.
	// Either one means the landedness of this task is UNKNOWN.
	BaseErr  string
	ProbeErr string
	// Interrupted distinguishes a git child killed by SIGINT — because its
	// parent was — from one that genuinely failed (E-2113). The VERDICT is the
	// same either way: the probe established nothing, so this is still
	// undetermined. What changes is the sentence a surface prints, and that is
	// the whole point: "git range-diff failed" is a false statement about a
	// healthy repository somebody just pressed Ctrl-C in.
	Interrupted bool
}

// Undetermined reports that the comparison could not be made, so this task's
// landedness is unknown rather than known-clean.
func (l Landedness) Undetermined() bool {
	return l.BaseErr != "" || l.ProbeErr != ""
}

// TaskLandedness answers the question for each branch, in the order given.
//
// Batched over one repository because every caller has a list: `task unlanded`
// surveys every finished task in a project, and resolving the default branch
// and enumerating local branches once for the whole batch is the difference
// between two git calls and two per task.
func TaskLandedness(repoRoot string, branches []string) []Landedness {
	out := make([]Landedness, len(branches))
	for i, b := range branches {
		out[i].Branch = b
	}
	if len(branches) == 0 {
		return out
	}

	// The base is RESOLVED, never assumed. Substituting "main" here is the bug
	// DefaultBranch exists to remove, and it fails a repository that named its
	// default branch anything else in the one direction that matters — a false
	// all-clear (E-1940).
	base, berr := DefaultBranch(repoRoot)
	if berr != nil {
		for i := range out {
			out[i].BaseErr = berr.Error()
			out[i].Interrupted = errors.Is(berr, ErrGitInterrupted)
		}
		return out
	}

	existing, eerr := localBranches(repoRoot)
	if eerr != nil {
		for i := range out {
			out[i].Base = base
			out[i].ProbeErr = eerr.Error()
			out[i].Interrupted = errors.Is(eerr, ErrGitInterrupted)
		}
		return out
	}

	for i := range out {
		out[i].Base = base
		if !existing[out[i].Branch] {
			continue
		}
		out[i].BranchExists = true
		commits, err := sourceUnlandedRevs(repoRoot, base, out[i].Branch)
		if err != nil {
			out[i].ProbeErr = err.Error()
			out[i].Interrupted = errors.Is(err, ErrGitInterrupted)
			continue
		}
		out[i].UnlandedCount = len(commits)
		if len(commits) > unlandedLogLimit {
			commits = commits[:unlandedLogLimit]
		}
		out[i].UnlandedLog = renderCommits(commits)
	}
	return out
}

// localBranches is the set of branch names present in the repository. One call
// for the whole batch: asking `rev-parse --verify` per branch would be one
// process per task, and the survey runs over every finished task in a project.
func localBranches(repoRoot string) (map[string]bool, error) {
	out, err := runGit(repoRoot, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, gitProbeError{
			Command: "git for-each-ref", Detail: firstLine(out, err), Err: err,
		}
	}
	set := make(map[string]bool)
	for _, ln := range strings.Split(out, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			set[ln] = true
		}
	}
	return set, nil
}

// sourceUnlandedRevs is unlandedRevs narrowed to commits that touch project
// source, newest first.
//
// Two filters, in cost order, and the cheap one is what makes the survey
// affordable. Measured on this repository: 151 task branches took 110 seconds
// through range-diff alone and 13 with the pre-filter, because 143 of them hold
// nothing but Endless's own commits and can be dismissed by a `rev-list --count`
// that never loads a diff.
//
// The pre-filter cannot hide real work: it counts the commits in the range that
// touch anything outside bookkeepingDir, and the reported commits are a SUBSET
// of that range. Zero there means every commit in the range is bookkeeping, so
// no surviving commit could have been reported anyway.
//
// The post-filter is still required. A branch can hold both kinds — its source
// commit landed and its plan mirror did not — and only the second filter can
// tell which of the commits that came back is which.
func sourceUnlandedRevs(dir, base, rev string) ([]unlandedCommit, error) {
	mergeBase, err := mergeBaseOf(dir, base, rev)
	if err != nil {
		return nil, err
	}

	ahead, err := countSourceRevs(dir, mergeBase+".."+rev)
	if err != nil {
		return nil, err
	}
	if ahead == 0 {
		return nil, nil
	}

	commits, err := unlandedRevsFrom(dir, mergeBase, base, rev)
	if err != nil {
		return nil, err
	}
	return dropBookkeepingCommits(dir, commits)
}

// countSourceRevs counts the commits in a range that change at least one path
// outside bookkeepingDir.
//
// --full-history because the default pathspec simplification is allowed to omit
// commits it considers uninteresting for a path, and this count is load-bearing
// in the direction where an omission would HIDE work.
func countSourceRevs(dir, revRange string) (int, error) {
	return countRevs(dir, revRange, "--full-history", "--", ":(exclude)"+bookkeepingDir)
}

// dropBookkeepingCommits removes the commits that changed nothing outside
// bookkeepingDir, preserving order.
//
// One `git show` for the whole slice rather than one per commit: a long-lived
// branch can carry dozens of unlanded commits, and the caller is already in a
// loop over tasks.
//
// A commit whose file list comes back EMPTY is kept. That is what a merge commit
// looks like to `--name-only` without `-m`, and keeping it errs toward reporting
// work that might be real rather than silently dropping it.
func dropBookkeepingCommits(dir string, commits []unlandedCommit) ([]unlandedCommit, error) {
	if len(commits) == 0 {
		return nil, nil
	}
	args := []string{"show", "--pretty=format:%x00%h", "--name-only", "--no-color"}
	for _, c := range commits {
		args = append(args, c.SHA)
	}
	out, err := runGit(dir, args...)
	if err != nil {
		return nil, gitProbeError{Command: "git show", Detail: firstLine(out, err), Err: err}
	}

	touchesSource := parseShowFileLists(out)
	kept := make([]unlandedCommit, 0, len(commits))
	for _, c := range commits {
		// An unparsed commit is kept for the same reason an empty file list is:
		// this filter may only ever remove commits it positively identified as
		// bookkeeping.
		if touches, ok := touchesSource[c.SHA]; ok && !touches {
			continue
		}
		kept = append(kept, c)
	}
	if len(kept) == 0 {
		return nil, nil
	}
	return kept, nil
}

// parseShowFileLists reads the NUL-delimited `git show` output above into
// "<short-sha> -> does it touch anything outside bookkeepingDir".
func parseShowFileLists(out string) map[string]bool {
	touches := make(map[string]bool)
	for _, block := range strings.Split(out, "\x00") {
		block = strings.TrimLeft(block, "\n")
		if block == "" {
			continue
		}
		sha, rest, _ := strings.Cut(block, "\n")
		sha = strings.TrimSpace(sha)
		if sha == "" {
			continue
		}
		found := false
		empty := true
		for _, path := range strings.Split(rest, "\n") {
			if path = strings.TrimSpace(path); path == "" {
				continue
			}
			empty = false
			if !isBookkeepingPath(path) {
				found = true
				break
			}
		}
		touches[sha] = found || empty
	}
	return touches
}

// isBookkeepingPath reports whether a repo-relative path lives under the
// directory Endless owns. Prefix-matched on the separator so a sibling named
// `.endlessly` is not swallowed.
func isBookkeepingPath(path string) bool {
	return path == bookkeepingDir || strings.HasPrefix(path, bookkeepingDir+"/")
}
