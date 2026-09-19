# E-2083 — as implemented

## Restore the four E-2074 deleted

`.endless/tasks/e-{1568,1570,1572,1621}/verify.sh`, content taken from
`bf285b54^:tests/tasks/e-NNNN-verify.sh`.

The three project-wide sweeps that crossed every suite after E-2074 are applied
to them, because they were already deleted when those sweeps ran and restoring
a file means putting it where it would be now:

- E-2023 (`c79857dfc`, `d3cf23423`) — relocation to `.endless/tasks/<id>/`,
  the harness-source block as the first executable statement, the header
  run-line rewritten to `endless task verify E-NNNN`, prose path references
  rewritten.
- E-2090 (`53376963c`, `971636d16`) — the four-line DO-NOT-EDIT banner below
  the shebang (via the idempotent `just suite-banner`), and the executable bit.

Without them `tests/test_suite_guard.py` fails: it parametrizes over every
suite and requires banner, harness-first-line and the x bit.

## Revert the ten it amended

`e-{1573,1624,1645,1648,1659,1905,1906,1914,1967,2067}`. Nine reverse-applied
cleanly from E-2074's own diff. e-1573's first hunk was done by hand because
E-2023 had since rewritten the run-line inside it.

The task title says eight; the commit amended ten, as the analysis field lists.
Ten are reverted.

### The per-file judgment the task asked for

E-2074's message defends three amendments as fixes to real pre-existing
leftovers. All three claims are false — each thing was live in the tree at
`bf285b54^`, the commit's own parent, and was made stale BY E-2074 in the same
commit that then "fixed" the suite for it:

| claim | state at `bf285b54^` |
|---|---|
| e-1573 asserted the guide documents `bg_throttle_warn` | live in `src/endless/task_cmd.py` AND in `docs/guide/orchestration.md` |
| e-1914 probed `sessions.summary` | declared in `internal/schema/schema.sql` |
| e-2067 probed `session.go` for `nearestEpicAncestor` | present in `internal/monitor/session.go` |

So all ten are retrofits and revert uniformly. Section 5 of the suite asserts
this against git history rather than leaving it as prose.

## E-2074's own suite: deliberately untouched

The task description's last step assumed its four `assert_absent` lines would
start failing. They do not: they name the pre-E-2023 paths
(`tests/tasks/e-1568-verify.sh`), which stay gone, and its reference sweep
greps only `cmd internal src tests docs justfile`, never `.endless/`. Nothing
in it breaks, so editing it would commit this task's own sin while reverting
it. Mike's call, this session. Section 6 asserts it was left alone.

## GROWN SCOPE — the harness TAP defect

Found while mutation-testing this task's own suite; folded in on Mike's call
(ED-1550: fold a finding into an open task rather than file).

`_harness.sh`'s `tap()` wrote its argument to the runner's TAP stream with
`printf '%s\n' "$1"`, so a multi-line `actual` — a diff, or `tail -25` of a log
— put unprefixed lines mid-stream. The runner's parser stopped counting where
it met one: a run that was really 1 failed / 53 passed was reported as
1 failed / 18 passed, with the suite's own on-screen summary still correct. It
under-reported only on a FAILING run, which is exactly when the count is read.
30 landed suites pass a multi-line `actual` today.

- `tap()` now emits every line after the first as a `# ` diagnostic, which TAP
  attaches to the record it follows. Single-line calls are byte-identical.
- `tests/test_suite_guard.py` gains two durable tests: one proving a multi-line
  detail stays one densely-numbered record with its detail intact (it fails
  against the old `tap()`), one pinning that single-line records are unchanged.
  The coverage lives there, not only here, because a fix protected solely by a
  land-time suite is unprotected the moment this task lands.

Blast radius is asserted, not assumed: section 6 confirms the only files
touched outside the fifteen suites are `_harness.sh` and its test, that the
harness still defines exactly the same functions by name, and that every
non-comment line the edit changed is inside `tap()`.

## Verify

`.endless/tasks/e-2083/verify.sh` — 59 assertions. Section 1 is the fail-fast
gate (`tests/test_suite_guard.py`, `tests/test_suite_rules.py`). Section 3 is
the core proof: subtract the three sweeps named above and every one of the
fourteen is byte-identical to `bf285b54^`.
