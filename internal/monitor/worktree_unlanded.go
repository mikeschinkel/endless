package monitor

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// E-2087 — "has this branch's work reached the base branch?" is a question
// about CONTENT, not about SHAs.
//
// `worktree land` REBASES the branch onto the base before fast-forwarding, so
// the base receives a copy of every commit under a new hash while the branch
// keeps the original. Any probe that asks "is this SHA reachable from the
// base?" therefore reports those copies as unlanded forever, and the number
// grows with every later land. Measured on this repo when the fix was written:
// 45 worktrees reporting 118 unlanded commits, of which 73 across 30 worktrees
// were real. e-1834 alone reported 11, and `git range-diff` matched every one
// of them to a commit already on main.
//
// Patch-id is not enough either. The rebase is onto a MOVING base, and
// resolving a conflict CHANGES the diff — after which the branch's copy and the
// landed copy have different patch-ids and read as unlanded forever. E-2089
// measured that residue: `git cherry` clears e-1834 but leaves twelve worktrees
// each reporting one phantom commit.
//
// `git range-diff` pairs two commit series by SIMILARITY rather than by exact
// identity, so a conflict-resolved commit is still matched to its landed
// counterpart. Its left-only rows are exactly the branch commits with no
// counterpart on the base, and that is the definition this file implements.
//
// It REPLACES E-1940's landed-SHA credit rather than joining it. That credit
// existed only to paper over the rewritten SHAs; it could only ever hide
// unlanded work, never reveal it; and it made a deliberately DB-free git probe
// read the database (E-1766). Content comparison subsumes it — and neither of
// the two worst offenders above has a task_landings row at all, so it was not
// carrying the load it was added for.
//
// What this deliberately does NOT decide: a branch can hold commits genuinely
// absent from the base and still not be worth landing, because the base moved
// past them by another route. Content comparison correctly calls those
// unlanded; it is the operator who decides they are not worth landing.

// rangeDiffLeftOnly matches range-diff's left-only row — a commit in the first
// range with no counterpart in the second:
//
//	1:  8540eb42 <   -:  -------- Endless: add analysis for E-1944
//
// The other three markers (`=` identical, `!` paired but changed, `>` right
// only) all mean the commit HAS a counterpart, or is not the branch's, so only
// this shape counts. `!` is the conflict-resolved landing this fix exists to
// stop mis-reporting.
var rangeDiffLeftOnly = regexp.MustCompile(`^\s*\d+:\s+([0-9a-f]+)\s+<\s+-:\s+-+\s*(.*)$`)

// gitProbeError names the git command that failed alongside its message, so a
// caller can fingerprint the fault on the specific probe rather than lumping
// every failure of this file under one incident.
type gitProbeError struct {
	Command string
	Detail  string
	// Err is the failure this was built over, when there was one. It exists so a
	// classification made where the subprocess ran — ErrGitInterrupted — survives
	// being carried in this type and stays reachable by errors.Is (E-2113);
	// Detail is a rendered string and drops the chain.
	//
	// Nil for the errors this file raises on its own rather than inheriting from
	// git (no common ancestor, an unparsable count). That is correct: neither is a
	// subprocess failure, so neither can have been interrupted.
	Err error
}

func (e gitProbeError) Error() string { return e.Command + ": " + e.Detail }

// Unwrap exposes the underlying git failure so errors.Is reaches through the
// probe label to the classification beneath it.
func (e gitProbeError) Unwrap() error { return e.Err }

// probeCommand returns the git command an error came from, or a generic label
// when the error is not one of ours. It is the fault's dedup key, so two
// different failing probes on one worktree stay two incidents.
func probeCommand(err error) string {
	var pe gitProbeError
	if errors.As(err, &pe) {
		return pe.Command
	}
	return "unlanded probe"
}

// unlandedCommits returns the worktree's branch commits whose CONTENT has not
// reached base, newest first, each rendered "<short-sha> <subject>".
//
// An empty result means every commit on the branch has a counterpart on base —
// the settled steady state. An error means the comparison could not be made,
// which every caller must treat as undetermined rather than as clean.
func unlandedCommits(worktreePath, base string) ([]string, error) {
	out, err := runGit(worktreePath, "merge-base", base, "HEAD")
	if err != nil {
		return nil, gitProbeError{Command: "git merge-base", Detail: firstLine(out, err), Err: err}
	}
	mergeBase := strings.TrimSpace(out)
	if mergeBase == "" {
		return nil, gitProbeError{
			Command: "git merge-base",
			Detail:  "no common ancestor between HEAD and " + base,
		}
	}

	branchRange := mergeBase + "..HEAD"
	baseRange := mergeBase + ".." + base

	ahead, err := countRevs(worktreePath, branchRange)
	if err != nil {
		return nil, err
	}
	if ahead == 0 {
		// HEAD is an ancestor of base. Nothing to compare, nothing outstanding.
		return nil, nil
	}

	gained, err := countRevs(worktreePath, baseRange)
	if err != nil {
		return nil, err
	}
	if gained == 0 {
		// The base has not moved since the fork, so there is nothing for the
		// branch's commits to be matched against and all of them are unlanded.
		// Handled here rather than left to range-diff, which refuses an empty
		// range outright ("fatal: need two commit ranges") instead of reading
		// it as zero counterparts.
		return commitLines(worktreePath, branchRange)
	}

	out, err = runGit(worktreePath, "range-diff", "--no-color", "--no-patch",
		branchRange, baseRange)
	if err != nil {
		return nil, gitProbeError{Command: "git range-diff", Detail: firstLine(out, err), Err: err}
	}
	return parseUnlandedRows(out), nil
}

// parseUnlandedRows extracts the left-only commits from range-diff output,
// reversed into newest-first order. range-diff lists a range oldest-first;
// every surface that renders these lines shows history newest-first.
func parseUnlandedRows(out string) []string {
	var rows []string
	for _, ln := range strings.Split(out, "\n") {
		m := rangeDiffLeftOnly.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		rows = append(rows, strings.TrimSpace(m[1]+" "+m[2]))
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows
}

// countRevs counts the commits in a revision range.
func countRevs(worktreePath, revRange string) (int, error) {
	out, err := runGit(worktreePath, "rev-list", "--count", revRange)
	if err != nil {
		return 0, gitProbeError{Command: "git rev-list", Detail: firstLine(out, err), Err: err}
	}
	n, perr := strconv.Atoi(strings.TrimSpace(out))
	if perr != nil {
		return 0, gitProbeError{
			Command: "git rev-list",
			Detail:  fmt.Sprintf("unparsable count %q for %s", strings.TrimSpace(out), revRange),
		}
	}
	return n, nil
}

// commitLines renders a revision range as "<short-sha> <subject>" lines,
// newest first — the same shape parseUnlandedRows produces, so the two paths
// into unlandedCommits are indistinguishable downstream.
func commitLines(worktreePath, revRange string) ([]string, error) {
	out, err := runGit(worktreePath, "log", "--format=%h %s", revRange)
	if err != nil {
		return nil, gitProbeError{Command: "git log", Detail: firstLine(out, err), Err: err}
	}
	var rows []string
	for _, ln := range strings.Split(out, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			rows = append(rows, ln)
		}
	}
	return rows, nil
}
