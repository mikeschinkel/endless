#!/usr/bin/env bash
#
# E-1755 verification script — confirms the obsolete `.endless/sessions/`
# companion-file path is fully pruned and the live session commands still work
# off the DB + tmux (E-1426 sourcing).
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1755-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error. Fail-fast: the first failed check still lets the
# rest run, but the summary exit code gates the handoff.
#
# Checks:
#   A (static)     — no `.endless/sessions` reference remains in src/endless/
#   B (static)     — `.endless/sessions/` is not in the repo .gitignore
#   C (behavioral) — `endless session list` runs with no Python traceback
#                    (exercises the DB+tmux `_live_sessions` path)
#   D (behavioral) — `endless session show <bogus>` fails gracefully: no
#                    traceback, no `.endless/sessions` mention (exercises
#                    session_show_resolve / _resolve_companion)

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

# ─── checks ─────────────────────────────────────────────────────────────────

check_a_no_src_reference() {
    section "A. No .endless/sessions reference remains in src/endless/"
    local hits
    hits=$(grep -rn '\.endless/sessions' src/endless/ 2>/dev/null || true)
    if [[ -z "${hits}" ]]; then
        report_pass "src/endless/ has zero \`.endless/sessions\` references"
    else
        report_fail "src/endless/ still references \`.endless/sessions\`" \
            "no matches" "${hits}"
    fi
}

check_b_not_in_gitignore() {
    section "B. .endless/sessions/ is not in the repo .gitignore"
    local hits
    hits=$(grep -n '\.endless/sessions' .gitignore 2>/dev/null || true)
    if [[ -z "${hits}" ]]; then
        report_pass ".gitignore no longer ignores \`.endless/sessions/\`"
    else
        report_fail ".gitignore still carries a \`.endless/sessions/\` entry" \
            "no matches" "${hits}"
    fi
}

check_c_session_list_runs() {
    section "C. \`endless session list\` runs off DB+tmux (no traceback)"
    local out
    out=$(endless session list 2>&1)
    if grep -q 'Traceback (most recent call last)' <<<"${out}"; then
        report_fail "\`session list\` raised a Python traceback" \
            "no traceback" "$(printf '%s' "${out}" | tail -3)"
    else
        report_pass "\`session list\` completed without a traceback"
    fi
}

check_d_session_show_graceful() {
    section "D. \`endless session show <bogus>\` fails gracefully"
    local out
    out=$(endless session show e-999999999 2>&1)
    local bad=0
    if grep -q 'Traceback (most recent call last)' <<<"${out}"; then
        report_fail "\`session show\` raised a Python traceback" \
            "no traceback" "$(printf '%s' "${out}" | tail -3)"
        bad=1
    fi
    if grep -q '\.endless/sessions' <<<"${out}"; then
        report_fail "\`session show\` output mentions the pruned files dir" \
            "no \`.endless/sessions\` mention" "${out}"
        bad=1
    fi
    if [[ "${bad}" -eq 0 ]]; then
        report_pass "\`session show\` failed gracefully (no traceback, no files-dir mention)"
    fi
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

    printf '%sE-1755 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    check_a_no_src_reference
    check_b_not_in_gitignore
    check_c_session_list_runs
    check_d_session_show_graceful

    summary
}

main "$@"
