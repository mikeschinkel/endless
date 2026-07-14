#!/usr/bin/env bash
#
# E-1782 verification — `endless guide tasks` documents the `endless task report`
# command and the report-by-default posture. Before this task the guide had zero
# mention of the command or the posture; a non-spawned session (or any session
# reading the guide rather than a spawn handoff) never learned it exists.
#
# Run from anywhere inside the worktree:
#   esu
#   ./tests/tasks/e-1782-verify.sh
#
# Strategy: doc-only change. `endless guide <section>` just cats
# docs/guide/<section>.md (cli.py `guide()` → click.echo(target.read_text())),
# so grepping the worktree source markdown is equivalent to grepping the rendered
# guide. The cross-reference (command → section) is validated two ways: the real
# `--help` agent directive rendered from the worktree source, and `just
# guide-check`. Nothing touches the real ledger, DB, or this worktree's branch.
#
# What it checks:
#   1. docs/guide/tasks.md carries the new "## Reporting to your user" section,
#      names `endless task report`, states the relay-verbatim + no-payload
#      posture, and points at `endless task report --help` for the payload shape.
#   2. tasks.md does NOT inline the --json payload schema (deferred to --help).
#   3. docs/guide/index.md happy path names `endless task report`.
#   4. Cross-reference resolves: `endless task report --help` prints the
#      `endless guide tasks` agent directive, and `just guide-check` exits 0.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 setup error.

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'
    BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }

report_pass() {
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" \
        "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_contains DESC HAYSTACK NEEDLE
assert_contains() {
    local desc="$1" hay="$2" needle="$3"
    if [[ "${hay}" == *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "contains: ${needle}" "${hay}"
}

# assert_not_contains DESC HAYSTACK NEEDLE
assert_not_contains() {
    local desc="$1" hay="$2" needle="$3"
    if [[ "${hay}" != *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "does NOT contain: ${needle}" "${hay}"
}

# ─── setup ──────────────────────────────────────────────────────────────────

REPO_ROOT=""
TASKS_MD=""
INDEX_MD=""

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    TASKS_MD="${REPO_ROOT}/docs/guide/tasks.md"
    INDEX_MD="${REPO_ROOT}/docs/guide/index.md"
    [[ -f "${TASKS_MD}" ]] || { printf 'ERROR: %s missing\n' "${TASKS_MD}" >&2; exit 2; }
    [[ -f "${INDEX_MD}" ]] || { printf 'ERROR: %s missing\n' "${INDEX_MD}" >&2; exit 2; }
}

# ─── checks ─────────────────────────────────────────────────────────────────

check_tasks_content() {
    section "1 — docs/guide/tasks.md documents the report command + posture"
    local body
    body=$(cat "${TASKS_MD}")
    assert_contains "has the '## Reporting to your user' section" \
        "${body}" "## Reporting to your user"
    assert_contains "names the report command" \
        "${body}" "endless task report <id>"
    assert_contains "states relay-verbatim posture" \
        "${body}" "verbatim"
    assert_contains "states the normal path takes no payload" \
        "${body}" "no payload"
    assert_contains "points at the command's --help for the payload shape" \
        "${body}" "endless task report --help"
}

check_no_schema_dup() {
    section "2 — the --json payload schema is NOT duplicated in the guide"
    local body
    body=$(cat "${TASKS_MD}")
    # The canonical schema (in `task report --help`) uses these tokens; their
    # absence here proves the guide defers rather than inlining the shape.
    assert_not_contains "does not inline the anomaly|discovery enum" \
        "${body}" "anomaly|discovery"
    assert_not_contains "does not inline the notes/questions JSON keys" \
        "${body}" '"notes":'
}

check_index_pointer() {
    section "3 — index.md happy path names the report command"
    local body
    body=$(cat "${INDEX_MD}")
    assert_contains "happy path names endless task report" \
        "${body}" "endless task report <id>"
}

check_cross_reference() {
    section "4 — cross-reference resolves 'task report' → tasks section"
    local help_out rc
    # Render the real --help directive from the WORKTREE source (editable global
    # install points at main, so route explicitly through this checkout).
    help_out=$(cd "${REPO_ROOT}" && uv run --directory "${REPO_ROOT}" \
        endless task report --help 2>/dev/null)
    assert_contains "'endless task report --help' steers to the tasks guide" \
        "${help_out}" "endless guide tasks"

    section "4b — guide map + generated index are in sync"
    local check_out
    check_out=$(cd "${REPO_ROOT}" && just guide-check 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "just guide-check exits 0"
    else report_fail "just guide-check exits 0" "exit 0" "exit ${rc}
${check_out}"; fi
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup

    printf '%sE-1782 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  repo:    %s\n' "${REPO_ROOT}"

    check_tasks_content
    check_no_schema_dup
    check_index_pointer
    check_cross_reference

    summary
}

main "$@"
