Why a shell check with matchers rather than the alternatives (decided with the
user while planning):

- Native tests only: every check becomes durable coverage, but plain facts
  ("Makefile is gone") must become a test in the project's language, and a
  runner that prints neither TAP nor a format Endless reads has no path.
- Exit-code-only checks: any command works, but a failure cannot state an
  expected value the check never declared; `test` and `grep -q` print nothing.
- Hand-written TAP (go-cfgstore's E-2206): each suite copies the same
  ok/not_ok/check helpers, and a malformed line is dropped — non-TAP output
  normalizes to 0 tests.
- A nested `[name, command]` list in one check: compact, but its entries
  cannot carry as/tmux/matchers.

Matchers replace assert_contains/assert_not_contains/assert_eq one for one, so
the endless repo's own suites lose nothing by moving to manifests; each
assertion is its own [[check]] with its own result, and setup runs once with
every check sharing the run's HOME and checkout.

The harness is documented in its own header because only the endless repo has
it; SUITE_RULES and the guide ship to every project.

JUnit XML as a result format is deferred until a project whose test runner
prints neither TAP nor a supported format needs per-test results; until then
such a runner works as one `sh` check.
