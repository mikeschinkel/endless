# Plan — an interrupted probe is not a failed probe

## The decision

A git child that died on **SIGINT** is not evidence about the worktree. The
probe stops recording ERR-0010 for it. Everything else about the verdict is
unchanged.

Three calls, all decided:

- **SIGINT only.** SIGTERM, SIGHUP, SIGKILL and every other signal keep
  recording. We have observed exactly one benign scenario — Ctrl-C in a pane
  reaching the git child through the shared foreground process group — so that
  is the one guarded. A SIGKILL is usually the OOM killer, and a `range-diff`
  big enough to be OOM-killed is a real operational fact. Widening the set later
  is a one-line change to `killedBySIGINT`; narrowing it after silence has hidden
  something is not.
- **Silent, but still `◆`.** No fault recorded; the verdict stays UNDETERMINED,
  so the row still marks and nothing is ever reported clean. E-1940's actual
  invariant — "a worktree nobody could inspect must not render as verified
  clean" — is preserved intact. What is removed is only the persistent incident.
- **Classification only.** No `context.Context`, no cancellation. See
  *Not in scope*.

## Why the classification lives in `runGit`

`runGit` is the single place every probe's git subprocess is created, so
classifying there means no caller can forget, and the resulting error carries
its own meaning to any depth via `errors.Is`. The alternative — a predicate
applied at each fault-recording site — spreads the same decision across four
call sites and grows a fifth the next time someone adds a probe.

`runGit` is a swappable `var` (`SetRunGitForTest`), so a stub bypasses the
classification. That is deliberate and useful: a stub returns an already-
classified error to exercise the guard, while `killedBySIGINT` is tested
directly against a real signalled process.

## What changes

### 1. Classify at the source

`internal/monitor/reap_worktrees.go`:

```go
// ErrGitInterrupted marks a git child that died on SIGINT rather than failing.
// A probe that was interrupted has said nothing about the worktree, so it is
// undetermined like any other unrunnable probe — but it is NOT an incident.
var ErrGitInterrupted = errors.New("git interrupted")

// killedBySIGINT reports that a child died on SIGINT specifically.
//
// Narrow on purpose (E-2113): SIGINT is the one signal observed reaching a
// probe benignly — Ctrl-C goes to the terminal's whole foreground process
// group, and monitor.runGit uses a plain exec.Command, so the git child is in
// it. SIGTERM/SIGHUP/SIGKILL still record; an OOM-killed probe is worth knowing
// about.
//
// On a platform whose ProcessState.Sys() is not a syscall.WaitStatus the type
// assertion fails and this returns false — the fault is recorded, which is
// today's behaviour and the safe direction.
func killedBySIGINT(err error) bool
```

`runGit` wraps: `err = fmt.Errorf("%w: %v", ErrGitInterrupted, err)`.

### 2. Let the classification survive wrapping

`gitProbeError` (`worktree_unlanded.go`) stores `Detail string` and drops the
error chain, so `errors.Is` cannot reach through it. Add an `Err error` field
and `func (e gitProbeError) Unwrap() error { return e.Err }`, populated at the
three construction sites that wrap a real git failure (`merge-base`,
`rev-list` in `countRevs`, `range-diff`). The two synthetic ones — "no common
ancestor", "unparsable count" — leave it nil, which is correct: neither is a
subprocess failure.

### 3. Guard inside the recorders, not at their call sites

A guard at the call site is a rule each caller must remember. Put it in the
three functions that record a git-derived fault, so it cannot be forgotten:

- `recordProbeFault` — gains an `err error` parameter alongside the existing
  string detail, and returns early on `errors.Is(err, ErrGitInterrupted)`.
- `recordDefaultBranchFault` — already takes the error; add the check.
- `recordReapDefaultBranchFault` (`reap_worktrees.go`) — same.

The fourth `faults.Record` in the package (the worktree-lookup fault) is a
database error, not a subprocess, and is untouched.

### 4. Say "interrupted", not "failed"

`UndeterminedReason()` currently renders `git status failed: signal: interrupt`
— which is precisely the false statement this task exists to remove, on the one
surface (`task unsettled <id>`) whose whole job is explaining the marker.

`UnsettledDetail` gains `Interrupted bool`, set alongside the existing
`StatusErr`/`UnlandedErr`/`BaseErr`. `UndeterminedReason()` reads
`"<probe> interrupted"` when it is set.

**This is the one discretionary item in the plan.** It adds a field for a
wording fix and can be dropped without affecting the defect. It is included
because "failed" is the lie, not just the incident.

## What deliberately does not change

- The verdict. `IsUndetermined()` stays true, `Unsettled()` stays true, the row
  still reads `◆`.
- Any other signal. SIGTERM/SIGHUP/SIGKILL record exactly as today.
- The reaper's safety direction: an interrupted probe still refuses to reap.

## Not in scope

- **Cancellation.** Threading a `context.Context` so an interrupt cancels the
  sweep instead of racing it would touch `runGit`, `AnnotateSessionStatusUnsettled`,
  `WorktreeUnsettledAt`, `TaskWorktreeUnsettledDetail`, `unlandedCommits`,
  `countRevs` and `DefaultBranch`, plus wiring `liveview`'s existing signal
  handler to a cancel func. It is a different change with a different risk
  profile, and E-2111 is underway on the same files.
- **E-2111's cache.** That work removes the git subprocess from the display path
  and so shrinks this window enormously — but does not close it: E-2111's plan
  says so itself, since its job runs on a goroutine inside the monitor process.
  The two are independent and either may land first.
- **`DefaultBranch` memoizing a failure.** `defaultBranchCache` stores errors as
  well as answers, so an interrupted resolve poisons the memo for the process
  lifetime. Harmless here (the process took SIGINT and is exiting) and
  pre-existing. Noted, not fixed.

## Verification

A new `internal/monitor/git_interrupt_test.go`:

- `killedBySIGINT` against **real** signalled processes — `sh -c 'kill -INT $$'`
  is true; `-TERM`, `-KILL` and `sh -c 'exit 3'` are false; a nil error and a
  non-`*exec.ExitError` are false. Real processes rather than a fabricated
  `os.ProcessState`, which is not constructible.
- The chain survives wrapping: a `gitProbeError` built over an interrupted
  error satisfies `errors.Is(err, ErrGitInterrupted)`, and one built over an
  ordinary failure does not.
- The guard, via a new `var recordFault = faults.Record` seam in
  `internal/monitor` — the same swappable-var idiom `runGit` and
  `hasLiveProcessInDir` already use in this package, and necessary because
  `faults.Record` writes straight to a database. With `runGit` stubbed to return
  a real interrupted error: **no fault recorded**, `IsUndetermined()` true,
  `Unsettled()` true. The contrast case — an ordinary git failure — records
  exactly one.
- `UndeterminedReason()` says "interrupted" for the interrupted case and keeps
  its existing wording for every other.

Regression: `go test ./internal/...`, `just test`, `go vet ./...`.

## Files touched

| File | Change |
|---|---|
| `internal/monitor/reap_worktrees.go` | `ErrGitInterrupted`, `killedBySIGINT`, `runGit` wrap, `recordFault` seam, `recordReapDefaultBranchFault` guard |
| `internal/monitor/worktree_unlanded.go` | `gitProbeError.Err` + `Unwrap`, populated at three sites |
| `internal/monitor/worktree_unsettled.go` | `recordProbeFault`/`recordDefaultBranchFault` guards, `Interrupted` field, `UndeterminedReason` |
| `internal/monitor/git_interrupt_test.go` | new |



## As built — two departures from the letter of this plan

Both are recorded here because a reviewer reading the plan against the diff
would otherwise have to work out whether they were deliberate.

### A fourth `gitProbeError` site, not three

The plan states the rule — populate `Err` at "the construction sites that wrap
a real git failure" — and then enumerates three (`merge-base`, `rev-list`,
`range-diff`). There are four: `commitLines`'s `git log`, taken when the base
branch has not moved since the fork, wraps a real git failure too. It was
populated along with the other three. Leaving it out would have left one probe
structurally unable to be recognised as interrupted, by the plan's own rule.

The two synthetic errors ("no common ancestor", "unparsable count") leave `Err`
nil exactly as planned.

### No `recordFault` seam

The plan proposed `var recordFault = faults.Record` so a test could observe
whether a fault was recorded. `internal/monitor` already has
`bindFaultsForTest` (worktree_unsettled_faults_test.go), which points the
faults package at a fresh in-memory store — E-1940's own dedup tests use it.
The new tests use that instead, which asserts against the REAL recorder and its
real persistence rather than a stub, and adds no production surface.

### Verification, as built

`.endless/tasks/e-2113/verify.sh` — 25 assertions. Every signal case drives a
REAL child (`os.ProcessState` is not constructible), and the end-to-end
sections build `cmd/endless-go` from the tree and run it against a scratch repo
with a `git` shim that interrupts, terminates, or fails on demand.

Each of the three guards was mutation-tested: removing the `recordProbeFault`
guard, the `Unwrap`, or the `runGit` wrap each turns the suite red, and the
first reproduces the reported incident verbatim — `E-1972: git range-diff
failed for its worktree`.

### Still not in scope, and now with a reason to state

`DefaultBranch` collapses every git error into `ErrDefaultBranchUnresolved`, so
an interrupted `symbolic-ref`/`rev-parse` during resolution surfaces as that
sentinel and the guards on `recordDefaultBranchFault` /
`recordReapDefaultBranchFault` cannot see the interrupt. Those guards are
therefore structural — correct however the resolver's error handling changes,
unreachable through it today. Making the resolver propagate would change the
fall-through contract of `branchIfExists`/`originHeadBranch`/`gitConfigValue`,
which is a different change with a different risk profile.
