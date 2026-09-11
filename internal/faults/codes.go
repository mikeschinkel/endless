package faults

import (
	"sort"
)

// Severity ranks a fault for display. The badge on the session-status view
// shows the MAX severity among open incidents, so the ordering here is the
// ordering the user sees.
type Severity string

const (
	// SeverityWarning is a degraded-but-working condition.
	SeverityWarning Severity = "warning"
	// SeverityError is a failure: something the user asked for did not happen.
	SeverityError Severity = "error"
)

// Rank returns the display precedence of a severity, higher being more severe.
// An unrecognized severity ranks lowest so a future value written by a newer
// binary cannot outrank a real error in an older one.
func (s Severity) Rank() (rank int) {
	switch s {
	case SeverityError:
		rank = 2
	case SeverityWarning:
		rank = 1
	}
	return rank
}

// Code is one entry in the error catalog: a stable, documented classification
// of something that can go wrong.
//
// Numbers are flat and unprefixed (ERR-0001, ERR-0002, ...) ON PURPOSE. A
// subsystem prefix (JOB-, HOOK-, DB-) would squat on identifier namespace that
// project-scoped task IDs may want — task IDs are E-NNNN today and may become
// per-project prefixes later. The subsystem is already carried by a fault's
// Source field, so a prefix here would be redundant as well as risky.
//
// Severity is a property of the CODE, never of the call site. Two places
// raising the same condition therefore cannot disagree about whether the user
// sees yellow or red.
type Code struct {
	ID       string   // "ERR-0001" — stable, never reused
	Slug     string   // kebab-case identifier, also the docs/errors.md anchor
	Severity Severity // display severity for every fault carrying this code
	Title    string   // short human-readable classification
}

// The catalog. Every code MUST have a matching section in docs/errors.md; the
// sync is asserted by TestCatalog_MatchesDocs so a new code cannot ship
// undocumented and a documented code cannot go stale.
//
// Numbers are never reused. Retiring a code means deleting it here and from the
// docs, leaving its number permanently spent.
var (
	// ErrCodeJobFailed covers a registered job whose Run returned an error.
	// Warning rather than error: the runner recovers, reschedules, and the job
	// gets another turn, so a single failure is not yet a broken system.
	ErrCodeJobFailed = Code{
		ID:       "ERR-0001",
		Slug:     "job-failed",
		Severity: SeverityWarning,
		Title:    "A background job returned an error",
	}

	// ErrCodeJobPanicked covers a job whose Run panicked. Error severity: a
	// panic is a bug in the job, not an expected failure mode, and without the
	// runner's recover() it would have killed the whole session monitor.
	ErrCodeJobPanicked = Code{
		ID:       "ERR-0002",
		Slug:     "job-panicked",
		Severity: SeverityError,
		Title:    "A background job panicked",
	}

	// ErrCodeJobTimedOut covers a job that outran its lease TTL and had its
	// context cancelled. Error severity: the work did not complete, and Go
	// cannot kill a goroutine that ignores its context, so a leaked goroutine
	// may remain in a long-running monitor process.
	ErrCodeJobTimedOut = Code{
		ID:       "ERR-0003",
		Slug:     "job-timed-out",
		Severity: SeverityError,
		Title:    "A background job exceeded its lease and was cancelled",
	}

	// ErrCodeJobScheduling covers a database failure while claiming, releasing,
	// or upserting a job's scheduling row. Warning: the tick is lost but the
	// next one re-attempts, and no job state is corrupted (every write is a
	// single statement).
	ErrCodeJobScheduling = Code{
		ID:       "ERR-0004",
		Slug:     "job-scheduling",
		Severity: SeverityWarning,
		Title:    "A background job's schedule could not be read or written",
	}

	// ErrCodeJobStuckLease covers a job re-claimed while a previous owner may
	// still be running it, detected when a release finds the lease already
	// taken by someone else. Warning: it means a job overran its LeaseTTL, so
	// the TTL is mistuned or the job is not as fast as declared.
	ErrCodeJobStuckLease = Code{
		ID:       "ERR-0005",
		Slug:     "job-stuck-lease",
		Severity: SeverityWarning,
		Title:    "A background job outran its lease and was re-claimed",
	}

	// ErrCodeTestWarning and ErrCodeTestError are raised only by
	// `endless errors raise` (E-1950). Nothing has gone wrong when one appears.
	//
	// They exist because the badge, the store and the detail log had no way to
	// be exercised without waiting for a real failure — which made the one
	// surface whose whole job is reporting trouble the hardest one to look at.
	// Two codes rather than a --severity flag on one, because severity is a
	// property of the CODE here and a flag would be the first exception to that.
	//
	// The titles say "synthetic" so a raised fault is never mistaken for a real
	// one in `errors show`, in a screenshot, or in a bug report.
	ErrCodeTestWarning = Code{
		ID:       "ERR-0006",
		Slug:     "test-warning",
		Severity: SeverityWarning,
		Title:    "A synthetic warning raised on purpose to exercise this surface",
	}

	// ErrCodeTestError is the error-severity counterpart to ErrCodeTestWarning.
	ErrCodeTestError = Code{
		ID:       "ERR-0007",
		Slug:     "test-error",
		Severity: SeverityError,
		Title:    "A synthetic error raised on purpose to exercise this surface",
	}

	// ErrCodeStatusLineUnavailable covers the tmux status line failing to
	// resolve what it should display. Error severity: the bar renders a dim
	// placeholder that is indistinguishable from "this pane has no Endless
	// context", so without a recorded fault the failure is invisible — which is
	// how the 2026-08-05 incident ran for hours with 59 blank status lines and
	// no diagnostic anywhere (E-1898, absorbing E-1895).
	//
	// ERR-0008, not 0006: E-1950 took 0006/0007 for the synthetic codes above
	// while this branch was in flight, and a spent number is never reused.
	ErrCodeStatusLineUnavailable = Code{
		ID:       "ERR-0008",
		Slug:     "status-line-unavailable",
		Severity: SeverityError,
		Title:    "The tmux status line could not resolve its pane",
	}

	// ErrCodeTriageFailed covers a triage attempt that could not produce a
	// verdict (E-1859): the model call timed out, `claude` was missing, the
	// process exited non-zero, or the reply did not parse.
	//
	// Warning rather than error: triage is fail-open by design, so the task
	// simply stays `untriaged` and the sweep retries — nothing is lost or
	// corrupted. It is recorded at all because the inline path runs DETACHED,
	// where a crash and a considered no-verdict are otherwise
	// indistinguishable and neither is written anywhere. Repeats collapse into
	// one incident with an occurrence count, so a machine with no `claude`
	// installed raises one warning, not one per filing.
	//
	// ERR-0009, not 0008: E-1898 took 0008 for the status-line code while this
	// branch was in flight, and a spent number is never reused.
	ErrCodeTriageFailed = Code{
		ID:       "ERR-0009",
		Slug:     "triage-failed",
		Severity: SeverityWarning,
		Title:    "Triage could not reach a verdict and left the task untriaged",
	}

	// ErrCodeWorktreeProbeFailed covers a git probe behind the ◆ unsettled
	// marker failing: `git status --porcelain` or `git rev-list` returned an
	// error for a task's worktree (E-1940).
	//
	// Error rather than warning: the user asked "is my work safe?" and Endless
	// could not answer. Before this code the answer to an unanswerable probe
	// was the all-clear — byte-identical to a verified-clean worktree — which
	// is the failure the code exists to make visible.
	//
	// Deduped on (worktree, failing probe), so the 2s monitor tick raises one
	// incident with a rising occurrence count rather than thousands.
	ErrCodeWorktreeProbeFailed = Code{
		ID:       "ERR-0010",
		Slug:     "worktree-probe-failed",
		Severity: SeverityError,
		Title:    "A worktree's settled-state probe could not run",
	}

	// ErrCodeDefaultBranchUnresolved covers monitor.DefaultBranch falling
	// through every resolution step (E-1940, absorbing E-1166): no
	// `default_branch` in .endless/config.json, no origin/HEAD, no usable
	// init.defaultBranch, and neither `main` nor `master` present.
	//
	// Error severity because it disables the unsettled probe and the reaper's
	// unmerged-commits condition entirely — every worktree in the project
	// becomes unjudgeable at once, which is a broken installation rather than
	// a degraded one.
	ErrCodeDefaultBranchUnresolved = Code{
		ID:       "ERR-0011",
		Slug:     "default-branch-unresolved",
		Severity: SeverityError,
		Title:    "The repository's default branch could not be resolved",
	}

	// ErrCodeUnlandedCacheUnwritable covers the derived-state cache under the
	// git common dir being unusable — it cannot be created, or an entry cannot
	// be written (E-2128).
	//
	// Warning rather than error, and that is the whole distinction from
	// ERR-0010: every probe still RUNS and every on-demand answer is still
	// exact. What is lost is the ability to remember an answer, so the ◆ column
	// shows `~` (not yet determined) indefinitely and `task unsettled`
	// recomputes from scratch each time. Correct, just not fast.
	//
	// Fingerprinted on the cache DIRECTORY, not on a worktree: one unwritable
	// directory is one condition with one remedy, and a per-worktree
	// fingerprint would raise N incidents about it on every pass.
	ErrCodeUnlandedCacheUnwritable = Code{
		ID:       "ERR-0012",
		Slug:     "unlanded-cache-unwritable",
		Severity: SeverityWarning,
		Title:    "The unlanded-verdict cache cannot be written",
	}
)

// catalog indexes every registered Code by ID. Built once at init from the
// vars above so there is exactly one place a code is declared.
var catalog = buildCatalog(
	ErrCodeJobFailed,
	ErrCodeJobPanicked,
	ErrCodeJobTimedOut,
	ErrCodeJobScheduling,
	ErrCodeJobStuckLease,
	ErrCodeTestWarning,
	ErrCodeTestError,
	ErrCodeStatusLineUnavailable,
	ErrCodeTriageFailed,
	ErrCodeWorktreeProbeFailed,
	ErrCodeDefaultBranchUnresolved,
	ErrCodeUnlandedCacheUnwritable,
)

// buildCatalog indexes codes by ID. It panics on a duplicate ID: a collision is
// a programming error that must never reach a build, and there is no sensible
// runtime recovery from two codes claiming one number.
func buildCatalog(codes ...Code) (m map[string]Code) {
	m = make(map[string]Code, len(codes))
	for _, c := range codes {
		_, dup := m[c.ID]
		if dup {
			panic(ErrDuplicateCode.Error() + ": " + c.ID)
		}
		m[c.ID] = c
	}
	return m
}

// Codes returns every catalog entry, ordered by ID. Used by the docs-sync test
// and by `endless errors codes`.
func Codes() (codes []Code) {
	codes = make([]Code, 0, len(catalog))
	for _, c := range catalog {
		codes = append(codes, c)
	}
	sort.Slice(codes, func(i, j int) bool {
		return codes[i].ID < codes[j].ID
	})
	return codes
}

// LookupCode returns the catalog entry for an ID. ok is false for an unknown
// ID, which happens when an older binary reads a row written by a newer one.
func LookupCode(id string) (code Code, ok bool) {
	code, ok = catalog[id]
	return code, ok
}
