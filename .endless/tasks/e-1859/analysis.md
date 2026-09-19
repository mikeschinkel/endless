# Evidence: why automatic triage never fires where tasks are filed

Gathered 2026-08-13 during the E-1958 brainstorm. The prescription lives in this
task's plan text; this is only the trace behind it.

## The suppression's premise does not hold on this path

`triage.inline_suppressed()` returns a suppression reason for "self-dev worktree
pinned to the real ledger", mirroring `internal/jobs`'s `suppressedWithReason`.
The mirrored rationale is that candidate code must not write the real ledger.

Traced under `--db main` from inside a worktree:

- `triage.spawn_detached` launches `sys.argv[0]` — the `endless` shim. `just
  install` runs `uv tool install -e .` from the MAIN checkout, so the shim
  resolves to main's source. CLAUDE.md forbids installing editable from a
  worktree.
- The child writes via `event_bridge._resolve_endless_go()`, whose documented
  behavior is: prefer `<worktree>/bin/endless-go` under `--db sandbox`,
  otherwise fall back to the PATH-resolved global.

So under `--db main` both the Python and the Go halves are landed code. The DB
router had already closed the door the suppression guards.

Corroborating: the justfile's `verify` recipe invokes `endless --db sandbox task
verify`, i.e. the global shim; only the `test` recipe's `uv run pytest` resolves
worktree Python. Nothing in normal use runs candidate Python via the `endless`
command.

Where the same rule IS correct: the Claude hooks written into a worktree's
`.claude/settings.json` invoke `<worktree>/bin/endless-go`, which is genuinely
candidate. `internal/jobs`'s suppression protects that; triage copied it without
re-deriving it.

## What is and is not already race-safe

Safe — sweep vs. sweep across processes. `internal/jobs` claims with a CAS lease
(one conditional UPDATE whose WHERE clause is the precondition) and takes every
due/expiry comparison from SQLite's clock, so N processes share one clock. The N
`session monitor` instances that each call `jobs.RunDue` contend and one wins.
`triagejob`'s Schedule sets `LeaseTTL` explicitly from `batchLimit *
perTaskTimeout + leaseHeadroom`, not from Interval, so shortening the interval
does not shrink the lease.

NOT safe — inline vs. sweep. `spawn_detached` invokes `endless triage run --task
N` directly, bypassing the jobs runner entirely, so it holds no lease. A sweep
firing during an inline child's model call re-selects the same still-`untriaged`
row and pays for a second model call. Python's post-call re-read guards the
write but not the spend.

Latent today only because the suppression means the inline path never runs in a
worktree. Fixing the suppression makes it live.

## Errors are unobservable

`spawn_detached` routes all three stdio streams to DEVNULL, on top of a
`triage.run` that is fail-open by design — timeout, missing `claude`, non-zero
exit and unparseable verdict all leave the task `untriaged` without a sound. A
crashed child and a considered no-verdict are indistinguishable, and neither is
recorded anywhere.

## Scale of the symptom

137 live worktrees under `.endless/worktrees/`. Agents file the large majority of
tasks, from worktrees, with `--db main`. So the suppressed path is the normal
path, and the 15-minute sweep is the effective triage latency for nearly every
task filed.
