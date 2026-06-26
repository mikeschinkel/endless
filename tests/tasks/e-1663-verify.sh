#!/usr/bin/env bash
#
# E-1663 verification script — confirms the outcome requirement is keyed on task
# TYPE (research, brainstorm), not on the `completed` status (ED-1520, which
# supersedes E-1240's status coupling).
#
# Matrix (via `task update --status completed`, the type-gated path):
#   - research   completed w/o outcome  -> REFUSED
#   - research   completed w/  outcome  -> OK
#   - brainstorm completed w/o outcome  -> REFUSED
#   - brainstorm completed w/  outcome  -> OK
#   - plain task completed w/o outcome  -> OK   (no longer required — the change)
#   - epic       completed w/o outcome  -> OK   (epics self-complete)
#   - decline    w/o reason             -> REFUSED (decline keeps its own rule)
#   - confirm    w/o outcome            -> OK   (never required)
# Plus the guide table reflects the type-based wording.
#
# Run from anywhere inside the worktree (esu cd's here and exports the session):
#   esu && ./tests/tasks/e-1663-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 environment/setup error. Fresh ids per run; sandbox is not wiped between runs.

set -u

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
    printf '      %sexpected:%s %s\n      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$2" "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}
summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done; printf '\n'
    return 1
}

# Route through the sandbox DB and the WORKTREE source; add --no-session when no
# session is exported (consumed globally; harmless on reads).
endless() {
    if [[ -n "${ENDLESS_SESSION_ID:-}" ]]; then
        uv run endless "$@" --db sandbox
    else
        uv run endless "$@" --db sandbox --no-session
    fi
}

add_task_get_id() {
    local title="$1"; shift
    local output; output=$(endless task add "${title}" "$@" 2>&1)
    if [[ $? -ne 0 ]]; then printf 'ERROR: add failed for %q: %s\n' "${title}" "${output}" >&2; return 1; fi
    printf '%s\n' "${output}" | grep -oE 'E-[0-9]+' | head -1
}

assert_refused() {
    local desc="$1" pattern="$2"; shift 2
    local output; output=$("$@" 2>&1); local rc=$?
    if [[ "${rc}" -ne 0 ]] && [[ "${output}" == *"${pattern}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit!=0 AND contains: ${pattern}" "exit=${rc} | ${output}"
}
assert_succeeds() {
    local desc="$1"; shift
    local output; output=$("$@" 2>&1); local rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | ${output}"
}
assert_contains() {
    local desc="$1" pattern="$2"; shift 2
    local output; output=$("$@" 2>&1)
    if [[ "${output}" == *"${pattern}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output contains: ${pattern}" "${output}"
}

test_deliverable_types_require_outcome() {
    section "Deliverable types (research, brainstorm) require --outcome"

    local rid
    rid=$(add_task_get_id "Research the pricing tiers" --type research --justification "verify") || return
    assert_refused "research 'completed' without outcome is refused" \
        "outcome is required" endless task update "${rid}" --status completed

    rid=$(add_task_get_id "Research the pricing tiers" --type research --justification "verify") || return
    assert_succeeds "research 'completed' WITH outcome succeeds" \
        endless task update "${rid}" --status completed --outcome "findings: 3 tiers"

    local bid
    bid=$(add_task_get_id "Explore the pricing tiers" --type brainstorm) || return
    assert_refused "brainstorm 'completed' without outcome is refused" \
        "outcome is required" endless task update "${bid}" --status completed

    bid=$(add_task_get_id "Explore the pricing tiers" --type brainstorm) || return
    assert_succeeds "brainstorm 'completed' WITH outcome succeeds" \
        endless task update "${bid}" --status completed --outcome "synthesis + follow-ups"
}

test_other_types_not_required() {
    section "Non-deliverable types are NOT forced to carry an outcome"

    # Plain task with a completable verb (passes the verb gate); the headline change.
    local tid
    tid=$(add_task_get_id "Audit the cache layer" --type task) || return
    assert_succeeds "plain task 'completed' without outcome now succeeds" \
        endless task update "${tid}" --status completed

    local eid
    eid=$(add_task_get_id "Build the reporting subsystem" --type epic) || return
    assert_succeeds "epic 'completed' without outcome succeeds (self-completing)" \
        endless task update "${eid}" --status completed
}

test_independent_rules_unchanged() {
    section "Decline still needs a reason; confirm/assume never require outcome"

    local did
    did=$(add_task_get_id "Add a throwaway widget" --type task) || return
    assert_refused "decline without a reason is refused (ED-1022)" \
        "outcome is required" endless task update "${did}" --status declined

    local cid
    cid=$(add_task_get_id "Add a confirmable widget" --type task) || return
    assert_succeeds "confirm without outcome succeeds" \
        endless task confirm "${cid}"
}

test_guide_reflects_change() {
    section "Guide table states the type-based rule"

    assert_contains "'guide tasks' ties the requirement to research/brainstorm" \
        "completing a \`research\`/\`brainstorm\` task" \
        endless guide tasks
}

main() {
    local repo_root; repo_root=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -n "${repo_root}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    cd "${repo_root}" || exit 2
    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }

    printf '%sE-1663 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cwd:     %s\n  db:      sandbox\n  python:  %s\n' "${repo_root}" "$(uv run python --version 2>&1 | tail -1)"

    test_deliverable_types_require_outcome
    test_other_types_not_required
    test_independent_rules_unchanged
    test_guide_reflects_change

    summary
}

main "$@"
