#!/usr/bin/env bash
#
# E-1640 verification script — confirms duplicate session rows are no longer
# minted per Claude launch when TMUX_PANE is empty.
#
# The change lives in internal/monitor/session.go: BindSessionToTask now runs an
# active_task_id-scoped fallback dedup. After binding a session to a task it ends
# any OTHER non-ended foreground (kind_id = tmux) row for the same task that has
# no pane — the empty-pane case TouchSession's pane-collision path can't catch.
# Background agents (kind_id = background) and pane-bound rows are deliberately
# left alone. The behavior is exercised against the REAL code path by the Go
# tests in internal/monitor/session_dedup_test.go — this script compiles the
# package and runs each behavior as a named check, so a single command tells
# Mike pass/fail per case.
#
# Run from anywhere inside the worktree (esu cd's here and exports the session):
#   ./tests/tasks/e-1640-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on environment/setup error.

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

MONITOR_PKG="./internal/monitor/"

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

# assert_go_test DESC TESTNAME
#   Run exactly one Go test in the monitor package (fresh, uncached) and pass iff
#   it succeeds. The test drives the real TouchSession→BindSessionToTask flow
#   against a schema-applied SQLite DB.
assert_go_test() {
    local desc="$1"
    local name="$2"
    assert_cmd "${desc}" \
        go test -count=1 -run "^${name}$" "${MONITOR_PKG}"
}

# ─── checks ─────────────────────────────────────────────────────────────────

test_compiles() {
    section "Build — package compiles with the dedup change"
    assert_cmd "internal/monitor compiles" go build "${MONITOR_PKG}"
}

test_dedup_behavior() {
    section "Dedup — empty-pane relaunch ends the prior row, not duplicates it"

    assert_go_test "second launch on same task ends the stale paneless row" \
        "TestBindSessionToTask_EndsStalePanelessRowsSameTask"
    assert_go_test "repeated relaunches stay at exactly one live row" \
        "TestBindSessionToTask_RepeatedLaunchesStayAtOneLiveRow"
}

test_scoping_guards() {
    section "Scoping — only the bound task's stale paneless foreground rows end"

    assert_go_test "a background agent on the same task is NOT ended" \
        "TestBindSessionToTask_DoesNotEndBackgroundAgentSameTask"
    assert_go_test "a pane-bound row on the same task is NOT ended" \
        "TestBindSessionToTask_DoesNotEndPanedRowSameTask"
    assert_go_test "a paneless row for a DIFFERENT task is NOT ended" \
        "TestBindSessionToTask_DoesNotEndOtherTasksRows"
}

test_no_regression() {
    section "Regression — monitor package stays green"

    assert_cmd "internal/monitor full suite passes" \
        go test -count=1 "${MONITOR_PKG}"
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

    if ! command -v go >/dev/null 2>&1; then
        printf 'ERROR: go not on PATH\n' >&2
        exit 2
    fi

    # Worktrees need a go.work pointing at the local go-pkgs/ modules; without it
    # the replace directives resolve at the wrong depth and the build fails.
    # Generate it on demand so the script is self-contained.
    if [[ ! -f "${repo_root}/go.work" ]]; then
        if command -v just >/dev/null 2>&1; then
            just go-work-init >/dev/null 2>&1
        fi
        if [[ ! -f "${repo_root}/go.work" ]]; then
            printf 'ERROR: go.work missing and could not be generated (run: just go-work-init)\n' >&2
            exit 2
        fi
    fi

    printf '%sE-1640 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  go:      %s\n' "$(go version 2>&1 | awk '{print $3}')"

    test_compiles
    test_dedup_behavior
    test_scoping_guards
    test_no_regression

    summary
}

main "$@"
