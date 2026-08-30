#!/usr/bin/env bash
#
# E-1605 verification script — proves the txtar/testscript executable form and the
# two first reference verification suites (E-1758 and E-1603) end to end.
#
# Run from anywhere inside the worktree (esu puts you here):
#   esu && ./tests/tasks/e-1605-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup/environment error. This is the single fail-fast suite for
# the task; it folds the task's own tests in as checks.
#
# This exercises all four runner forms the system now supports: gotest, the
# testscript/.txtar CLI/e2e form, a raw TAP command, and the first-class pytest
# runner (pytest/uv, per E-1789's driver architecture).

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() {
    printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"
}

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
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" \
        "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

assert_succeeds() {
    local desc="$1"; shift
    local out; out=$("$@" 2>&1); local rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | $(printf '%s' "${out}" | tail -3 | tr '\n' ' ')"
}

assert_contains() {
    local desc="$1"; local pat="$2"; shift 2
    local out; out=$("$@" 2>&1)
    if [[ "${out}" == *"${pat}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output contains: ${pat}" "$(printf '%s' "${out}" | tail -3 | tr '\n' ' ')"
}

assert_path_exists() {
    local desc="$1"; local path="$2"
    if [[ -e "${path}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "path exists: ${path}" "missing"
}

# cache_dir mirrors Go's os.UserCacheDir so we can locate the CTRF the runner writes.
cache_dir() {
    if [[ "$(uname)" == "Darwin" ]]; then
        printf '%s/Library/Caches\n' "${HOME}"
    else
        printf '%s\n' "${XDG_CACHE_HOME:-${HOME}/.cache}"
    fi
}

# assert_suite_passes ID — run the candidate runner over a real .endless/tasks/ID
# suite, asserting exit 0, the PASSED summary, and a written CTRF report.
assert_suite_passes() {
    local id="$1"; local expect="${2:-}"
    local out; out=$("${BIN}" verify "${id}" 2>&1); local rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "verify ${id}: exit 0"
    else
        report_fail "verify ${id}: exit 0" "exit == 0" "exit=${rc} | $(printf '%s' "${out}" | tail -6 | tr '\n' ' ')"
    fi
    if [[ "${out}" == *"PASSED"* ]]; then
        report_pass "verify ${id}: PASSED summary"
    else
        report_fail "verify ${id}: PASSED summary" "output contains PASSED" "$(printf '%s' "${out}" | tail -6 | tr '\n' ' ')"
    fi
    if [[ -n "${expect}" ]]; then
        if [[ "${out}" == *"${expect}"* ]]; then
            report_pass "verify ${id}: ran ${expect} check"
        else
            report_fail "verify ${id}: ran ${expect} check" "output contains ${expect}" "$(printf '%s' "${out}" | tail -6 | tr '\n' ' ')"
        fi
    fi
    assert_path_exists "verify ${id}: CTRF report written" "$(cache_dir)/endless/verify/${id}/ctrf.json"
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2; exit 2
    fi
    cd "${REPO_ROOT}" || exit 2

    printf '%sE-1605 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  repo:   %s\n' "${REPO_ROOT}"

    section "Build — worktree endless-go (candidate code under test)"
    assert_succeeds "go build -o bin/endless-go ./cmd/endless-go" \
        go build -o bin/endless-go ./cmd/endless-go
    BIN="${REPO_ROOT}/bin/endless-go"
    if [[ ! -x "${BIN}" ]]; then
        printf 'ERROR: endless-go did not build; aborting\n' >&2; exit 2
    fi

    section "Python env — materialize .venv (pytest + pytest-json-report)"
    assert_succeeds "uv sync (installs the pytest/uv launcher target + json-report plugin)" \
        uv sync
    assert_path_exists "project venv pytest executable present" "${REPO_ROOT}/.venv/bin/pytest"

    section "testscript/txtar dependency (rogpeppe/go-internal, module mode)"
    assert_contains "go.mod declares github.com/rogpeppe/go-internal" \
        "github.com/rogpeppe/go-internal" cat "${REPO_ROOT}/go.mod"
    assert_succeeds "module builds with the testscript dep (go build ./...)" \
        go build ./...

    section "txtar form runs under plain 'go test' (bare-clone safe, no Endless)"
    assert_succeeds "TestWorktreeCheckScripts passes via go test" \
        go test -count=1 -run '^TestWorktreeCheckScripts$' ./internal/monitor/

    section "Reference suite — E-1758 (txtar + gotest)"
    assert_suite_passes "E-1758"

    section "Reference suite — E-1603 (gotest + raw TAP + pytest/uv)"
    assert_suite_passes "E-1603" "pytest/uv"

    summary
}

main "$@"
