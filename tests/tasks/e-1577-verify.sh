#!/usr/bin/env bash
#
# E-1577 verification script — exercises the three lifecycle bugs end-to-end
# against the worktree's sandbox DB.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1577-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure. Each new task gets a fresh ID; the script does NOT wipe the sandbox
# between runs (pollution is bounded and inspectable via
#   uv run endless task list --db sandbox).
#
# This is the ad-hoc prototype referenced by E-1596 (formalization task).

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

# assert_contains DESC PATTERN CMD [ARGS...]
assert_contains() {
    local desc="$1"
    local pattern="$2"
    shift 2
    local output
    output=$("$@" 2>&1)
    if [[ "${output}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output contains: ${pattern}" "${output}"
}

# assert_not_contains DESC PATTERN CMD [ARGS...]
assert_not_contains() {
    local desc="$1"
    local pattern="$2"
    shift 2
    local output
    output=$("$@" 2>&1)
    if [[ "${output}" != *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" \
        "output does NOT contain: ${pattern}" \
        "${output}"
}

# assert_ordering DESC FIRST SECOND CMD [ARGS...]
#   Pass if both FIRST and SECOND appear in output AND SECOND appears AFTER FIRST.
assert_ordering() {
    local desc="$1"
    local first="$2"
    local second="$3"
    shift 3
    local output first_pos second_pos
    output=$("$@" 2>&1)
    first_pos=$(printf '%s\n' "${output}" \
                | grep -n -F -- "${first}" \
                | head -1 | cut -d: -f1)
    second_pos=$(printf '%s\n' "${output}" \
                 | grep -n -F -- "${second}" \
                 | head -1 | cut -d: -f1)
    if [[ -n "${first_pos}" ]] \
        && [[ -n "${second_pos}" ]] \
        && [[ "${second_pos}" -gt "${first_pos}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" \
        "'${first}' before '${second}'" \
        "first_pos=${first_pos:-MISSING} second_pos=${second_pos:-MISSING} | output=${output}"
}

# ─── bug 1: research/epic terminal gate ─────────────────────────────────────

test_bug1_terminal_gate() {
    section "Bug 1 — Research/epic terminal gate (assumed/confirmed refused)"

    local rid eid pid cid

    rid=$(add_task_get_id "Research bug1-a" \
        --type research --justification "smoke")
    assert_refused "research rejects 'task confirm'" \
        "'confirmed'" endless task confirm "${rid}"

    rid=$(add_task_get_id "Research bug1-b" \
        --type research --justification "smoke")
    assert_refused "research rejects 'task assume'" \
        "'assumed'" endless task assume "${rid}"

    rid=$(add_task_get_id "Research bug1-c" \
        --type research --justification "smoke")
    assert_refused "research rejects 'task update --status confirmed'" \
        "'confirmed'" endless task update "${rid}" --status confirmed

    rid=$(add_task_get_id "Research bug1-d" \
        --type research --justification "smoke")
    assert_refused "research rejects 'task update --status assumed'" \
        "'assumed'" endless task update "${rid}" --status assumed

    assert_refused "research rejects 'task add --status confirmed'" \
        "'confirmed'" \
        endless task add "Research bug1-e" \
            --type research --justification "smoke" --status confirmed

    eid=$(add_task_get_id "Build epic bug1-a" --type epic)
    assert_refused "epic rejects 'task confirm'" \
        "'confirmed'" endless task confirm "${eid}"

    eid=$(add_task_get_id "Build epic bug1-b" --type epic)
    assert_refused "epic rejects 'task assume'" \
        "'assumed'" endless task assume "${eid}"

    # ── happy paths (no regression on other types) ──────────────────────────
    pid=$(add_task_get_id "Implement plain-confirm")
    assert_succeeds "plain task accepts 'task confirm'" \
        endless task confirm "${pid}"

    pid=$(add_task_get_id "Implement plain-assume")
    assert_succeeds "plain task accepts 'task assume'" \
        endless task assume "${pid}"

    rid=$(add_task_get_id "Research bug1-completed-happy" \
        --type research --justification "smoke")
    assert_succeeds "research accepts 'task complete --outcome'" \
        endless task complete "${rid}" --outcome "findings"

    rid=$(add_task_get_id "Research bug1-obsolete-universal" \
        --type research --justification "smoke")
    assert_succeeds "research accepts universal terminal 'obsolete'" \
        endless task update "${rid}" --status obsolete

    # ── cascade refusal ─────────────────────────────────────────────────────
    pid=$(add_task_get_id "Implement cascade-parent")
    cid=$(add_task_get_id "Research cascade-child" \
        --type research --justification "smoke" --parent "${pid}")
    assert_refused "'confirm --cascade' refuses research descendant by id" \
        "${cid}" endless task confirm "${pid}" --cascade
}

# ─── bug 2: outcome-required check uses merged value ────────────────────────

test_bug2_outcome_merge() {
    section "Bug 2 — Outcome-required check uses merged (existing+new) value"

    local tid

    tid=$(add_task_get_id "Audit bug2-existing-satisfies")
    endless task update "${tid}" --outcome "draft findings" >/dev/null 2>&1
    assert_succeeds "existing DB outcome satisfies 'update --status completed'" \
        endless task update "${tid}" --status completed

    tid=$(add_task_get_id "Audit bug2-still-refused")
    assert_refused "still refused when neither existing nor new outcome" \
        "outcome is required" \
        endless task update "${tid}" --status completed
}

# ─── bug 3: outcome renders as section after text ───────────────────────────

test_bug3_outcome_section() {
    section "Bug 3 — 'task show --outcome' renders section after Text"

    local tid text_file
    tid=$(add_task_get_id "Audit bug3-section-render")
    endless task update "${tid}" --outcome "rendered outcome body" \
        >/dev/null 2>&1

    assert_contains "'task show --outcome' emits '— Outcome —' section header" \
        "— Outcome —" \
        endless task show "${tid}" --outcome

    assert_not_contains "no more inline 'Outcome:' label/value field" \
        "Outcome:" \
        endless task show "${tid}" --outcome

    # Need text content for the ordering check.
    text_file=$(mktemp)
    printf 'body text content\n' > "${text_file}"
    endless task update "${tid}" --text "${text_file}" >/dev/null 2>&1
    rm -f "${text_file}"
    assert_ordering "'— Outcome —' renders AFTER '— Text —'" \
        "— Text —" "— Outcome —" \
        endless task show "${tid}" --text --outcome
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

    printf '%sE-1577 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_bug1_terminal_gate
    test_bug2_outcome_merge
    test_bug3_outcome_section

    summary
}

main "$@"
