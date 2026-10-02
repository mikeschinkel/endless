# Plan — measure first, then act on what the measurement says

## Measure before theorising

Nothing is changed until there are numbers. The 12x is a symptom and the cause is
not yet known; acting on a guess here risks optimising something that is not the
cost.

1. **`pytest --durations=50`** over the whole suite. The first question is whether
   this is a handful of slow tests or a broad drift, because the two have nothing
   in common as fixes.
2. **Subprocess count and cost.** Instrument how many times the suite invokes
   `endless-go` and what one invocation costs cold. Many tests shell to it, and it
   is now one consolidated binary linking every subcommand — if each call pays
   process start plus a database open, total cost tracks invocations, not tests.
3. **Per-test fixture cost.** Time a single trivial test in isolation. Whatever it
   costs is paid 3841 times, and whether fixtures rebuild a schema or replay a
   ledger projection per test is the thing most likely to have changed.
4. **Baseline the machine.** Record the same measurements with and without other
   sessions active, so the load contribution is quantified rather than argued.

## Then act, largest cost first

The work the measurement justifies, and nothing else. Likely candidates, in the
order they would be considered:

- Hoist per-test setup to per-module or per-session where tests do not mutate it.
- Reuse one built binary across tests instead of rebuilding or re-invoking.
- Cache or share the fixture database where tests only read it.
- Parallelise — last, and only if shared state allows it (see the question below).

## Report progress live, regardless of the above

Independent of total time, and worth doing even if the suite stays slow: a run
must emit progress and failures AS IT GOES. `pytest -q` buffers, so a run stopped
at a time limit reports neither how far it got nor the failures it had already
found — two runs were killed at 30 minutes during E-2128 having reported nothing
at all. Whatever the fix (`-p no:cacheprovider`, line buffering, a reporter
plugin, `--tb=line` with incremental flush), the test is that a run killed
part-way still says where it was and what failed.

## Out of scope

Deleting, skipping, marking slow, or weakening tests to make the number smaller.
If a test is genuinely redundant that is its own task with its own argument.

## Verification

- A before/after wall-clock figure for the full suite on the same machine, with
  the load condition stated.
- The full suite still passes, with the same test count or more.
- A run interrupted part-way reports its progress and any failures found.
