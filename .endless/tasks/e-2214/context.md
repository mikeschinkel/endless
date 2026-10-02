The Python suite has gone from 3347 tests in about 2m30s on 2026-09-15 to 3841
tests in about 47m on 2026-10-01 — 15% more tests for roughly twelve times the
wall clock. Both figures were measured on the same machine, from a worktree, with
other sessions active in both cases, so concurrent load is a contributor but
cannot account for the factor.

Why it matters beyond the wait:

- **It is load-bearing on every land.** Every task is obliged to run the
  project-wide regression. At 47 minutes that is routinely the longest step in a
  land, which is how it comes to be skipped or sampled instead of run.
- **A killed run reports nothing at all.** `pytest -q` buffers, so a run stopped
  at a time limit emits neither a progress marker nor the failures it had already
  found. During E-2128 two separate runs were killed at 30 minutes having
  reported literally nothing.
- **Chunking is a workaround that can hide regressions.** E-2128 had to split the
  suite into four file-list chunks to get any result, and splitting changes which
  tests share state and in what order.
