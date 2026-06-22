#!/usr/bin/env bash
#
# E-1540 verification script — exercises the `endless epic` CLI subcommand
# group (add / list / show / update) end-to-end against the worktree's
# sandbox DB.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1540-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error (not a git worktree / uv missing). Each new task
# gets a fresh ID; the script does NOT wipe the sandbox between runs
# (pollution is bounded and inspectable via
#   uv run endless task list --db sandbox).
#
# Shape/output mirrors the E-1577 prototype referenced by E-1596 (the
# verification-suite formalization task).

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

# Wrap the CLI so every invocation routes through the sandbox DB and the
# worktree's source (uv run resolves the editable install in this worktree).
endless() {
    uv run endless "$@" --db sandbox
}

# Create a task via `endless epic add` and emit just its E-NNN id on stdout.
# All other output goes to stderr so callers can capture only the id.
add_epic_get_id() {
    local title="$1"
    shift
    local output rc
    output=$(endless epic add "${title}" "$@" 2>&1)
    rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        printf 'ERROR: epic add failed for %q: %s\n' "${title}" "${output}" >&2
        return 1
    fi
    printf '%s\n' "${output}" | grep -oE 'E-[0-9]+' | head -1
}

# Create a plain (type=task) task and emit just its E-NNN id on stdout.
add_task_get_id() {
    local title="$1"
    shift
    local output rc
    output=$(endless task add "${title}" "$@" 2>&1)
    rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        printf 'ERROR: task add failed for %q: %s\n' "${title}" "${output}" >&2
        return 1
    fi
    printf '%s\n' "${output}" | grep -oE 'E-[0-9]+' | head -1
}

# Read a task's current type (the bare value from the `Type:` line).
task_type() {
    endless task show "$1" 2>&1 | grep -E '^Type:' | head -1 | awk '{print $2}'
}

# Read a task's current phase (the bare value from the `Phase:` line).
task_phase() {
    endless task show "$1" 2>&1 | grep -E '^Phase:' | head -1 | awk '{print $2}'
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_refused DESC PATTERN CMD [ARGS...]
#   Pass if CMD exits non-zero AND its combined output contains PATTERN.
assert_refused() {
    local desc="$1"
    local pattern="$2"
    shift 2
    local output rc
    output=$("$@" 2>&1)
    rc=$?
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
    local output rc
    output=$("$@" 2>&1)
    rc=$?
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

# assert_eq DESC WANT GOT
assert_eq() {
    local desc="$1"
    local want="$2"
    local got="$3"
    if [[ "${got}" == "${want}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "${want}" "${got:-<empty>}"
}

# ─── §1: epic add pins type=epic, drops --type ──────────────────────────────

test_epic_add() {
    section "§1 — epic add (pins type=epic; no --type flag)"

    local eid
    eid=$(add_epic_get_id "Build the reporting epic") || exit 2
    assert_eq "epic add creates a type=epic row" "epic" "$(task_type "${eid}")"

    assert_refused "epic add has no --type flag" \
        "No such option: --type" \
        endless epic add "Build an override epic" --type task
}

# ─── §2: epic list filters to epic-typed rows ───────────────────────────────

test_epic_list() {
    section "§2 — epic list (filters to epic-typed rows; --json round-trips)"

    local eid tid
    eid=$(add_epic_get_id "Migrate the auth subsystem epic") || exit 2
    tid=$(add_task_get_id "Implement the login form") || exit 2

    assert_contains "epic list includes the epic id" \
        "${eid}" \
        endless epic list --project endless
    assert_not_contains "epic list excludes the plain task id" \
        "${tid}" \
        endless epic list --project endless

    assert_contains "epic list --json includes the epic id" \
        "\"${eid}\"" \
        endless epic list --project endless --json
    assert_not_contains "epic list --json excludes the plain task id" \
        "\"${tid}\"" \
        endless epic list --project endless --json
}

# ─── §3: epic show children default-on / --no-children ──────────────────────

test_epic_show() {
    section "§3 — epic show (children shown by default; --no-children hides)"

    local eid cid
    eid=$(add_epic_get_id "Build the dashboard epic") || exit 2
    cid=$(add_task_get_id "Add a child widget task" --parent "${eid}") || exit 2

    assert_contains "epic show lists children by default" \
        "${cid}" \
        endless epic show "${eid}"
    assert_not_contains "epic show --no-children hides children" \
        "${cid}" \
        endless epic show "${eid}" --no-children
}

# ─── §4: epic update promotes to epic + passes non-type fields through ───────

test_epic_update() {
    section "§4 — epic update (promotes type to epic; passes other fields; no --type)"

    local tid eid
    tid=$(add_task_get_id "Implement the export pipeline") || exit 2
    assert_eq "fixture starts as a plain task" "task" "$(task_type "${tid}")"

    assert_succeeds "epic update on a plain task succeeds" \
        endless epic update "${tid}" --status ready
    assert_eq "epic update promoted the task to epic" \
        "epic" "$(task_type "${tid}")"

    eid=$(add_epic_get_id "Refactor the billing epic") || exit 2
    assert_succeeds "epic update passes non-type fields through" \
        endless epic update "${eid}" --phase next --description "Reworked scope"
    assert_eq "non-type field (phase) applied" "next" "$(task_phase "${eid}")"
    assert_contains "non-type field (description) applied" \
        "Reworked scope" \
        endless epic show "${eid}"
    assert_eq "type stays epic after a non-type update" \
        "epic" "$(task_type "${eid}")"

    assert_refused "epic update has no --type flag" \
        "No such option: --type" \
        endless epic update "${eid}" --type task
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

    printf '%sE-1540 verification — endless epic subcommand%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_epic_add
    test_epic_list
    test_epic_show
    test_epic_update

    summary
}

main "$@"
