#!/usr/bin/env bash
#
# E-1706 verification suite — the SINGLE entry point for verifying E-1706.
#
#   esu
#   ./tests/tasks/e-1706-verify.sh
#
# Self-contained. Verifies the session-status type-letter reassignment:
# `brainstorm` renders as B (was Z) and `bug` renders as F (was B), via the
# `typeLetter(slug)` function in internal/sessionstatuscmd/session_status.go
# (the only letter-render site). Asserts the behavioral contract with the
# TestTypeLetter unit test, an independent source-level guard on the mapping,
# and a build-sanity compile of the package.
#
# A full `endless session status` render is intentionally NOT scripted:
# typeLetter is a pure function fully covered by the unit test, and seeding
# brainstorm+bug rows into the sandbox to grep a rendered column is fragile.
#
# Exit 0 on all-passed, 1 on any failure (with detail to diagnose).

set -u

# ─── locate worktree ────────────────────────────────────────────────────────

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
WT_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
cd "${WT_ROOT}" || { printf 'ERROR: cannot cd to %s\n' "${WT_ROOT}" >&2; exit 1; }

SRC="${WT_ROOT}/internal/sessionstatuscmd/session_status.go"
PKG="./internal/sessionstatuscmd/"

# ─── output ─────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}

# assert_file_contains DESC FILE PATTERN — pass if FILE exists and contains PATTERN (regex).
assert_file_contains() {
    local desc="$1" file="$2" pattern="$3"
    if [[ ! -f "${file}" ]]; then report_fail "${desc}" "file exists: ${file}" "missing"; return; fi
    if grep -qE -- "${pattern}" "${file}"; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "matches: ${pattern}" "absent in ${file##*/}"
    fi
}

# assert_typeletter_case DESC SLUG LETTER — pass if typeLetter's `case "SLUG":` block
# returns "LETTER". Matches the two-line `case "slug":\n\t\treturn "X"` shape.
assert_typeletter_case() {
    local desc="$1" slug="$2" letter="$3"
    if [[ ! -f "${SRC}" ]]; then report_fail "${desc}" "file exists: ${SRC}" "missing"; return; fi
    local got
    got=$(grep -A1 "case \"${slug}\":" "${SRC}" | grep -oE 'return "[A-Z]"' | grep -oE '"[A-Z]"' | tr -d '"' | head -1)
    if [[ "${got}" == "${letter}" ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "case \"${slug}\" → \"${letter}\"" "got \"${got:-<none>}\""
    fi
}

# assert_cmd DESC CMD [ARGS...] — pass if CMD exits 0.
assert_cmd() {
    local desc="$1"; shift
    local out rc
    out=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "exit 0" "exit=${rc} | $(printf '%s\n' "${out}" | tail -8 | tr '\n' '⏎')"
    fi
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── checks ─────────────────────────────────────────────────────────────────

section "Behavioral contract — typeLetter unit test"
assert_cmd "go test TestTypeLetter (full mapping E/F/R/B/T, no Z)" \
    go test "${PKG}" -run TestTypeLetter -count=1

section "Source guard — typeLetter letter mapping"
assert_typeletter_case "brainstorm renders B (was Z)" brainstorm B
assert_typeletter_case "bug renders F (was B)" bug F
assert_typeletter_case "epic renders E (unchanged)" epic E
assert_typeletter_case "research renders R (unchanged)" research R

section "Source guard — freed letter Z is gone"
assert_file_contains "typeLetter no longer returns \"Z\"" "${SRC}" 'func typeLetter'
if grep -A15 'func typeLetter' "${SRC}" | grep -qE 'return "Z"'; then
    report_fail "typeLetter body contains no return \"Z\"" 'no return "Z"' 'still present'
else
    report_pass "typeLetter body contains no return \"Z\""
fi

section "Build sanity — package compiles"
assert_cmd "go build ${PKG}" go build "${PKG}"

summary
