#!/usr/bin/env bash
#
# E-1806 verification script — confirms the `completed` status-gate refusal (and
# the `task complete --help` docstring) drop internal jargon and speak plain
# language.
#
# Background: attempting `completed` on an implementation task (whose title's
# lead verb is not "completable") used to print a long, technical error that
# leaked internal mechanism — `verbs.json`, `completable: true`, "lead verb".
# The gate BEHAVIOR is unchanged; only the wording changed. This script pins
# both the new wording and the ABSENCE of the jargon, so a regression that
# reintroduces the leak fails loudly.
#
# Run from anywhere inside the worktree (esu cd's here and exports the session):
#   esu && ./tests/tasks/e-1806-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on environment/setup error. A fresh task id is allocated per run;
# the script does NOT wipe the sandbox between runs (pollution is bounded and
# inspectable via `uv run endless task list --all --db sandbox`).

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

# Wrap the CLI so every invocation routes through the sandbox DB and runs the
# WORKTREE source. When ENDLESS_SESSION_ID is unset (script run without `esu`),
# add --no-session so write commands don't hit the session-attribution gate.
endless() {
    if [[ -n "${ENDLESS_SESSION_ID:-}" ]]; then
        uv run endless "$@" --db sandbox
    else
        uv run endless "$@" --db sandbox --no-session
    fi
}

# Create a task and emit just its E-NNN id on stdout; other output to stderr.
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

# assert_refused DESC PATTERN CMD [ARGS...] — pass iff non-zero exit AND output
# contains PATTERN.
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

# assert_refused_lacks DESC PATTERN CMD [ARGS...] — pass iff non-zero exit AND
# output does NOT contain PATTERN. Used to pin the ABSENCE of leaked jargon on
# the refusal path (a bare not-contains would also pass on a success exit).
assert_refused_lacks() {
    local desc="$1"
    local pattern="$2"
    shift 2
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -ne 0 ]] && [[ "${output}" != *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" \
        "exit != 0 AND output does NOT contain: ${pattern}" \
        "exit=${rc} | output=${output}"
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
    report_fail "${desc}" "output does NOT contain: ${pattern}" "${output}"
}

# ─── 1. refusal wording ─────────────────────────────────────────────────────

test_refusal_wording() {
    section "Refusal — plain-language wording, gate behavior unchanged"

    # Title verb 'Add' is registered but NOT completable, so a plain
    # implementation task cannot reach 'completed'. The gate still fires; only
    # the wording changed.
    local tid
    tid=$(add_task_get_id "Add a status-gate wording widget" --type task) || return

    assert_refused "refusal states 'completed' isn't a valid final status" \
        "valid final status" \
        endless task update "${tid}" --status completed --outcome "x"
    assert_refused "refusal names the correct final statuses" \
        "'confirmed' or 'assumed'" \
        endless task update "${tid}" --status completed --outcome "x"
}

# ─── 2. no leaked jargon on the refusal ──────────────────────────────────────

test_refusal_no_jargon() {
    section "Refusal — no internal mechanism leaked"

    local tid
    tid=$(add_task_get_id "Add a jargon-check widget" --type task) || return

    assert_refused_lacks "refusal does not mention verbs.json" \
        "verbs.json" \
        endless task update "${tid}" --status completed --outcome "x"
    assert_refused_lacks "refusal does not mention 'completable'" \
        "completable" \
        endless task update "${tid}" --status completed --outcome "x"
    assert_refused_lacks "refusal does not mention 'lead verb'" \
        "lead verb" \
        endless task update "${tid}" --status completed --outcome "x"
}

# ─── 3. --help docstring is de-jargoned ──────────────────────────────────────

test_help_no_jargon() {
    section "Help — 'task complete --help' drops the same jargon"

    assert_not_contains "help text does not mention verbs.json" \
        "verbs.json" \
        endless task complete --help
    assert_not_contains "help text does not mention 'completable: true'" \
        "completable: true" \
        endless task complete --help
    assert_not_contains "help text does not mention 'lead verb'" \
        "lead verb" \
        endless task complete --help
    # Still explains the confirmed/assumed track for implementation tasks.
    assert_contains "help text points implementation tasks at confirmed/assumed" \
        "confirmed" \
        endless task complete --help
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

    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }

    printf '%sE-1806 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_refusal_wording
    test_refusal_no_jargon
    test_help_no_jargon

    summary
}

main "$@"
