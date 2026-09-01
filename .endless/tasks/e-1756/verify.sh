#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1756 and records what was true when E-1756
# landed. Edit it only if you ARE E-1756. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1756 verification script — the project-management verbs moved under a new
# `project` command group. Exercises end-to-end against the worktree's sandbox DB.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1756
#
# Asserts:
#   1. `endless project <cmd> --help` works for each moved command.
#   2. `endless project --help` lists every moved subcommand.
#   3. Each old top-level name exits non-zero with a message naming the new
#      command (hidden hard-error stub).
#   4. No old top-level name is listed by `endless --help`.
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error. Reads no ledger; --help checks touch no DB.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

# The nine verbs moved under `project` (E-1756).
MOVED_COMMANDS=(register unregister purge set rename list status scan discover)

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

# assert_not_listed DESC CMD
#   Pass if `endless --help` does NOT list CMD as a top-level command. Matches
#   the command column (leading whitespace + the exact name at a word boundary)
#   so a substring like "register" inside "Manage registered projects" does not
#   false-trip.
assert_not_listed() {
    local desc="$1"
    local cmd="$2"
    local output
    output=$(uv run endless --help 2>&1)
    if printf '%s\n' "${output}" | grep -qE "^[[:space:]]+${cmd}([[:space:]]|$)"; then
        report_fail "${desc}" \
            "'${cmd}' NOT in 'endless --help' command list" \
            "found a command-list line for '${cmd}'"
        return
    fi
    report_pass "${desc}"
}

# ─── moved commands are reachable under `project` ───────────────────────────

test_moved_commands_reachable() {
    section "Moved commands reachable at 'endless project <cmd>'"

    local cmd
    for cmd in "${MOVED_COMMANDS[@]}"; do
        assert_succeeds "endless project ${cmd} --help works" \
            endless project "${cmd}" --help
    done
}

# ─── project group lists every moved subcommand ─────────────────────────────

test_group_lists_subcommands() {
    section "'endless project --help' lists every moved subcommand"

    local cmd
    for cmd in "${MOVED_COMMANDS[@]}"; do
        assert_contains "'project --help' lists '${cmd}'" \
            "${cmd}" endless project --help
    done
}

# ─── old top-level names hard-error with a redirect ─────────────────────────

test_old_names_refused() {
    section "Old top-level names exit non-zero and name the new command"

    local cmd
    for cmd in "${MOVED_COMMANDS[@]}"; do
        assert_refused "'endless ${cmd}' redirects to 'endless project ${cmd}'" \
            "endless project ${cmd}" \
            endless "${cmd}"
    done
}

# ─── old names hidden from top-level --help ─────────────────────────────────

test_old_names_hidden() {
    section "Old top-level names are absent from 'endless --help'"

    local cmd
    for cmd in "${MOVED_COMMANDS[@]}"; do
        assert_not_listed "'endless --help' does not list '${cmd}'" "${cmd}"
    done

    # The group itself IS listed.
    assert_contains "'endless --help' lists the 'project' group" \
        "project" uv run endless --help
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

    printf '%sE-1756 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_moved_commands_reachable
    test_group_lists_subcommands
    test_old_names_refused
    test_old_names_hidden

    summary
}

main "$@"
