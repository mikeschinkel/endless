Three sites, in the order a run reaches them.

**The recipe.** The `verify` recipe in `justfile` drops its database argument and
calls `endless task verify` with just the task id. Nothing replaces it:
`run_verify` calls `config.default_db_to_main`, which pins main when the caller
named no database, including inside a self-dev worktree where the no-database
gate would otherwise refuse the read. The rest of the recipe — deriving the task
id, locating the worktree, changing into it — is left exactly as it is; removing
it belongs to the task that deletes the recipe.

**The pre-flight.** `run_verify` in `src/endless/verify_cmd.py` gains a check
between pinning the database and shelling out to the runner, beside the existing
`plan_items.require_gate` backstop. It asks `unlanded.verify_states` for the
task. A successful read that returns no row refuses: the task is absent from the
database this invocation pinned, so a pass could not be recorded whatever the
suite says. The refusal names the database it looked in and the invocation that
reaches the right one. A read that raises is swallowed and the run continues, the
way `reconcile_quietly` already treats a read it cannot complete.

**The backstop.** The empty-read branch of `record_pass` in
`src/endless/unlanded.py` stops returning silently. It states that the pinned
database does not hold the task and that the pass was therefore not recorded,
and the command exits non-zero. This is unreachable once the pre-flight is in
place, which is the point: reaching it means the pre-flight was bypassed, and
that is worth an exit code rather than a shrug. The sibling branches — the
agent's run, both status gates, the relayed failure — keep their current wording
and their current exit behaviour.

Tests go in `tests/test_verify_cmd.py` for the pre-flight and the recipe's
routing, and beside the existing `record_pass` tests for the backstop.
