#!/usr/bin/env bash
#
# E-1579 verification script — exercises the type-aware status-transition gate
# end-to-end against the worktree's sandbox DB.
#
# Gate (src/endless/task_cmd.py): research and epic tasks reject
# 'verify'/'assumed'/'confirmed'; their only type-specific terminal is
# 'completed'. Universal terminals 'obsolete'/'declined' stay allowed for all
# types. The gate is a hard type-correctness invariant — no --force bypass.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1579-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure. Each new task gets a fresh ID; the script does NOT wipe the sandbox
# between runs (pollution is bounded and inspectable via
#   uv run endless task list --db sandbox).
#
# Ad-hoc per-task verify script in the shape of tests/tasks/e-1577-verify.sh —
# the reference prototype the E-1596 verification-suite epic points to. Not a
# consumer of E-1596's (not-yet-built) framework.

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

# ─── type-aware status gate ──────────────────────────────────────────────────

test_status_gate() {
    section "Type-aware status gate (research/epic reject verify)"

    local rid eid tid bid

    # ── verify rejected for research/epic via update ────────────────────────
    eid=$(add_task_get_id "Implement epic gate-a" --type epic)
    assert_refused "epic rejects 'task update --status verify'" \
        "'verify'" endless task update "${eid}" --status verify

    rid=$(add_task_get_id "Research gate-a" \
        --type research --justification "smoke")
    assert_refused "research rejects 'task update --status verify'" \
        "'verify'" endless task update "${rid}" --status verify

    # ── verify rejected for research/epic via add ───────────────────────────
    assert_refused "epic rejects 'task add --status verify'" \
        "'verify'" \
        endless task add "Implement epic gate-b" --type epic --status verify

    assert_refused "research rejects 'task add --status verify'" \
        "'verify'" \
        endless task add "Research gate-b" \
            --type research --justification "smoke" --status verify

    # ── verify allowed for task/bug (no regression on normal types) ─────────
    tid=$(add_task_get_id "Implement plain gate-verify")
    assert_succeeds "plain task accepts 'task update --status verify'" \
        endless task update "${tid}" --status verify

    bid=$(add_task_get_id "Fix bug gate-verify" --type bug)
    assert_succeeds "bug accepts 'task update --status verify'" \
        endless task update "${bid}" --status verify

    assert_succeeds "plain task accepts 'task add --status verify'" \
        endless task add "Implement plain gate-add" --status verify

    assert_succeeds "bug accepts 'task add --status verify'" \
        endless task add "Fix bug gate-add" --type bug --status verify

    # ── E-1577 inheritance: assumed/confirmed still rejected ────────────────
    eid=$(add_task_get_id "Implement epic gate-c" --type epic)
    assert_refused "epic still rejects 'task confirm' (E-1577)" \
        "'confirmed'" endless task confirm "${eid}"

    rid=$(add_task_get_id "Research gate-c" \
        --type research --justification "smoke")
    assert_refused "research still rejects 'task assume' (E-1577)" \
        "'assumed'" endless task assume "${rid}"

    # ── type-specific + universal terminals still accepted ──────────────────
    rid=$(add_task_get_id "Research gate-completed-happy" \
        --type research --justification "smoke")
    assert_succeeds "research accepts 'task complete --outcome'" \
        endless task complete "${rid}" --outcome "findings"

    eid=$(add_task_get_id "Implement epic gate-obsolete" --type epic)
    assert_succeeds "epic accepts universal terminal 'obsolete'" \
        endless task update "${eid}" --status obsolete
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

    printf '%sE-1579 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_status_gate

    summary
}

main "$@"
