#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2349 and records what was true when E-2349
# landed. Edit it only if you ARE E-2349. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2349 — re-running `endless project init` brings a registered project up
# to Endless usage, land stops counting Endless's runtime files as user work,
# and `endless project forget` drops a mistaken registration.
#
# Checks, fail-fast in order:
#   1. The task's own Go tests: ForgetProject and migration 25.
#   2. The task's own Python tests: the gitignore block and the path
#      classification guard, the worktree refusal, the guide pointer, the
#      commit rule, forget with and without --with-tasks, and land's partition.
#   3. End to end: a project registered the old way is brought up to date in
#      one commit, and a second run changes no file and makes no commit.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || setup_error "not inside a git worktree"
cd "${REPO_ROOT}" || setup_error "cannot cd to ${REPO_ROOT}"
for tool in go uv; do
    command -v "${tool}" >/dev/null 2>&1 || setup_error "${tool} not on PATH"
done

section "1. Go: forget and its migration"
if out=$(go test -count=1 -run 'TestForgetProject' ./internal/monitor/ 2>&1 \
        && go test -count=1 ./internal/schema/... 2>&1); then
    report_pass "internal/monitor ForgetProject; internal/schema migrations"
else
    report_fail "internal/monitor ForgetProject; internal/schema migrations" "pass" "${out}"
    summary
fi

section "2. Python: bring-up, forget and land"
just build >/dev/null 2>&1 || setup_error "just build failed"
if out=$(uv run pytest -q tests/test_project_bring_up.py tests/test_project_forget.py 2>&1); then
    report_pass "tests/test_project_bring_up.py, tests/test_project_forget.py"
else
    report_fail "tests/test_project_bring_up.py, tests/test_project_forget.py" "pass" "${out}"
    summary
fi

section "3. End to end: one commit, then an idempotent re-run"
if out=$(uv run pytest -q \
        "tests/test_project_bring_up.py::test_bring_up_commits_what_it_wrote_and_a_rerun_changes_nothing" \
        "tests/test_project_bring_up.py::test_a_never_committed_config_is_committed" 2>&1); then
    report_pass "a legacy project is brought up to date; a second run is a no-op"
else
    report_fail "a legacy project is brought up to date; a second run is a no-op" "pass" "${out}"
fi

summary
