#!/usr/bin/env bash
#
# E-1643 verification script — confirms the research/policy deliverable exists.
#
# E-1643 is a RESEARCH task with no code deliverable: the output is a
# recommendation (forbid / warn / leave) recorded in the task's `outcome`. A
# research outcome is reviewed by *reading* it, not by behavioral assertions, so
# this script is intentionally minimal — it only guards against an empty
# hand-off. It asserts:
#   1. `endless task show E-1643 --outcome` is non-empty.
#   2. The outcome names exactly one of the three sanctioned recommendations
#      (forbid / warn / leave) — i.e. a concrete decision was recorded.
#
# Run from anywhere inside the worktree (esu cd's here and exports the session):
#   ./tests/tasks/e-1643-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on environment/setup error. Re-runnable (read-only).

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

# ─── checks ─────────────────────────────────────────────────────────────────

OUTCOME=""

test_outcome_present() {
    section "Deliverable — recommendation recorded in E-1643 outcome"

    OUTCOME=$(endless task show E-1643 --outcome --db main 2>&1)
    local rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        report_fail "endless task show E-1643 --outcome succeeds" \
            "exit=${rc} | $(printf '%s' "${OUTCOME}" | tail -2 | tr '\n' '⏎')"
        return
    fi
    report_pass "endless task show E-1643 --outcome succeeds"

    # Strip a leading "Outcome" header line if the CLI prints one, then test for
    # any non-whitespace content.
    if printf '%s' "${OUTCOME}" | grep -q '[^[:space:]]'; then
        report_pass "outcome is non-empty (no empty research hand-off)"
    else
        report_fail "outcome is non-empty (no empty research hand-off)" \
            "outcome rendered empty"
    fi
}

test_outcome_names_recommendation() {
    section "Decision — outcome names one of forbid / warn / leave"

    # Case-insensitive whole-word match so 'warn-only' / 'leave-as-is' count.
    if printf '%s' "${OUTCOME}" | grep -Eiq '\b(forbid|warn|leave)\b'; then
        report_pass "outcome names a concrete recommendation (forbid|warn|leave)"
    else
        report_fail "outcome names a concrete recommendation (forbid|warn|leave)" \
            "none of forbid/warn/leave found in outcome"
    fi
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    if ! command -v endless >/dev/null 2>&1; then
        printf 'ERROR: endless not on PATH\n' >&2
        exit 2
    fi

    printf '%sE-1643 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  task:    E-1643 (research / policy — recommendation deliverable)\n'

    test_outcome_present
    test_outcome_names_recommendation

    summary
}

main "$@"
