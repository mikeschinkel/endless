#!/usr/bin/env bash
#
# E-1743 verification script — the brainstorm's deliverables.
#
# E-1743 is a `brainstorm` task; its deliverable is ledger state, not code:
#   (a) the brainstorm concluded with a synthesis `outcome`, and
#   (b) a correctly-wired follow-up implementation task (E-1746) was filed.
#
# Both live in the REAL ledger (they were created from the main checkout), so
# every query forces `--db main` — `esu` routes this worktree to its sandbox,
# where these tasks do not exist.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1743
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem. This is the E-1596 ad-hoc prototype shape
# (see .endless/tasks/e-1577/verify.sh), not a shared harness.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

BRAINSTORM="1743"   # this task
FOLLOWUP="1746"     # the implementation task it spawned
WEB_RENDERER="445"  # the web markdown renderer it relates to

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

section()     { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}
summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'; return 1
}

# ─── helpers ─────────────────────────────────────────────────────────────────

# Every ledger query hits the REAL DB regardless of worktree sandbox routing.
edb() { endless --db main "$@"; }

# assert_contains DESC PATTERN CMD [ARGS...]  — pass if output contains PATTERN.
assert_contains() {
    local desc="$1" pattern="$2"; shift 2
    local output; output=$("$@" 2>&1)
    if [[ "${output}" == *"${pattern}"* ]]; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "output contains: ${pattern}" "${output}"
}

# ─── checks ──────────────────────────────────────────────────────────────────

test_brainstorm_concluded() {
    section "E-${BRAINSTORM} — brainstorm concluded with a synthesis"

    assert_contains "type is brainstorm" "type=brainstorm" \
        edb task show "${BRAINSTORM}" --llm
    assert_contains "status is completed" "status=completed" \
        edb task show "${BRAINSTORM}" --llm
    assert_contains "outcome carries the synthesis" "What we landed on" \
        edb task show "${BRAINSTORM}" --outcome
    assert_contains "outcome records the goldmark/no-glamour decision" "goldmark" \
        edb task show "${BRAINSTORM}" --outcome
    assert_contains "outcome records the less -R color finding" "less -R" \
        edb task show "${BRAINSTORM}" --outcome
    assert_contains "outcome records dropping the AutoResearch loop" "AutoResearch" \
        edb task show "${BRAINSTORM}" --outcome
    assert_contains "outcome links back to the follow-up" "E-${FOLLOWUP} (implemented by)" \
        edb task show "${BRAINSTORM}" --llm
}

test_followup_filed_and_wired() {
    section "E-${FOLLOWUP} — implementation follow-up filed and wired"

    assert_contains "follow-up is a do-task" "type=task" \
        edb task show "${FOLLOWUP}" --llm
    assert_contains "follow-up awaits approval (submitted)" "status=submitted" \
        edb task show "${FOLLOWUP}" --llm
    assert_contains "follow-up implements the brainstorm" "E-${BRAINSTORM} (implements)" \
        edb task show "${FOLLOWUP}" --llm
    assert_contains "follow-up cleans up the brainstorm" "E-${BRAINSTORM} (cleans up)" \
        edb task show "${FOLLOWUP}" --llm
    assert_contains "follow-up relates to the web renderer" "E-${WEB_RENDERER} (relates to)" \
        edb task show "${FOLLOWUP}" --llm
}

test_followup_plan_content() {
    section "E-${FOLLOWUP} — plan captures the agreed design"

    assert_contains "plan names goldmark renderer" "goldmark" \
        edb task show "${FOLLOWUP}" --text
    assert_contains "plan specifies the pager invocation" "less -R --mouse" \
        edb task show "${FOLLOWUP}" --text
    assert_contains "plan specifies the --no-color toggle" "--no-color" \
        edb task show "${FOLLOWUP}" --text
    assert_contains "plan keeps prose un-reflowed (core fix)" "no inserted hard breaks" \
        edb task show "${FOLLOWUP}" --text
}

# ─── main ────────────────────────────────────────────────────────────────────

main() {
    if ! command -v endless >/dev/null 2>&1; then
        printf 'ERROR: endless not on PATH\n' >&2
        exit 2
    fi

    printf '%sE-%s verification%s\n%s\n' "${BOLD}" "${BRAINSTORM}" "${RESET}" "${UNDERLINE}"
    printf '  db:  main (real ledger)\n'
    printf '  for: brainstorm E-%s + follow-up E-%s\n' "${BRAINSTORM}" "${FOLLOWUP}"

    test_brainstorm_concluded
    test_followup_filed_and_wired
    test_followup_plan_content

    summary
}

main "$@"
