#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2461 and records what was true when E-2461
# landed. Edit it only if you ARE E-2461. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2461: `endless worktree land` refuses a task with an unfinished blocker.
#
# Each check runs this branch's tests against the worktree source, with a test
# database and real git repositories (tests/test_land_blocks_gate.py). The first
# is the whole file, fail-fast; the rest re-run it a plan item at a time so a
# failure names what broke.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

cd "$(git -C "${ENDLESS_VERIFY_DIR}" rev-parse --show-toplevel)" \
    || setup_error "cannot find the worktree root"

TESTS=tests/test_land_blocks_gate.py

# pytest_ok <label> [-k expr]
pytest_ok() {
    local label="$1"; shift
    local out
    if out="$(uv run --quiet pytest -q -p no:cacheprovider "$TESTS" "$@" 2>&1)"; then
        report_pass "$label"
    else
        report_fail "$label" "pytest passes" "$(printf '%s\n' "$out" | tail -15)"
    fi
}

section "Fail-fast: this task's tests, and the gates it sits beside"
if ! uv run --quiet pytest -q -x -p no:cacheprovider "$TESTS" \
        tests/test_blocks_gate.py tests/test_unlanded.py >/dev/null 2>&1; then
    report_fail "the test files pass" "all pass" \
        "$(uv run --quiet pytest -q -x -p no:cacheprovider "$TESTS" \
            tests/test_blocks_gate.py tests/test_unlanded.py 2>&1 | tail -20)"
    summary
fi
report_pass "test_land_blocks_gate, test_blocks_gate and test_unlanded pass"

section "P1, P2: land refuses before anything moves, naming each blocker"
pytest_ok "main, the branch and the status are unchanged" -k changes_nothing
pytest_ok "each blocker is named with its status, title and unblock line" \
    -k names_each_blocker

section "P3: only a terminal blocker releases the land"
pytest_ok "a landed blocker still refuses" -k landed_blocker
pytest_ok "assumed and declined blockers release it" -k terminal_blocker_releases
pytest_ok "an unblocked task goes on to land" -k unblocked_task_lands

section "P4: a blocker in another project"
pytest_ok "refuses the same way" -k another_project

section "P5: --dry-run"
pytest_ok "refuses with the same text as a real land" -k dry_run_refuses

section "P6, P7: a land that already happened"
pytest_ok "a re-run whose branch is already merged is not refused" -k already_merged
pytest_ok "a branch merely behind main is still refused" -k merely_behind
pytest_ok "--record-only is not refused" -k record_only

summary
