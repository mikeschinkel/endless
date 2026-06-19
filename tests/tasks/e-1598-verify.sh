#!/usr/bin/env bash
#
# E-1598 verification script — confirms the internal/hookcmd
# shouldSkipForWorktreeAt unit tests are green and pins the fix in place.
#
# Bug: the test fixtures wrote the stale binary name bin/endless-hook, but
# production (and the real claude-settings-init recipe) check for
# bin/endless-go since the E-1367 binary unification. The worktreeOverride
# substring match never fired, so two override-asserting tests failed:
#   - TestShouldSkipForWorktreeAt_WorktreeBinaryMissing  (expected WARN; got "")
#   - TestShouldSkipForWorktreeAt_SelfIsGlobal           (expected skip)
# A third assertion in _WorktreeBinaryMissing demanded a "just build" hint
# that was intentionally trimmed from the shipped WARN (just is dev-only).
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1598-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a missing prerequisite.
#
# Ad-hoc per-task verify script in the shape prototyped for E-1596.

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

PKG="./internal/hookcmd/..."
TEST_FILE="internal/hookcmd/claude_skip_test.go"
PROD_FILE="internal/hookcmd/claude.go"

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }

report_pass() {
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n' "${GREEN}" "${PASS_COUNT}" "${RESET}"
        printf '\n  %sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}"
    printf '\n  %sFAILED:%s\n' "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do
        printf '    - %s\n' "${t}"
    done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_rc DESC EXPECTED_RC ACTUAL_RC TAIL_OUTPUT
assert_rc() {
    if [[ "$3" -eq "$2" ]]; then
        report_pass "$1"
        return
    fi
    report_fail "$1" "exit == $2" "exit=$3 | $(printf '%s' "$4" | tail -3 | tr '\n' '|')"
}

# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then
        report_pass "$1"
        return
    fi
    report_fail "$1" "output contains: $2" "$(printf '%s' "$3" | tail -3 | tr '\n' '|')"
}

# assert_not_contains DESC NEEDLE HAYSTACK
assert_not_contains() {
    if [[ "$3" != *"$2"* ]]; then
        report_pass "$1"
        return
    fi
    report_fail "$1" "output does NOT contain: $2" "$(printf '%s' "$3" | tail -3 | tr '\n' '|')"
}

# ─── checks ─────────────────────────────────────────────────────────────────

test_regression_tests() {
    section "Regression — the two previously-failing tests now pass"

    local out rc
    out=$(go test "${PKG}" \
        -run 'TestShouldSkipForWorktreeAt_(WorktreeBinaryMissing|SelfIsGlobal)' -v 2>&1)
    rc=$?

    assert_rc       "go test (targeted) exits 0" 0 "${rc}" "${out}"
    assert_contains "_WorktreeBinaryMissing reports PASS" \
        "PASS: TestShouldSkipForWorktreeAt_WorktreeBinaryMissing" "${out}"
    assert_contains "_SelfIsGlobal reports PASS" \
        "PASS: TestShouldSkipForWorktreeAt_SelfIsGlobal" "${out}"
    assert_not_contains "no FAIL in targeted output" "FAIL" "${out}"
}

test_full_package() {
    section "Package — full internal/hookcmd suite is green"

    local out rc
    out=$(go test "${PKG}" 2>&1)
    rc=$?
    assert_rc       "go test (full package) exits 0" 0 "${rc}" "${out}"
    assert_not_contains "no FAIL in package output" "FAIL" "${out}"
}

test_vet() {
    section "Vet — internal/hookcmd vets clean"

    local out rc
    out=$(go vet "${PKG}" 2>&1)
    rc=$?
    assert_rc "go vet exits 0" 0 "${rc}" "${out}"
}

test_fix_pinned() {
    section "Source — fix is pinned (fixtures use endless-go; no stale assertions)"

    local test_src prod_src
    test_src=$(cat "${TEST_FILE}")
    prod_src=$(cat "${PROD_FILE}")

    assert_not_contains "test fixtures no longer write 'bin/endless-hook'" \
        'bin", "endless-hook"' "${test_src}"
    assert_contains "test fixtures write 'bin/endless-go'" \
        'bin", "endless-go"' "${test_src}"
    assert_not_contains "stale 'just build' assertion removed from test" \
        "remediation hint 'just build'" "${test_src}"
    # PRODUCT guard: the shipped WARN must not reference dev-only 'just'.
    assert_not_contains "shipped WARN does not reference 'just build'" \
        "just build" "${prod_src}"
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

    command -v go   >/dev/null 2>&1 || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }
    command -v just >/dev/null 2>&1 || { printf 'ERROR: just not on PATH\n' >&2; exit 2; }

    # go.work is gitignored and per-developer; worktree replace dirs don't
    # resolve without it. Generate it if absent so go test can build.
    if [[ ! -f go.work ]]; then
        printf 'go.work missing — running just go-work-init…\n'
        just go-work-init >/dev/null 2>&1 || {
            printf 'ERROR: just go-work-init failed\n' >&2; exit 2
        }
    fi

    printf '%sE-1598 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  pkg:     %s\n' "${PKG}"
    printf '  go:      %s\n' "$(go version 2>&1 | awk '{print $3}')"

    test_regression_tests
    test_full_package
    test_vet
    test_fix_pinned

    summary
}

main "$@"
