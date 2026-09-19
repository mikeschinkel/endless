## How it was hit

2026-08-08, bulk-routing `unplanned` tasks to `untriaged` from the endless main
checkout, with two other worktree sessions live and the triager on its
15-minute cadence. One `endless task update` carrying 337 ids: 3 applied, the
rest refused with git's raw lock message.

It fails CLOSED — no partial or corrupt state, the DB write simply does not
happen — and chunking into batches of 20 with three retries completed the job.
So severity is friction, not corruption.

## Why it is not just a bulk-operation problem

The contention is between PROCESSES, not within one. E-1309 routes every
session's ledger commits to the single project main checkout, so the writers
are: each concurrent worktree session, the triage sweep, and any foreground
command. Running several parallel worktree sessions is the normal workflow
here, so the collision window exists for a single ordinary write too — it is
just less likely to be observed than in a burst.

Note the monitor's own constant `git worktree list` / `rev-list` polling does
NOT contend: those are reads and take no index lock. The competing writers are
other ledger commits.

## Fix directions

- Retry lock acquisition with backoff in the Go commit path (smallest change).
- Or coalesce appends so one commit covers a burst of events rather than one
  commit per event. The commit already amends into a single `Endless: record
  ledger entry` commit, so the per-event commit is buying nothing that a
  debounced commit would not.

Secondary: the raw git text reaches the user, and the advice it gives — remove
the lock file manually — is wrong for this case and dangerous as a habit.
