package faults

import (
	"sort"
)

// Severity ranks a fault for display. The fault row on the session-status view
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
// # The prefix states the severity
//
// `WARN-NNNN` for a warning, `ERR-NNNN` for an error (E-2148). Severity is
// already a property of the CODE and never of the call site — two places
// raising the same condition cannot disagree about whether the user sees yellow
// or red — so the id can carry it, and carrying it is what lets every display
// stop spelling the word out. A column of severities beside a column of codes
// says one thing twice; the fault row spent nine columns on " WARNING ".
//
// ERR-0001 was severity warning for a year and a half, which is the defect this
// fixes: an id that states one thing and means another. The prefix is checked
// against the severity by TestCatalog_CodesAreUniqueAndWellFormed, so a code
// cannot be added with the wrong one.
//
// NUMBERS DO NOT MOVE. ERR-0001 became WARN-0001, not WARN-0006: a number is
// spent the moment it ships, and renumbering would make every incident already
// recorded in a user's database, every log line and every bug report cite a code
// that now means something else. Seven of the fourteen changed prefix;
// internal/schema/changes/e-2148-severity-keyed-fault-codes.sql rewrites
// `errors.code` for the rows recorded before the change so they still resolve
// to a catalog entry.
//
// # No subsystem prefix
//
// Still none, and severity is not one. A subsystem prefix (JOB-, HOOK-, DB-)
// would squat on identifier namespace that project-scoped task IDs may want —
// task IDs are E-NNNN today and may become per-project prefixes later — and the
// subsystem is already carried by a fault's Source field. Severity is different
// on both counts: there are exactly two values, neither can collide with a task
// id, and no other field states it.
type Code struct {
	ID       string   // "WARN-0001" / "ERR-0002" — prefix states Severity; never reused
	Slug     string   // kebab-case identifier, also the docs/errors.md anchor
	Severity Severity // display severity for every fault carrying this code
	Title    string   // short human-readable classification

	// Remedy is what to do about a fault carrying this code, printed by
	// `endless errors show <id>` (E-2148).
	//
	// Before it, nothing anywhere told a user how to resolve anything. The
	// listing's footer explained how to DISMISS an incident, and was careful to
	// say dismissing is not a retry — so the one action the surface named was
	// the one that changes nothing. A reader learned that something was wrong
	// and not one thing about fixing it.
	//
	// The text is NOT written here. It is the first paragraph of the code's
	// "What to do" section in docs/errors.md, copied verbatim with its line
	// breaks collapsed — docs/errors.md has carried a remedy per code all
	// along, and this task surfaces that text rather than inventing a second,
	// shorter, drifting version of it. TestCatalog_RemediesMatchTheDocs holds
	// the two byte-identical, so editing either alone fails the build's tests.
	Remedy string
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
		ID:       "WARN-0001",
		Slug:     "job-failed",
		Severity: SeverityWarning,
		Title:    "A background job returned an error",
		Remedy: "Read the detail (`endless errors show <n> --detail`) — it " +
			"carries the error and anything the job logged. If the cause is " +
			"transient, the job will retry on its own cadence. If the job " +
			"declares a `MaxBackoff`, repeated failures push its next attempt " +
			"exponentially further out, up to that cap; fix the cause and run " +
			"`endless jobs retry <name>` rather than waiting the backoff out.",
	}

	// ErrCodeJobPanicked covers a job whose Run panicked. Error severity: a
	// panic is a bug in the job, not an expected failure mode, and without the
	// runner's recover() it would have killed the whole session monitor.
	ErrCodeJobPanicked = Code{
		ID:       "ERR-0002",
		Slug:     "job-panicked",
		Severity: SeverityError,
		Title:    "A background job panicked",
		Remedy: "Treat it as a bug in the job. The stack in the detail log names " +
			"the line. The job's scheduling row is intact and it will be " +
			"retried.",
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
		Remedy: "Either the job is slower than its `Schedule.LeaseTTL` allows, or " +
			"it is wedged. Raise `LeaseTTL` if the work legitimately takes " +
			"that long — the TTL must exceed the job's realistic worst case, " +
			"because once it expires another invocation may claim and run the " +
			"job concurrently.",
	}

	// ErrCodeJobScheduling covers a database failure while claiming, releasing,
	// or upserting a job's scheduling row. Warning: the tick is lost but the
	// next one re-attempts, and no job state is corrupted (every write is a
	// single statement).
	ErrCodeJobScheduling = Code{
		ID:       "WARN-0004",
		Slug:     "job-scheduling",
		Severity: SeverityWarning,
		Title:    "A background job's schedule could not be read or written",
		Remedy: "Check that the database is reachable and that the schema is " +
			"current. A schema version mismatch is raised as ERR-0020 rather " +
			"than here, and a worktree build is refused the main database " +
			"outright (ED-1601), so neither is the cause of this code.",
	}

	// ErrCodeJobStuckLease covers a job re-claimed while a previous owner may
	// still be running it, detected when a release finds the lease already
	// taken by someone else. Warning: it means a job overran its LeaseTTL, so
	// the TTL is mistuned or the job is not as fast as declared.
	ErrCodeJobStuckLease = Code{
		ID:       "WARN-0005",
		Slug:     "job-stuck-lease",
		Severity: SeverityWarning,
		Title:    "A background job outran its lease and was re-claimed",
		Remedy: "Raise the job's `Schedule.LeaseTTL` above its realistic " +
			"worst-case runtime. Also confirm the job is genuinely " +
			"idempotent: the lease is time-boxed rather than an OS lock " +
			"precisely so a dead process needs no cleanup, and the " +
			"unavoidable cost of that design is that a slow job can be " +
			"re-entered.",
	}

	// ErrCodeTestWarning and ErrCodeTestError are raised only by
	// `endless errors raise` (E-1950). Nothing has gone wrong when one appears.
	//
	// They exist because the fault row, the store and the detail log had no way to
	// be exercised without waiting for a real failure — which made the one
	// surface whose whole job is reporting trouble the hardest one to look at.
	// Two codes rather than a --severity flag on one, because severity is a
	// property of the CODE here and a flag would be the first exception to that.
	//
	// The titles say "synthetic" so a raised fault is never mistaken for a real
	// one in a listing, in a screenshot, or in a bug report.
	ErrCodeTestWarning = Code{
		ID:       "WARN-0006",
		Slug:     "test-warning",
		Severity: SeverityWarning,
		Title:    "A synthetic warning raised on purpose to exercise this surface",
		Remedy: "Dismiss it: `endless errors clear <id>`. If you did not raise it " +
			"yourself, someone was testing; it is not a fault report.",
	}

	// ErrCodeTestError is the error-severity counterpart to ErrCodeTestWarning.
	ErrCodeTestError = Code{
		ID:       "ERR-0007",
		Slug:     "test-error",
		Severity: SeverityError,
		Title:    "A synthetic error raised on purpose to exercise this surface",
		Remedy:   "Dismiss it: `endless errors clear <id>`.",
	}

	// ErrCodeStatusLineUnavailable covers the tmux status line failing to
	// resolve what it should display. Error severity: the bar renders a dim
	// placeholder that is indistinguishable from "this pane has no Endless
	// context", so without a recorded fault the failure is invisible — which is
	// how the 2026-08-05 incident ran for hours with 59 blank status lines and
	// no diagnostic anywhere (E-1898, absorbing E-1895).
	//
	// 0008, not 0006: E-1950 took 0006/0007 for the synthetic codes above
	// while this branch was in flight, and a spent number is never reused.
	ErrCodeStatusLineUnavailable = Code{
		ID:       "ERR-0008",
		Slug:     "status-line-unavailable",
		Severity: SeverityError,
		Title:    "The tmux status line could not resolve its pane",
		Remedy: "Read the detail (`endless errors show <n> --detail`); it carries " +
			"the underlying error and the pane id. An enum-integrity failure " +
			"means the binary and the database disagree about an enum mirror; " +
			"`endless db upgrade` reseeds it. A schema version mismatch is " +
			"raised as ERR-0020 instead. If the bar is blank " +
			"with *no* incident recorded, suspect the database itself and " +
			"check `endless sql \"select 1\"`.",
	}

	// ErrCodeTriageFailed covered a triage attempt that could not produce a
	// verdict (E-1859). RETIRED by E-1993, which removed the description triage
	// outright: nothing raises it any more. It stays in the catalog because a
	// spent number is never reused, and because incidents recorded before the
	// removal still carry the code and must keep resolving to an entry.
	ErrCodeTriageFailed = Code{
		ID:       "WARN-0009",
		Slug:     "triage-failed",
		Severity: SeverityWarning,
		Title:    "Triage could not reach a verdict (retired: triage no longer exists)",
		Remedy: "Nothing to fix: the description triage was removed (E-1993) and " +
			"nothing raises this code any more. An incident still carrying it " +
			"predates the removal — dismiss it with `endless errors clear <id>`.",
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
		Remedy: "Read the detail (`endless errors show <n> --detail`); it carries " +
			"the worktree path, the failing git command and its stderr. The " +
			"usual causes are a worktree directory whose git administrative " +
			"file is stale or gone (`git worktree list` disagrees with the " +
			"disk) and a base branch that does not exist locally. Dismiss " +
			"with `endless errors clear <id>`.",
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
		Remedy: "Set the branch explicitly — add `\"default_branch\": " +
			"\"<branch>\"` to the project's `.endless/config.json`, which " +
			"beats every detection step. Or give git the answer it is " +
			"missing: `git remote set-head origin --auto` populates " +
			"`origin/HEAD` for a clone that never had it. Dismiss with " +
			"`endless errors clear <id>`.",
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
		ID:       "WARN-0012",
		Slug:     "unlanded-cache-unwritable",
		Severity: SeverityWarning,
		Title:    "The unlanded-verdict cache cannot be written",
		Remedy: "Read the detail (`endless errors show <n> --detail`); it names " +
			"the directory and the filesystem error. The usual causes are a " +
			"checkout on read-only media, a `.git` directory owned by another " +
			"user, and a full disk. `git rev-parse --path-format=absolute " +
			"--git-common-dir` from inside the repository prints the parent " +
			"the cache wants to live under. Nothing needs repairing " +
			"afterwards — the cache is rebuildable derived state, and the " +
			"next job pass refills it. Dismiss with `endless errors clear " +
			"<id>`.",
	}

	// ErrCodeTurnFailedTransient and ErrCodeTurnFailedFatal both cover a Claude
	// Code turn that ended on an API error — the `StopFailure` event (E-2145),
	// which the harness fires INSTEAD OF `Stop` when a turn dies.
	//
	// TWO codes rather than one code whose severity depends on the payload,
	// because severity is a property of the CODE here (see the Code doc above)
	// and a per-occurrence severity would be the first exception to that. The
	// split is by whether the failure can resolve itself, which is also what
	// decides which one deserves the single line the fault row renders.
	//
	// Transient: `rate_limit`, `overloaded`, `server_error`, `max_output_tokens`,
	// and every error type not named in the fatal set — including ones a future
	// Claude Code adds. Warning is the forgiving direction for an unrecognised
	// value: an unknown failure is more likely to be a passing one than a fatal
	// one, and over-reporting it as red would outrank real errors for the fault
	// row's one line.
	ErrCodeTurnFailedTransient = Code{
		ID:       "WARN-0013",
		Slug:     "turn-failed-transient",
		Severity: SeverityWarning,
		Title:    "A turn ended on an API error that should pass on its own",
		Remedy: "Usually nothing but take the turn again. Read the detail " +
			"(`endless errors show <n> --detail`) for the session, the task " +
			"and the error type of every occurrence. A high occurrence count " +
			"on `max_output_tokens` is worth acting on — it means turns are " +
			"routinely being cut off mid-reply. Dismiss with `endless errors " +
			"clear <id>`.",
	}

	// ErrCodeTurnFailedFatal is the needs-a-person half: `authentication_failed`,
	// `billing_error`, `oauth_org_not_allowed` and `account_on_hold`. Nothing
	// self-heals — every one of them is a fact about the account that retrying
	// cannot change — so they outrank a transient failure for the fault row.
	ErrCodeTurnFailedFatal = Code{
		ID:       "ERR-0014",
		Slug:     "turn-failed-fatal",
		Severity: SeverityError,
		Title:    "A turn ended on an API error that will not clear itself",
		Remedy: "Fix the account condition the error type names — re-authenticate " +
			"(`claude` will prompt), settle billing, or ask whoever " +
			"administers the organisation about an org policy or a hold. " +
			"`endless errors show <n> --detail` names the error type, the " +
			"session and the task for every occurrence. Dismiss with `endless " +
			"errors clear <id>` once it is sorted.",
	}

	// The four hook codes (E-1887). A Claude hook that fails reports to NOBODY:
	// hook.Run log.Printf's the error and exits with a code chosen to keep a
	// broken hook from blocking a tool call, and Claude Code discards hook
	// stderr. Session ES-1055 ran that way for four weeks — every PreToolUse
	// died inside monitor.TouchSession with "table sessions has no column named
	// process", `sessions.process_id` stayed NULL, and the only symptom was a
	// blank tmux status bar, which is also what a pane with no task looks like.
	//
	// # Why four codes and not one
	//
	// They are recorded at ONE site — the error sink in hook.Run — so that a
	// handler added later is covered without anyone remembering to. But a
	// single code for everything that sink catches would put a malformed
	// harness payload and a schema-drifted database under one title, and a
	// reader whose report says only "a hook failed" has learned nothing they
	// can act on. So the error is CLASSIFIED where it is raised (see
	// internal/hookcmd/faultclass.go) and the sink records whichever code the
	// classification names. Nothing is sniffed out of an error string.
	//
	// ErrCodeHookWriteFailed is the one the incident above would have raised:
	// the hook could not WRITE the session or activity row it exists to write.
	// Error severity, and the most consequential of the four — the write not
	// happening leaves state that every later read believes, so the failure is
	// silently load-bearing rather than merely lost.
	ErrCodeHookWriteFailed = Code{
		ID:       "ERR-0015",
		Slug:     "hook-write-failed",
		Severity: SeverityError,
		Title:    "A Claude hook could not write to the database",
		Remedy: "Read the detail (`endless errors show <n> --detail`); it names " +
			"the hook event, the session, the pane and the binary that ran. A " +
			"\"no such column\" or enum-integrity failure means that binary " +
			"and the database disagree about the schema, repaired by bringing " +
			"the binary up to the database — `just install` from the main " +
			"checkout. It names the binary because a worktree used to pin its " +
			"hooks at its own `bin/endless-go`, which a schema change left " +
			"behind; E-2166 removed that pin, so a hook now runs the installed " +
			"one. Until it is fixed the session's `process_id` stays NULL, so " +
			"the tmux status line renders the same hint a pane with no Endless " +
			"session gets.",
	}

	// ErrCodeHookReadFailed covers a hook that could not READ what it needed —
	// the project lookup for its cwd, the activity throttle, the active-task
	// query.
	//
	// Error severity, same as the write, but the consequence is narrower and
	// worth telling apart: the hook returns before it touches anything, so it
	// did no work for that event rather than leaving a wrong answer behind.
	// The next event retries from scratch.
	ErrCodeHookReadFailed = Code{
		ID:       "ERR-0016",
		Slug:     "hook-read-failed",
		Severity: SeverityError,
		Title:    "A Claude hook could not read from the database",
		Remedy: "Read the detail (`endless errors show <n> --detail`); it names " +
			"the hook event, the session and the binary that ran. The causes " +
			"are the same ones behind ERR-0015 — a binary and a database that " +
			"disagree about the schema — but the consequence is narrower: the " +
			"hook did no work for that event rather than leaving wrong state " +
			"behind, and the next event retries from scratch.",
	}

	// ErrCodeHookPayloadUnreadable covers stdin, not the database: the harness
	// sent the hook nothing, or sent something that is not the JSON event
	// envelope.
	//
	// Separated from the two above because the remedy has nothing in common
	// with theirs. No amount of rebuilding a binary fixes a hook whose stdin is
	// being piped through another command by a `settings.json` entry, and a
	// reader sent to look at schema drift for it is being sent the wrong way.
	ErrCodeHookPayloadUnreadable = Code{
		ID:       "ERR-0017",
		Slug:     "hook-payload-unreadable",
		Severity: SeverityError,
		Title:    "A Claude hook could not read the event the harness sent it",
		Remedy: "This one is not a database problem. Claude Code either sent the " +
			"hook nothing on stdin or sent something that is not the JSON " +
			"event envelope, so check how the hook is wired in " +
			"`settings.json` — an entry that pipes the hook's input through " +
			"another command is the usual cause. The detail (`endless errors " +
			"show <n> --detail`) carries what was received.",
	}

	// ErrCodeHookFailed is the catch-all, and it exists for exactly one reason:
	// the sink must never be silent about an error it was not taught to
	// classify. A handler added next year raises this until someone decides it
	// deserves a code of its own.
	//
	// It is the LEAST useful of the four by construction, which is the argument
	// for keeping the other three rather than collapsing them into it. Its
	// detail line still carries the full error text, so even here a reader can
	// name the operation that failed.
	//
	// Today the unclassified paths are cwd resolution and worktree adoption.
	ErrCodeHookFailed = Code{
		ID:       "ERR-0018",
		Slug:     "hook-failed",
		Severity: SeverityError,
		Title:    "A Claude hook failed",
		Remedy: "Read the detail (`endless errors show <n> --detail`): it carries " +
			"the full error text, the hook event, the session and the binary " +
			"that ran, and the error text names the operation that failed. " +
			"This code is the one raised when no more specific hook code " +
			"applies, so a run of them against a single operation is a sign " +
			"that operation has earned a code of its own.",
	}
	// ErrCodeMonitorRestartFailed covers a live view — `session monitor`,
	// `project monitor` — whose binary was replaced by an install, but which
	// could not restart onto the replacement (E-2193): the new binary failed
	// its `--help` probe, or the exec itself failed.
	//
	// Warning rather than error: the view keeps rendering with the old binary,
	// so nothing the user is looking at has broken. What it gives up is job
	// firing, for the rest of that process's life, because a stale binary must
	// never again run a job the new install may have retired — which is how
	// E-1993's removed triage job kept raising WARN-0001 from monitors started
	// before the land.
	//
	// Fingerprinted on the replacement's resolved path, so every monitor that
	// trips over one bad install raises a single incident.
	ErrCodeMonitorRestartFailed = Code{
		ID:       "WARN-0019",
		Slug:     "monitor-restart-failed",
		Severity: SeverityWarning,
		Title:    "A monitor could not restart onto its replaced binary",
		Remedy: "Read the detail (`endless errors show <n> --detail`); it names " +
			"the stage that failed — `probe` (the new binary could not even " +
			"print its usage) or `exec` — and the error. Fix or rebuild the " +
			"install; a monitor retries on its own whenever the binary changes " +
			"again, so a good build usually clears it without a restart. A " +
			"monitor that still shows the notice has stopped firing background " +
			"jobs: restart it — `endless session monitor --restart` does every " +
			"session monitor in the tmux session at once; a project monitor is " +
			"quit and started again by hand. Dismiss with `endless errors " +
			"clear <id>`.",
	}

	// ErrCodeSchemaVersionRefused covers monitor.DB() refusing a database on
	// schema grounds (E-2020): a worktree-built binary aimed at the main
	// database (ED-1601), a database AHEAD of the binary (ED-1570), or a forward
	// migration that did not complete.
	//
	// Raised by the surfaces that render that refusal SILENTLY — the Claude hook,
	// the tmux status line, the background jobs — because they fire on every
	// event for nobody, and an error per event is the fifty-identical-lines
	// experience of the 2026-08-10 land. Fingerprinted on the refusal's summary,
	// which carries the kind and both versions and nothing per-event, so the
	// whole machine's worth of hooks during one land window is ONE incident.
	ErrCodeSchemaVersionRefused = Code{
		ID:       "ERR-0020",
		Slug:     "schema-version-refused",
		Severity: SeverityError,
		Title:    "endless-go refused the database's schema version",
		Remedy: "Read the summary: it names which of three cases this is. " +
			"`database is at schema vN, endless-go carries vM` means the binary " +
			"is older than the database — upgrade endless (in a self-dev " +
			"checkout, `just build` in the main checkout); a self-dev land clears " +
			"the one its own migration causes, so one that stays open is real. " +
			"`migrating the " +
			"database ... did not complete` means a forward migration failed — " +
			"run `endless db upgrade`, which backs up first. `a worktree-built " +
			"endless-go refused the main database` means something ran a " +
			"worktree's binary against main; use the installed binary. Dismiss " +
			"with `endless errors clear <id>` once the cause is gone.",
	}

	// ErrCodeOutputStyleInactive covers the Endless output style is installed but not active.
	//
	// Warning, not error: nothing is blocked and nothing a command was asked to
	// do failed. It is here rather than on stderr because the only reader who
	// can act on it is the user (E-2159 decision 5), and an agent shown it
	// spends a message on something it cannot change.
	ErrCodeOutputStyleInactive = Code{
		ID:       "WARN-0021",
		Slug:     "output-style-inactive",
		Severity: SeverityWarning,
		Title:    "The Endless output style is installed but not active",
		Remedy: "Activate it when you want it in effect — `/config " +
			"output-style=endless` in a Claude session, or re-run the " +
			"install with `--activate`. Leave it inactive and dismiss this " +
			"with `endless errors clear <id>` if you installed the style " +
			"without meaning to switch to it.",
	}

	// ErrCodeWorktreeTTLUnreadable covers a project's worktree_ttl could not be read, so the default applies.
	//
	// Warning, not error: nothing is blocked and nothing a command was asked to
	// do failed. It is here rather than on stderr because the only reader who
	// can act on it is the user (E-2159 decision 5), and an agent shown it
	// spends a message on something it cannot change.
	ErrCodeWorktreeTTLUnreadable = Code{
		ID:       "WARN-0022",
		Slug:     "worktree-ttl-unreadable",
		Severity: SeverityWarning,
		Title:    "A project's worktree_ttl could not be read, so the default applies",
		Remedy: "Fix `worktree_ttl` in the project's `.endless/config.json` — " +
			"it takes a Go duration (`336h`) or a day count (`14d`). Until " +
			"then every sweep uses the default, which is the same answer it " +
			"gave before the value was added. Dismiss with `endless errors " +
			"clear <id>` once the value parses.",
	}

	// ErrCodeUnsupportedHarness covers endless ran under an agent harness it does not support.
	//
	// Warning, not error: nothing is blocked and nothing a command was asked to
	// do failed. It is here rather than on stderr because the only reader who
	// can act on it is the user (E-2159 decision 5), and an agent shown it
	// spends a message on something it cannot change.
	ErrCodeUnsupportedHarness = Code{
		ID:       "WARN-0023",
		Slug:     "unsupported-harness",
		Severity: SeverityWarning,
		Title:    "Endless ran under an agent harness it does not support",
		Remedy: "Run Endless from Claude Code in a terminal if you want it to " +
			"work as documented; its hooks do not fire on other harnesses. " +
			"Nothing is wrong with the install. Dismiss with `endless " +
			"errors clear <id>` — the agent has already been told to ignore " +
			"Endless for that session.",
	}

	// ErrCodeReportUnminimized covers a reply was sent unminimized after the minimizer broke an invariant.
	//
	// Warning, not error: nothing is blocked and nothing a command was asked to
	// do failed. It is here rather than on stderr because the only reader who
	// can act on it is the user (E-2159 decision 5), and an agent shown it
	// spends a message on something it cannot change.
	ErrCodeReportUnminimized = Code{
		ID:       "WARN-0024",
		Slug:     "report-unminimized",
		Severity: SeverityWarning,
		Title:    "A reply was sent unminimized after the minimizer broke an invariant",
		Remedy: "Nothing, for the reply — it was sent, and intact. The signal " +
			"is the minimizer itself: read the detail (`endless errors show " +
			"<n> --detail`) for which invariant broke, and treat a rising " +
			"occurrence count as a bug in the minimizer rather than in the " +
			"drafts. Dismiss with `endless errors clear <id>`.",
	}

	// ErrCodeSigilSynonym covers a new sigil closely resembles one the corpus already has.
	//
	// Warning, not error: nothing is blocked and nothing a command was asked to
	// do failed. It is here rather than on stderr because the only reader who
	// can act on it is the user (E-2159 decision 5), and an agent shown it
	// spends a message on something it cannot change.
	ErrCodeSigilSynonym = Code{
		ID:       "WARN-0025",
		Slug:     "sigil-synonym",
		Severity: SeverityWarning,
		Title:    "A new sigil closely resembles one the corpus already has",
		Remedy: "Decide whether the two spellings mean the same thing. If they " +
			"do, pick the one you want as canonical and use it from then on " +
			"— the other keeps working, so nothing breaks while you switch. " +
			"If they are genuinely different labels, nothing needs doing. " +
			"Dismiss with `endless errors clear <id>` either way.",
	}
	// ErrCodeCreateHookNotExecutable covers the project's post-worktree-create hook is not executable, so it was skipped.
	//
	// Warning, not error: the worktree was created and is usable; only its
	// project-specific bootstrap did not run. It is here as well as on stderr
	// because only the user can make the hook executable — the file is tracked
	// on main — and an agent shown it spends a message on something it cannot
	// change (E-2213).
	ErrCodeCreateHookNotExecutable = Code{
		ID:       "WARN-0026",
		Slug:     "create-hook-not-executable",
		Severity: SeverityWarning,
		Title:    "The project's post-worktree-create hook is not executable, so it was skipped",
		Remedy: "Make the hook executable on main — `chmod +x " +
			".endless/hooks/post-worktree-create.sh`, then commit the mode " +
			"change — so later worktrees run it. A worktree created while it " +
			"was skipped is usable but not bootstrapped; finish it with `sh " +
			".endless/hooks/post-worktree-create.sh <worktree>`. Dismiss with " +
			"`endless errors clear <id>` once the hook runs.",
	}

	// ErrCodePostLandNotExecutable covers a task's post-land script is not executable, so it was skipped.
	//
	// Warning, not error: the land itself succeeded and cannot be unwound. It
	// is here as well as on stderr because the script is now on main, so
	// making it executable for good needs a commit only the user lands, and
	// the one-time step it carries still has to happen (E-2213).
	ErrCodePostLandNotExecutable = Code{
		ID:       "WARN-0027",
		Slug:     "post-land-not-executable",
		Severity: SeverityWarning,
		Title:    "A task's post-land script is not executable, so it was skipped",
		Remedy: "Run the skipped step yourself from the main checkout — `sh " +
			".endless/hooks/post-land/<task>.sh <main checkout>`; the script " +
			"is required to be idempotent, so running it late is safe, and " +
			"the incident's summary names the script. Commit future post-land scripts " +
			"executable (`git update-index --chmod=+x <script>`). Dismiss with " +
			"`endless errors clear <id>` once it has run.",
	}

	// ErrCodeStaleCompanion covers a worktree's companion names a different task than its path.
	//
	// Warning, not error: the path-derived task wins and every command answers
	// correctly. It is here as well as on stderr because the disagreement is a
	// standing condition of that worktree — it repeats on every command that
	// reads the companion until someone edits the file — and the reader of any
	// one command is not the one who owns it (E-2213).
	ErrCodeStaleCompanion = Code{
		ID:       "WARN-0028",
		Slug:     "stale-companion",
		Severity: SeverityWarning,
		Title:    "A worktree's companion names a different task than its path",
		Remedy: "Remove the legacy `task_id` key from the worktree's " +
			"`.endless/worktree.json` (the incident's summary names it); nothing " +
			"reads it any more, and the path-derived task is already the one " +
			"in use. Dismiss with `endless errors clear <id>` once the key is " +
			"gone.",
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
	ErrCodeTurnFailedTransient,
	ErrCodeTurnFailedFatal,
	ErrCodeHookWriteFailed,
	ErrCodeHookReadFailed,
	ErrCodeHookPayloadUnreadable,
	ErrCodeHookFailed,
	ErrCodeMonitorRestartFailed,
	ErrCodeSchemaVersionRefused,
	ErrCodeOutputStyleInactive,
	ErrCodeWorktreeTTLUnreadable,
	ErrCodeUnsupportedHarness,
	ErrCodeReportUnminimized,
	ErrCodeSigilSynonym,
	ErrCodeCreateHookNotExecutable,
	ErrCodePostLandNotExecutable,
	ErrCodeStaleCompanion,
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
