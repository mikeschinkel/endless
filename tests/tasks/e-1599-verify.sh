#!/usr/bin/env bash
#
# E-1599 verification script — exercises the analysis-rendering surface and the
# --all-fields flag end-to-end against the worktree's sandbox DB.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1599-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure. Each new task gets a fresh ID; the script does NOT wipe the sandbox
# between runs (pollution is bounded and inspectable via
#   uv run endless task list --db sandbox).
#
# Shape borrowed from the E-1577 ad-hoc prototype (the formalization task is
# E-1596); this is a per-task verify script, not a deliverable of E-1596.

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

# Echo a string of N copies of 'a' (for length-limit triggers).
repeat_a() {
    local n="$1"
    printf 'a%.0s' $(seq 1 "${n}")
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

# ─── 1: analysis section renders / is gated ─────────────────────────────────

test_analysis_section() {
    section "1 — 'task show --analysis' renders an Analysis section"

    local tid
    tid=$(add_task_get_id "Audit analysis-render")
    endless task update "${tid}" --analysis "rendered analysis body" \
        >/dev/null 2>&1

    assert_contains "'task show --analysis' emits '— Analysis —' header" \
        "— Analysis —" \
        endless task show "${tid}" --analysis

    assert_contains "'task show --analysis' emits the analysis body" \
        "rendered analysis body" \
        endless task show "${tid}" --analysis

    assert_not_contains "default 'task show' omits the Analysis section" \
        "— Analysis —" \
        endless task show "${tid}"
}

# ─── 2: ordering — Analysis precedes Text ───────────────────────────────────

test_analysis_before_text() {
    section "2 — Analysis renders BEFORE Text (pre-plan design content)"

    local tid text_file
    tid=$(add_task_get_id "Audit analysis-ordering")
    endless task update "${tid}" --analysis "ordering analysis body" \
        >/dev/null 2>&1
    text_file=$(mktemp)
    printf 'ordering plan body\n' > "${text_file}"
    endless task update "${tid}" --text "${text_file}" >/dev/null 2>&1
    rm -f "${text_file}"

    assert_ordering "'— Analysis —' renders BEFORE '— Text —'" \
        "— Analysis —" "— Text —" \
        endless task show "${tid}" --analysis --text
}

# ─── 3: --all-fields turns on every content section ─────────────────────────

test_all_fields() {
    section "3 — '--all-fields' shows every content section"

    local tid text_file child
    tid=$(add_task_get_id "Audit all-fields" --description "a short blurb")
    endless task update "${tid}" --analysis "all-fields analysis body" \
        >/dev/null 2>&1
    text_file=$(mktemp)
    printf 'all-fields plan body\n' > "${text_file}"
    endless task update "${tid}" --text "${text_file}" >/dev/null 2>&1
    rm -f "${text_file}"
    endless task update "${tid}" --outcome "all-fields outcome body" \
        >/dev/null 2>&1
    child=$(add_task_get_id "Implement all-fields child" --parent "${tid}")

    assert_contains "'--all-fields' emits '— Description —'" \
        "— Description —" endless task show "${tid}" --all-fields
    assert_contains "'--all-fields' emits '— Analysis —'" \
        "— Analysis —" endless task show "${tid}" --all-fields
    assert_contains "'--all-fields' emits '— Text —'" \
        "— Text —" endless task show "${tid}" --all-fields
    assert_contains "'--all-fields' emits '— Outcome —'" \
        "— Outcome —" endless task show "${tid}" --all-fields
    assert_contains "'--all-fields' emits '— Children —' with the child id" \
        "${child}" endless task show "${tid}" --all-fields
}

# ─── 4: --llm and --json output paths include analysis ──────────────────────

test_llm_and_json() {
    section "4 — '--llm' and '--json' carry the analysis field"

    local tid
    tid=$(add_task_get_id "Audit analysis-machine-paths")
    endless task update "${tid}" --analysis "machine-path analysis" \
        >/dev/null 2>&1

    assert_contains "'--analysis --llm' emits a '## Analysis' heading" \
        "## Analysis" \
        endless task show "${tid}" --analysis --llm

    assert_contains "'--analysis --json' includes the analysis value" \
        '"analysis": "machine-path analysis"' \
        endless task show "${tid}" --analysis --json

    assert_contains "default '--json' nulls analysis (gated off)" \
        '"analysis": null' \
        endless task show "${tid}" --json
}

# ─── 5: validator help text steers analysis to --analysis ───────────────────

test_validator_help() {
    section "5 — Validator help text steers long-form analysis to --analysis"

    local long_title long_desc
    long_title="Implement $(repeat_a 150)"
    long_desc=$(repeat_a 1100)

    assert_refused "over-length title error mentions '--analysis'" \
        "--analysis" \
        endless task add "${long_title}"

    assert_refused "over-length description error mentions '--analysis'" \
        "--analysis" \
        endless task add "Implement valid-title" --description "${long_desc}"
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

    printf '%sE-1599 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_analysis_section
    test_analysis_before_text
    test_all_fields
    test_llm_and_json
    test_validator_help

    summary
}

main "$@"
