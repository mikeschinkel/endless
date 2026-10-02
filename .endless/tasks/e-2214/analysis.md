## Lines of enquiry, not conclusions

- **Where the time actually goes.** `--durations=50` before any theory; the answer
  may be a handful of tests rather than a broad drift.
- **Subprocess count.** Many tests shell out to `endless-go`. If each invocation
  pays process start plus a database open, the suite's cost is dominated by
  something orthogonal to test count — and that binary has grown a lot, being one
  consolidated binary that links every subcommand.
- **Per-test database provisioning.** Whether fixtures rebuild a schema or replay
  a ledger projection per test, and whether any of that is cacheable per session.
- **Machine load.** Real, but the strategy must not depend on the machine being
  idle, and load cannot explain twelve times.
- **Parallelism.** `pytest-xdist` is NOT installed. Whether the suite can be
  parallelised at all depends on shared state — a per-worker database is likely
  the precondition — so this is a finding before it is a fix.
- **Buffering.** Independent of total time: the suite should emit progress and
  failures as it goes, so a long or interrupted run is still informative.

## Out of scope

Not a licence to delete, skip or weaken tests to make the number smaller.
