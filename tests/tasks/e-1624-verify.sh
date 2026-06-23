#!/usr/bin/env bash
#
# E-1624 verification script — confirms sessions.active_epic_id is populated on
# interactive task claim and cleared on release.
#
# The change lives in the Go event executor (internal/events/executor.go):
# execTaskClaimed resolves the nearest type='epic' ancestor of the claimed task
# into sessions.active_epic_id, and execTaskReleased NULLs it alongside
# active_task_id. The behavior is exercised against the REAL code path by the
# Go tests in internal/events/claim_epic_test.go — this script compiles the
# package and runs each behavior as a named check, so a single command tells
# Mike pass/fail per case.
#
# Run from anywhere inside the worktree (esu cd's here and exports the session):
#   ./tests/tasks/e-1624-verify.sh
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

EVENTS_PKG="./internal/events/"
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
#   Run exactly one Go test in the events package (fresh, uncached) and pass iff
#   it succeeds. The test drives the real execTaskClaimed/execTaskReleased
#   against a schema-applied SQLite DB.
assert_go_test() {
    local desc="$1"
    local name="$2"
    assert_cmd "${desc}" \
        go test -count=1 -run "^${name}$" "${EVENTS_PKG}"
}

# ─── checks ─────────────────────────────────────────────────────────────────

test_compiles() {
    section "Build — package compiles with the executor change"
    assert_cmd "internal/events compiles" go build "${EVENTS_PKG}"
}

test_claim_behavior() {
    section "Claim — active_epic_id resolved from the claimed task's ancestry"

    assert_go_test "claim child of an epic sets active_epic_id to the epic" \
        "TestClaim_ChildOfEpicSetsEpicID"
    assert_go_test "nested child resolves the NEAREST epic ancestor" \
        "TestClaim_NestedChildResolvesNearestEpic"
    assert_go_test "standalone task leaves active_epic_id NULL" \
        "TestClaim_StandaloneTaskNullEpicID"
    assert_go_test "claiming an epic directly resolves to its own id" \
        "TestClaim_EpicDirectlyResolvesToSelf"
    assert_go_test "re-claiming a standalone task clears a stale epic id" \
        "TestClaim_ClearsStaleEpicFromPriorClaim"
}

test_release_behavior() {
    section "Release — active_epic_id cleared alongside active_task_id"

    assert_go_test "release NULLs both active_task_id and active_epic_id" \
        "TestRelease_ClearsBothTaskAndEpic"
}

test_no_regression() {
    section "Regression — events + monitor packages stay green"

    assert_cmd "internal/events full suite passes" \
        go test -count=1 "${EVENTS_PKG}"
    assert_cmd "internal/monitor full suite passes (nearestEpicAncestor)" \
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

    printf '%sE-1624 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  go:      %s\n' "$(go version 2>&1 | awk '{print $3}')"

    test_compiles
    test_claim_behavior
    test_release_behavior
    test_no_regression

    summary
}

main "$@"
