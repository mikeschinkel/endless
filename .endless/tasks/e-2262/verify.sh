#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2262 and records what was true when E-2262
# landed. Edit it only if you ARE E-2262. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2262: a passing user verify sets `unlanded` at the commit it tested; a
# commit past it returns the task to `unverified`; `worktree land` lands a
# todo/bugfix only from a fresh `unlanded`, refuses research and brainstorm, and
# settles the task as `assumed` unless --keep-status.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

root="$(git -C "$ENDLESS_VERIFY_DIR" rev-parse --show-toplevel)" || setup_error "not in a git checkout"
cd "$root" || setup_error "cannot cd to $root"

section "This task's own tests (fail fast)"
out="$(go test -count=1 ./internal/taskstatus/ ./internal/tasktype/ ./internal/worktreecmd/ 2>&1)"
if [[ $? -ne 0 ]]; then
    report_fail "status vocabulary, type land properties and verify-state pass" "ok" "$out"
    summary
fi
report_pass "status vocabulary, type land properties and verify-state pass"

out="$(go test -count=1 ./internal/events/ -run 'PassedCommit|ClearsExceptOnUnlanded|SetUnlanded' -v 2>&1)"
for t in TestUnlandedRecordsThePassedCommit TestLeavingUnlandedClearsThePassedCommit \
         TestFieldsUpdatedClearsExceptOnUnlanded TestAgentMayNotSetUnlanded TestPersonMaySetUnlanded; do
    assert_contains "$t" "--- PASS: $t" "$out"
done

out="$(go test -count=1 ./internal/worktreecmd/ -v 2>&1)"
for t in TestLedgerCommitsDoNotStaleThePass TestACodeCommitStalesThePass TestASuiteEditStalesThePass; do
    assert_contains "$t" "--- PASS: $t" "$out"
done

section "The user's verify, the stale reset, the land gate and the settle"
out="$(uv run pytest tests/test_unlanded.py tests/test_verify_cmd.py -v -p no:cacheprovider 2>&1)"
if [[ $? -ne 0 ]]; then
    report_fail "tests/test_unlanded.py and tests/test_verify_cmd.py pass" "passed" "$out"
else
    report_pass "tests/test_unlanded.py and tests/test_verify_cmd.py pass"
fi
for t in test_a_users_pass_sets_unlanded_at_the_passed_commit test_an_agents_pass_does_not \
         test_a_commit_after_the_pass_returns_the_task_to_unverified \
         test_a_ledger_commit_after_the_pass_does_not \
         test_land_refuses_research_and_brainstorm test_land_refuses_a_todo_that_is_not_unlanded \
         test_land_refuses_a_stale_pass_and_says_why test_land_settles_a_todo_as_assumed \
         test_keep_status_leaves_it_unlanded test_worktree_land_runs_the_gate_before_anything_moves; do
    assert_contains "$t" "tests/test_unlanded.py::$t PASSED" "$out"
done
assert_contains "a passing verify hands the full commit to unlanded" \
    "test_only_a_recorded_pass_reaches_unlanded_with_the_full_commit PASSED" "$out"

section "The lifecycle diagram is regenerated from the table"
out="$(uv run python -m endless.lifecycle_map check 2>&1)"
assert_contains "lifecycle artifacts in sync" "in sync with internal/taskstatus" "$out"
assert_contains "the diagram draws the user's verify" \
    "unverified --> unlanded: user passes verify in their own context" "$(cat docs/status-lifecycle.mmd)"
assert_contains "the diagram draws the settling land" \
    "unlanded --> assumed: user lands the work" "$(cat docs/status-lifecycle.mmd)"
assert_contains "the diagram draws the stale reset" \
    "unlanded --> unverified: system resets when the branch moves past the passed commit" \
    "$(cat docs/status-lifecycle.mmd)"

summary
