package monitor

import (
	"context"
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

// unlandedCommit is one commit on the branch with no counterpart on the base —
// the row this file exists to identify. The SHA is git's abbreviation, which is
// what range-diff prints and what every later git call here accepts.
//
// Structured rather than pre-rendered because E-2095 needs to ASK something of
// each commit (does it touch anything outside Endless's own directory?) before
// deciding whether to report it, and a "<sha> <subject>" string would have to
// be taken apart again to do that.
type unlandedCommit struct {
	SHA     string
	Subject string
}

// String is the display form every surface rendered before there was a struct.
func (c unlandedCommit) String() string {
	if c.Subject == "" {
		return c.SHA
	}
	return c.SHA + " " + c.Subject
}

// renderCommits is String over a slice, for the callers that want display lines.
func renderCommits(commits []unlandedCommit) []string {
	if len(commits) == 0 {
		return nil
	}
	out := make([]string, 0, len(commits))
	for _, c := range commits {
		out = append(out, c.String())
	}
	return out
}

// unlandedCommits returns the worktree's branch commits whose CONTENT has not
// reached base, newest first, each rendered "<short-sha> <subject>".
//
// An empty result means every commit on the branch has a counterpart on base —
// the settled steady state. An error means the comparison could not be made,
// which every caller must treat as undetermined rather than as clean.
//
// This is the worktree-shaped entry point: it asks about whatever HEAD has
// checked out. E-2095's landedness probe asks the same question of a NAMED
// branch in a repository that may have no worktree for it at all, which is why
// the body below takes a rev rather than assuming one.
//
// Since E-2128 this is reached ONLY from the computing paths — the background
// job, `session-query worktree-unsettled`, and the reaper's condition 4. The ◆
// column reads the cache those write and never arrives here, which is the entire
// point of that task. ctx is threaded all the way down so a cancelled job lease
// stops the git children rather than racing them.
func unlandedCommits(ctx context.Context, worktreePath, base string) ([]string, error) {
	commits, err := unlandedRevs(ctx, worktreePath, base, "HEAD")
	if err != nil {
		return nil, err
	}
	return renderCommits(commits), nil
}

// mergeBaseOf resolves the fork point of two revs, or reports why it could not.
func mergeBaseOf(ctx context.Context, dir, base, rev string) (string, error) {
	out, err := runGit(ctx, dir, "merge-base", base, rev)
	if err != nil {
		return "", gitProbeError{Command: "git merge-base", Detail: firstLine(out, err), Err: err}
	}
	mergeBase := strings.TrimSpace(out)
	if mergeBase == "" {
		return "", gitProbeError{
			Command: "git merge-base",
			Detail:  "no common ancestor between " + rev + " and " + base,
		}
	}
	return mergeBase, nil
}

// unlandedRevs is unlandedCommits' body, generalized over the rev being asked
// about and returning the commits themselves.
func unlandedRevs(ctx context.Context, dir, base, rev string) ([]unlandedCommit, error) {
	mergeBase, err := mergeBaseOf(ctx, dir, base, rev)
	if err != nil {
		return nil, err
	}
	return unlandedRevsFrom(ctx, dir, mergeBase, base, rev)
}

// unlandedRevsFrom is the comparison itself, taking the fork point a caller has
// already resolved. Splitting it out is what lets E-2095's probe run its own
// cheap pre-filter against the same merge base instead of computing a second one.
func unlandedRevsFrom(ctx context.Context, dir, mergeBase, base, rev string) ([]unlandedCommit, error) {
	branchRange := mergeBase + ".." + rev
	baseRange := mergeBase + ".." + base

	ahead, err := countRevs(ctx, dir, branchRange)
	if err != nil {
		return nil, err
	}
	if ahead == 0 {
		// The rev is an ancestor of base. Nothing to compare, nothing outstanding.
		return nil, nil
	}

	gained, err := countRevs(ctx, dir, baseRange)
	if err != nil {
		return nil, err
	}
	if gained == 0 {
		// The base has not moved since the fork, so there is nothing for the
		// branch's commits to be matched against and all of them are unlanded.
		// Handled here rather than left to range-diff, which refuses an empty
		// range outright ("fatal: need two commit ranges") instead of reading
		// it as zero counterparts.
		return commitLines(ctx, dir, branchRange)
	}

	out, err := runGit(ctx, dir, "range-diff", "--no-color", "--no-patch",
		branchRange, baseRange)
	if err != nil {
		return nil, gitProbeError{Command: "git range-diff", Detail: firstLine(out, err), Err: err}
	}
	return parseUnlandedRows(out), nil
}

// parseUnlandedRows extracts the left-only commits from range-diff output,
// reversed into newest-first order. range-diff lists a range oldest-first;
// every surface that renders these commits shows history newest-first.
func parseUnlandedRows(out string) []unlandedCommit {
	var rows []unlandedCommit
	for _, ln := range strings.Split(out, "\n") {
		m := rangeDiffLeftOnly.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		rows = append(rows, unlandedCommit{SHA: m[1], Subject: strings.TrimSpace(m[2])})
	}
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows
}

// countRevs counts the commits in a revision range. Trailing `extra` arguments
// are passed to `rev-list` unchanged, which is how E-2095 narrows the same count
// to the commits that touch project source.
func countRevs(ctx context.Context, dir, revRange string, extra ...string) (int, error) {
	args := append([]string{"rev-list", "--count", revRange}, extra...)
	out, err := runGit(ctx, dir, args...)
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

// commitLines reads a revision range as commits, newest first — the same shape
// parseUnlandedRows produces, so the two paths into unlandedRevs are
// indistinguishable downstream.
func commitLines(ctx context.Context, dir, revRange string) ([]unlandedCommit, error) {
	out, err := runGit(ctx, dir, "log", "--format=%h %s", revRange)
	if err != nil {
		return nil, gitProbeError{Command: "git log", Detail: firstLine(out, err), Err: err}
	}
	var rows []unlandedCommit
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		sha, subject, _ := strings.Cut(ln, " ")
		rows = append(rows, unlandedCommit{SHA: sha, Subject: subject})
	}
	return rows, nil
}
