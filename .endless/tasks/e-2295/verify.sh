#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2295 and records what was true when E-2295
# landed. Edit it only if you ARE E-2295. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2295: land says the rebase was aborted only when the abort took.
# Every check drives real git; the failing abort is a real held index.lock.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

ROOT="$(git -C "$ENDLESS_VERIFY_DIR" rev-parse --show-toplevel)" \
    || setup_error "cannot find the worktree root from $ENDLESS_VERIFY_DIR"
cd "$ROOT" || setup_error "cannot cd to $ROOT"

T=tests/test_worktree_land_abort.py

pytest_check() {
    local desc="$1"; shift
    local out
    if out="$(uv run pytest -q "$@" 2>&1)"; then
        report_pass "$desc"
    else
        report_fail "$desc" "pytest passes" "$(printf '%s\n' "$out" | tail -25)"
    fi
}

section "Fail-fast: this task's own tests"
pytest_check "all E-2295 tests pass" "$T"

section "land: an abort that never takes"
pytest_check "refusal names the rebase still in progress, not an aborted one" \
    "$T::test_abort_that_never_takes_names_the_rebase_still_in_progress"

section "land: an abort that does take"
pytest_check "an abort that wins the lock on retry reports the branch unchanged" \
    "$T::test_abort_that_wins_the_lock_on_retry_reports_the_branch_unchanged"
pytest_check "an uncontended abort reports the branch unchanged" \
    "$T::test_abort_with_no_contention_reports_the_branch_unchanged"

section "worktree sync --apply: the sibling abort"
pytest_check "a stuck worktree is reported STUCK, not restored" \
    "$T::test_sync_does_not_report_a_stuck_worktree_as_restored"
pytest_check "a clean abort is still reported as restored" \
    "$T::test_sync_still_reports_a_clean_abort_as_restored"

section "worktree diagnose on a mid-rebase worktree"
pytest_check "names the in-progress rebase and how to clear it" \
    "$T::test_diagnose_names_the_rebase_in_progress_instead_of_a_bare_detached_head"

section "No regression in the neighbouring land tests"
pytest_check "lock contention, rebase failure, conflict message, land conflict" \
    tests/test_worktree_land_lock_contention.py \
    tests/test_worktree_land_rebase_failure.py \
    tests/test_worktree_land_conflict_msg.py \
    tests/test_land_conflict.py \
    tests/test_worktree_sync.py

summary
