#!/usr/bin/env bash
#
# E-1772 verification — the wind-down report reminder.
#
# When an agent sets a task to a terminal wind-down status, the status-update
# command prints a reminder to route this handoff (and all further reporting
# for the rest of the session) through `endless task report <id>`. It
# fires ONLY on underway->unverified, ->assumed, and ->completed (with an
# outcome); NOT on submitted/ready/confirmed, the claim's ->underway, or the
# revisit/declined/obsolete management transitions.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1772-verify.sh
#
# Fail-fast: the pytest unit suite (tests/test_report_reminder.py) runs first
# and aborts the script on any failure; the end-to-end sandbox checks below
# then exercise the real CLI. Exit 0 on all-passed, 1 on any failure.

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

# Route every CLI invocation through the worktree source and the sandbox DB.
endless() {
    uv run endless "$@" --db sandbox
}

# The pointer that proves the reminder fired.
MARKER="endless task report"

# Create a task and emit just its E-NNN id on stdout.
add_task_get_id() {
    local title="$1"
    shift
    local output
    output=$(endless task add "${title}" "$@" 2>&1)
    if [[ $? -ne 0 ]]; then
        printf 'ERROR: add failed for %q: %s\n' "${title}" "${output}" >&2
        return 1
    fi
    printf '%s\n' "${output}" | grep -oE 'E-[0-9]+' | head -1
}

# Drive a fresh task to `underway` and echo its id (all noise to stderr).
underway_task() {
    local title="$1"
    local tid
    tid=$(add_task_get_id "${title}") || return 1
    endless task update "${tid}" --status ready >/dev/null 2>&1
    endless task update "${tid}" --status underway >/dev/null 2>&1
    printf '%s\n' "${tid}"
}

# ─── assertions ─────────────────────────────────────────────────────────────
#
# The reminder fires exactly once, on the transition itself; re-running the
# command would be a no-op that never re-fires. So every check captures the
# transition's output ONCE (into a string) and asserts substrings against it.

# assert_str_contains DESC PATTERN STRING
assert_str_contains() {
    local desc="$1"
    local pattern="$2"
    local output="$3"
    if [[ "${output}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output contains: ${pattern}" "${output}"
}

# assert_str_not_contains DESC PATTERN STRING
assert_str_not_contains() {
    local desc="$1"
    local pattern="$2"
    local output="$3"
    if [[ "${output}" != *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output does NOT contain: ${pattern}" "${output}"
}

# ─── the reminder fires on wind-down transitions ──────────────────────────────

test_fires_on_wind_down() {
    section "Fires on agent wind-down transitions"

    local tid out
    tid=$(underway_task "Fix the e1772 unverified path")
    out=$(endless task update "${tid}" --status unverified 2>&1)
    assert_str_contains "underway->unverified emits the report pointer" \
        "${MARKER}" "${out}"
    assert_str_contains "pointer carries the task id" \
        "endless task report ${tid}" "${out}"

    tid=$(underway_task "Fix the e1772 assume path")
    out=$(endless task assume "${tid}" --outcome "believed correct" 2>&1)
    assert_str_contains "'task assume' emits the report pointer" \
        "${MARKER}" "${out}"

    tid=$(underway_task "Fix the e1772 update-assumed path")
    out=$(endless task update "${tid}" --status assumed --outcome "believed correct" 2>&1)
    assert_str_contains "update --status assumed emits the report pointer" \
        "${MARKER}" "${out}"

    tid=$(underway_task "Audit the e1772 complete path")
    out=$(endless task complete "${tid}" --outcome "findings: none material" 2>&1)
    assert_str_contains "'task complete' emits the report pointer" \
        "${MARKER}" "${out}"
}

# ─── the reminder stays silent on excluded transitions ────────────────────────

test_silent_on_excluded() {
    section "Silent on excluded transitions"

    local tid out
    tid=$(add_task_get_id "Fix the e1772 claim path")
    endless task update "${tid}" --status ready >/dev/null 2>&1
    out=$(endless task update "${tid}" --status underway 2>&1)
    assert_str_not_contains "ready->underway (claim) stays silent" \
        "${MARKER}" "${out}"

    tid=$(underway_task "Fix the e1772 confirm path")
    endless task update "${tid}" --status unverified >/dev/null 2>&1
    out=$(endless task confirm "${tid}" 2>&1)
    assert_str_not_contains "unverified->confirmed (user verify) stays silent" \
        "${MARKER}" "${out}"

    tid=$(add_task_get_id "Fix the e1772 submit path")
    out=$(endless task submit "${tid}" 2>&1)
    assert_str_not_contains "unplanned->submitted stays silent" \
        "${MARKER}" "${out}"

    tid=$(underway_task "Fix the e1772 decline path")
    out=$(endless task decline "${tid}" --reason "not doing it" 2>&1)
    assert_str_not_contains "underway->declined stays silent" \
        "${MARKER}" "${out}"
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

    printf '%sE-1772 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'

    # Fail-fast: the unit suite must be green before the e2e checks run.
    section "Unit suite (fail-fast) — tests/test_report_reminder.py"
    if uv run pytest tests/test_report_reminder.py -q; then
        report_pass "pytest tests/test_report_reminder.py"
    else
        report_fail "pytest tests/test_report_reminder.py" \
            "all unit tests pass" "see pytest output above"
        summary
        exit 1
    fi

    test_fires_on_wind_down
    test_silent_on_excluded

    summary
}

main "$@"
