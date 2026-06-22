#!/usr/bin/env bash
#
# E-1617 verification script — exercises the epic verb-gate exemption
# (ED-1511) end-to-end against the worktree's sandbox DB.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1617-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure. Each run creates fresh task IDs; the script does NOT wipe the
# sandbox between runs (pollution is bounded and inspectable via
#   uv run endless task list --db sandbox).
#
# Ad-hoc prototype following the convention being formalized by E-1596;
# requires no external tools beyond `uv` + the per-worktree sandbox.

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

# ─── helpers ────────────────────────────────────────────────────────────────

# Wrap the CLI so every invocation routes through the sandbox DB.
endless() {
    uv run endless "$@" --db sandbox
}

# Create a task and emit just its E-NNN id on stdout. All other output goes to
# stderr so callers can capture only the id.
add_task_get_id() {
    local title="$1"
    shift
    local output
    output=$(endless task add "${title}" "$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        printf 'ERROR: add failed for %q: %s\n' "${title}" "${output}" >&2
        return 1
    fi
    printf '%s\n' "${output}" | grep -oE 'E-[0-9]+' | head -1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_succeeds DESC CMD [ARGS...]
assert_succeeds() {
    local desc="$1"
    shift
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

# assert_refused DESC PATTERN CMD [ARGS...]
#   Pass if CMD exits non-zero AND its combined output contains PATTERN.
assert_refused() {
    local desc="$1"
    local pattern="$2"
    shift 2
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -ne 0 ]] && [[ "${output}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" \
        "exit != 0 AND output contains: ${pattern}" \
        "exit=${rc} | output=${output}"
}

# assert_status DESC EXPECTED_STATUS TASK_ID
#   Pass if `task show` reports the task at EXPECTED_STATUS.
assert_status() {
    local desc="$1"
    local expected="$2"
    local tid="$3"
    local output
    output=$(endless task show "${tid}" 2>&1)
    if [[ "${output}" == *"Status:"*"${expected}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "Status: ${expected}" "${output}"
}

# ─── unit tests (pytest) ─────────────────────────────────────────────────────

test_unit_suite() {
    section "Unit tests — tests/test_completed_status.py (pytest)"

    # The gate's unit-level coverage IS part of verifying this task; run it
    # here so the script is the single entry point. On failure, assert_succeeds
    # dumps pytest's output (which names the failing cases).
    assert_succeeds "tests/test_completed_status.py passes" \
        uv run pytest tests/test_completed_status.py -q
}

# ─── the deadlock fix: epics complete despite an implementation verb ─────────

test_epic_completes_via_complete() {
    section "Fix — epic with implementation verb completes via 'task complete'"

    local eid
    eid=$(add_task_get_id "Implement the foo subsystem" --type epic)
    assert_succeeds "epic 'Implement …' accepts 'task complete --outcome'" \
        endless task complete "${eid}" --outcome "shipped via children E-a, E-b"
    assert_status "epic now at status 'completed'" "completed" "${eid}"
}

test_epic_completes_via_update() {
    section "Fix — epic with implementation verb completes via 'task update'"

    local eid
    eid=$(add_task_get_id "Implement the bar subsystem" --type epic)
    assert_succeeds "epic 'Implement …' accepts 'task update --status completed'" \
        endless task update "${eid}" --status completed --outcome "coordination summary"
    assert_status "epic now at status 'completed'" "completed" "${eid}"
}

# ─── the exemption is narrow: other gates stay intact ────────────────────────

test_non_epic_still_gated() {
    section "Regression — verb gate still applies to non-epic types"

    local tid aid
    tid=$(add_task_get_id "Implement the baz subsystem")
    assert_refused "plain task 'Implement …' still rejected by verb gate" \
        "completable" \
        endless task complete "${tid}" --outcome "done"

    # Verb gate is dropped only for epics, not disabled: a completable verb on
    # a plain task still completes.
    aid=$(add_task_get_id "Audit the qux module")
    assert_succeeds "plain task 'Audit …' still completes (verb gate intact)" \
        endless task complete "${aid}" --outcome "findings"
}

test_epic_other_gates_intact() {
    section "Regression — epic's outcome + terminal gates stay enforced"

    # Drive the outcome gate via `task update` — `task complete` enforces
    # --outcome at the CLI arg layer before the gate runs; `task update
    # --status completed` reaches _require_outcome_for_completed directly.
    local eid
    eid=$(add_task_get_id "Implement the outcome-required epic" --type epic)
    assert_refused "epic completed still requires an outcome" \
        "outcome is required" \
        endless task update "${eid}" --status completed

    eid=$(add_task_get_id "Implement the terminal-gated epic" --type epic)
    assert_refused "epic still rejects 'assumed'/'confirmed' (E-1577)" \
        "'confirmed'" \
        endless task update "${eid}" --status confirmed
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

    if ! command -v uv >/dev/null 2>&1; then
        printf 'ERROR: uv not on PATH\n' >&2
        exit 2
    fi

    printf '%sE-1617 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_unit_suite
    test_epic_completes_via_complete
    test_epic_completes_via_update
    test_non_epic_still_gated
    test_epic_other_gates_intact

    summary
}

main "$@"
