#!/usr/bin/env bash
#
# E-1745 verification script — proves the worktree reaper now auto-removes
# stranded orphan worktree dirs (non-empty, no .git, untracked by git) via a
# strictly path-scoped RemoveAll, and no longer fakes a "removed" success.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1745
#
# The Go unit tests in internal/monitor/reap_worktrees_test.go are the real
# driver — removeStrandedWorktreeDir is unexported, so it is exercised there
# with real temp dirs and the reaper's swappable runGit/hasLiveProcessInDir
# seams. This script builds the project and asserts those tests pass, checking
# the specific cases E-1745 turns on.
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure.

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
    local expected="$2"
    local actual="$3"
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "${desc}"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "${expected}"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "${actual}"
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

# assert_go_test DESC TESTNAME
#   Pass if `go test -run ^TESTNAME$` reports it as PASS.
assert_go_test() {
    local desc="$1"
    local name="$2"
    local output
    output=$(go test ./internal/monitor/ -run "^${name}\$" -v 2>&1)
    local rc=$?
    if [[ "${rc}" -eq 0 ]] && printf '%s' "${output}" | grep -q -- "--- PASS: ${name} "; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "--- PASS: ${name}" "exit=${rc} | $(printf '%s' "${output}" | tail -3)"
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

    printf '%sE-1745 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:  %s\n' "${repo_root}"

    section "Build"
    local build_out
    build_out=$(just build 2>&1)
    if [[ $? -eq 0 ]]; then
        report_pass "just build succeeds"
    else
        report_fail "just build succeeds" "exit == 0" "$(printf '%s' "${build_out}" | tail -5)"
        summary
        return 1
    fi

    section "Stranded orphan is actually removed (not a false 'removed')"
    assert_go_test "empty stranded orphan dir is removed, reaped=true" \
        "TestMaybeReapWorktree_StrandedLeftover_Reaped"
    assert_go_test "NON-empty stranded orphan dir is removed, reaped=true (the e-1281 case)" \
        "TestMaybeReapWorktree_StrandedLeftover_NonEmptyDirReaped"

    section "Path guard refuses anything but a real <root>/.endless/worktrees/e-NNN"
    assert_go_test "refuses a dir outside the worktrees root (deletes nothing)" \
        "TestRemoveStrandedWorktreeDir_RefusesOutsideRoot"
    assert_go_test "refuses a non-e-NNN basename (deletes nothing)" \
        "TestRemoveStrandedWorktreeDir_RefusesNonMatchingBasename"
    assert_go_test "refuses a symlink, leaves its target intact" \
        "TestRemoveStrandedWorktreeDir_RefusesSymlink"

    section "Genuine git-remove failures still surface (no bogus 'removed')"
    assert_go_test "non-stranded git worktree-remove error surfaces as an error" \
        "TestMaybeReapWorktree_GitWorktreeRemoveOtherErrorSurfaces"

    section "No regression in the tracked-worktree reap path"
    assert_go_test "clean abandoned tracked worktree still reaped" \
        "TestMaybeReapWorktree_ReapsCleanAbandoned"

    summary
}

main "$@"
