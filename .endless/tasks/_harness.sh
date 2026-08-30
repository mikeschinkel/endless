#!/usr/bin/env bash
#
# Shared harness for per-task verification suites. Source it, don't run it:
#
#     source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"
#
# It provides the section / report_pass / report_fail / setup_error / summary
# vocabulary these suites have always shared, and adds two things a copied
# block could not:
#
#   1. It REFUSES when the suite was not started by `endless task verify`.
#      A suite is a land-time gate for ONE task; running one from another
#      task's worktree is meaningless at best (its fixtures were pinned to the
#      moment it landed) and destructive at worst (a suite that drives the hook
#      binary writes into the real database). The runner is the only caller
#      that knows which task it is running and can refuse a foreign one, so
#      the harness insists on being reached through it.
#
#   2. It emits TAP alongside the human-readable output, so the runner
#      normalizes each assertion into the same CTRF report a manifest suite
#      produces. TAP goes to the file named by $ENDLESS_VERIFY_TAP; the
#      pass/fail lines you are reading go to the terminal, live and in colour.
#      Neither channel costs the other anything.
#
# See .endless/tasks/CLAUDE.md for the rules these suites live under.

if [[ -z "${ENDLESS_VERIFY_RUN:-}" ]]; then
    printf '%s\n' \
        "This verification suite must be run through the verify runner:" \
        "" \
        "    endless task verify E-<id>" \
        "" \
        "Running it directly skips the isolation that keeps a suite out of your" \
        "real config and the main database, and skips the check that it is YOUR" \
        "task's suite. See .endless/tasks/CLAUDE.md." >&2
    exit 2
fi

PASS_COUNT=0
FAIL_COUNT=0
TAP_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# tap writes one line to the runner's TAP stream. A suite sourced outside the
# runner never gets here (the refusal above), so the variable is always set.
tap() { printf '%s\n' "$1" >>"${ENDLESS_VERIFY_TAP}"; }

# section prints a heading on the terminal only. It emits no TAP: a TAP
# diagnostic line attaches to the PRECEDING test, so a heading written there
# would hang itself off whatever assertion happened to run last.
section() {
    printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"
}

report_pass() {
    PASS_COUNT=$((PASS_COUNT + 1))
    TAP_COUNT=$((TAP_COUNT + 1))
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    tap "ok ${TAP_COUNT} - $1"
}

report_fail() {
    FAIL_COUNT=$((FAIL_COUNT + 1))
    TAP_COUNT=$((TAP_COUNT + 1))
    FAILED_TESTS+=("$1")
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    [[ -n "${2:-}" ]] && printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    [[ -n "${3:-}" ]] && printf '      %sactual:  %s %s\n' "${DIM}" "${RESET}" "$3"
    tap "not ok ${TAP_COUNT} - $1"
    [[ -n "${2:-}" ]] && tap "# expected: $2"
    [[ -n "${3:-}" ]] && tap "# actual:   $3"
    return 0
}

# report_skip records a check that could not run here (a missing optional tool,
# a platform it does not apply to). A skip is neither a pass nor a failure and
# must never be reported as one.
report_skip() {
    TAP_COUNT=$((TAP_COUNT + 1))
    printf '  %s-%s %s %s(skipped: %s)%s\n' "${DIM}" "${RESET}" "$1" "${DIM}" "${2:-}" "${RESET}"
    tap "ok ${TAP_COUNT} - $1 # SKIP ${2:-}"
}

# assert_eq / assert_contains are the two shapes almost every hand-written check
# in these suites took. Having them here means a new suite states the property
# and not the plumbing.
assert_eq() {
    local label="$1" expected="$2" actual="$3"
    if [[ "${actual}" == "${expected}" ]]; then
        report_pass "${label}"
    else
        report_fail "${label}" "${expected}" "${actual}"
    fi
}

assert_contains() {
    local label="$1" needle="$2" haystack="$3"
    if [[ "${haystack}" == *"${needle}"* ]]; then
        report_pass "${label}"
    else
        report_fail "${label}" "output containing: ${needle}" "${haystack}"
    fi
}

assert_not_contains() {
    local label="$1" needle="$2" haystack="$3"
    if [[ "${haystack}" != *"${needle}"* ]]; then
        report_pass "${label}"
    else
        report_fail "${label}" "output WITHOUT: ${needle}" "${haystack}"
    fi
}

setup_error() {
    printf '%sSETUP ERROR:%s %s\n' "${RED}${BOLD}" "${RESET}" "$1" >&2
    tap "Bail out! $1"
    exit 2
}

# summary prints the closing counts and exits: 0 when everything passed, 1 on
# any failure. Call it as the last line of a suite.
summary() {
    section "Summary"
    printf '  %s%d passed%s, %s%d failed%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" \
        "$([[ ${FAIL_COUNT} -gt 0 ]] && printf '%s' "${RED}")" "${FAIL_COUNT}" "${RESET}"

    tap "1..${TAP_COUNT}"

    if (( FAIL_COUNT > 0 )); then
        printf '\n  Failed:\n'
        for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
        exit 1
    fi
    exit 0
}
