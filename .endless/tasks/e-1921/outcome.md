# Outcome — §2 measurement

The full suite (3841 tests, all passing) was run with a logging `endless-go`
shim at `<checkout>/bin/endless-go`, a second one in place of the directory
`isolated_env` prepended to PATH, and a third first on the outer PATH. Each shim
logged `$PYTEST_CURRENT_TEST` and its argv, then exec'd a fresh build.

- Every test invokes the binary: 3841 of 3841 via PATH (route 1) — `isolated_env`'s
  `db.get_db()` runs `endless-go event migrate`.
- Routes 2–4 (absolute `bin/endless-go`): 29 tests — test_guide_conditionals 22,
  test_template_materialize 5, test_land_conflict 2.
- Route 5 (test_default_branch_parity's own build): not via any shim, as expected.
- A SIXTH route: 183 tests across 35 files, plus 33 calls per pytest process at
  collection, reached `<checkout>/bin/endless-go` through
  `config.worktree_endless_go()` — the status, session-state and task-content
  vocabulary readers. It exists only when pytest runs from a self-dev worktree.
- The outer PATH shim (a global install behind bin/) was never reached.

After the change, the same instrumentation over the changed and route-6 files
logged zero calls to bin/ or PATH; the verify suite asserts the same over the
full suite with both poisoned.
