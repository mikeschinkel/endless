#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1680 and records what was true when E-1680
# landed. Edit it only if you ARE E-1680. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1680 verification script — confirms `esu` accepts an OPTIONAL task-id.
#
#   esu          → unchanged: auto-resolve the current session (tmux sibling)
#   esu e-NNNN   → resolve the live session whose ACTIVE TASK is NNNN, cd into
#                  its worktree, export its ENDLESS_SESSION_ID
#   esu e-NNNN   → clear error + no-op when no live session is bound to NNNN
#
# The change is Python-only: src/endless/session_cmd.py `_match_companions`
# learns the `e-NNNN` / `E-NNNN` task-id form (matched against active_task_id),
# while bare-numeric (session-id) and UUID-prefix refs are unchanged. The arg
# already flowed through `esu()` → `session use "$@"`, so no shell change.
#
# Each behavior is exercised against the REAL code path by named pytest nodes —
# this script runs each as a check so one command tells Mike pass/fail per case.
#
# Run from anywhere inside the worktree (esu cd's here and exports the session):
#   endless task verify E-1680
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on environment/setup error.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'
    RED=$'\033[31m'
    DIM=$'\033[2m'
    BOLD=$'\033[1m'
    RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() {
    printf '\n%s%s%s\n' "${BOLD}" "$1" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
}

report_pass() {
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

report_fail() {
    local desc="$1"
    local detail="$2"
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "${desc}"
    printf '      %sdetail:%s %s\n' "${DIM}" "${RESET}" "${detail}"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("${desc}")
}

summary() {
    printf '\n%sSummary%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n' "${GREEN}" "${PASS_COUNT}" "${RESET}"
        printf '\n  %sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" \
        "${RED}" "${FAIL_COUNT}" "${RESET}"
    printf '\n  %sFAILED:%s\n' "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do
        printf '    - %s\n' "${t}"
    done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_cmd DESC CMD [ARGS...]
#   Pass if CMD exits 0. On failure, report the tail of its combined output.
assert_cmd() {
    local desc="$1"
    shift
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "exit=${rc} | $(printf '%s' "${output}" | tail -3 | tr '\n' '⏎')"
}

# assert_py_test DESC NODEID
#   Run exactly one pytest node and pass iff it succeeds.
assert_py_test() {
    local desc="$1"
    local node="$2"
    assert_cmd "${desc}" \
        uv run pytest -q "${node}"
}

# ─── checks ─────────────────────────────────────────────────────────────────

test_shell_snippet() {
    section "Shell — esu still forwards its (now optional) argument"

    # The esu helper passes "$@" through to `session use`; bare `esu` keeps
    # the no-arg path. Assert the snippet text rather than sourcing it (no
    # live tmux/session in CI).
    # uv run → the WORKTREE's editable source, so this reflects the candidate
    # build, not the globally-installed `endless` (which points at main).
    local snippet
    snippet=$(uv run endless --db main shell-init 2>&1)
    if printf '%s' "${snippet}" | grep -q 'session use "\$@"'; then
        report_pass 'esu forwards "$@" to `session use`'
    else
        report_fail 'esu forwards "$@" to `session use`' \
            "esu() no longer passes its argument through"
    fi
    if printf '%s' "${snippet}" | grep -q 'esu e-NNNN'; then
        report_pass "shell-init documents the optional e-NNNN task-id form"
    else
        report_fail "shell-init documents the optional e-NNNN task-id form" \
            "comment for the task-id form is missing"
    fi
}

test_task_id_resolution() {
    section "Python — esu e-NNNN resolves the session by active task"

    assert_py_test "esu e-NNNN → cd worktree + export that session's id" \
        "tests/test_session_use.py::test_use_resolves_task_id_form"
    assert_py_test "E-NNNN form is case-insensitive" \
        "tests/test_session_use.py::test_use_task_id_form_is_case_insensitive"
    assert_py_test "_match_companions matches the e-NNNN form vs active_task_id" \
        "tests/test_session_use.py::test_match_companions_task_id_branch"
}

test_optional_and_error_paths() {
    section "Python — optional arg + clear error preserved"

    assert_py_test "no live session for the task → clear error, no-op stdout" \
        "tests/test_session_use.py::test_use_task_id_no_live_session_errors"
    assert_py_test "bare numeric ref still means session-id (regression)" \
        "tests/test_session_use.py::test_use_bare_numeric_still_means_session_id"
    assert_py_test "no-arg activation block unchanged (regression)" \
        "tests/test_session_use.py::test_use_emits_minimal_block"
}

test_no_regression() {
    section "Regression — full session suites stay green"

    assert_cmd "session use suite passes" \
        uv run pytest -q tests/test_session_use.py
    assert_cmd "session cd suite passes" \
        uv run pytest -q tests/test_session_cd.py
    assert_cmd "shell-init suite passes" \
        uv run pytest -q tests/test_shell_init.py
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    local repo_root
    repo_root=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${repo_root}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${repo_root}" || exit 2

    if ! command -v uv >/dev/null 2>&1; then
        printf 'ERROR: uv not on PATH\n' >&2
        exit 2
    fi

    printf '%sE-1680 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"

    test_shell_snippet
    test_task_id_resolution
    test_optional_and_error_paths
    test_no_regression

    summary
}

main "$@"
