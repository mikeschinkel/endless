#!/usr/bin/env bash
#
# E-1601 verification script — exercises the flag-gated rendering of `task
# show`'s large fields (outcome / text / analysis) end-to-end against the
# worktree's sandbox DB.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1601-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on an environment problem. Each new task gets a fresh ID; the
# script does NOT wipe the sandbox between runs (pollution is bounded and
# inspectable via  uv run endless task list --db sandbox).
#
# The final section runs the Python suite (`just test`) so a single invocation
# is the whole verification gate for E-1601.
#
# Shape borrowed from the E-1599 / E-1577 ad-hoc prototypes (the formalization
# task is E-1596); this is a per-task verify script, not a deliverable of E-1596.

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

# Echo a string of N copies of 'a' (for exact char-count triggers).
repeat_a() {
    local n="$1"
    printf 'a%.0s' $(seq 1 "${n}")
}

# ─── assertions ─────────────────────────────────────────────────────────────

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

# ─── 1: default snapshot hides outcome + shows placeholder ───────────────────

test_outcome_placeholder_default() {
    section "1 — default 'task show' hides outcome behind a placeholder"

    local tid
    tid=$(add_task_get_id "Audit outcome-placeholder")
    endless task update "${tid}" --outcome "the full outcome deliverable body" \
        >/dev/null 2>&1

    assert_contains "default show emits an 'Outcome:' placeholder label" \
        "Outcome:" endless task show "${tid}"
    assert_contains "placeholder names the --outcome flag" \
        "(--outcome to display)" endless task show "${tid}"
    assert_not_contains "default show omits the '— Outcome —' section" \
        "— Outcome —" endless task show "${tid}"
    assert_not_contains "default show omits the outcome body" \
        "the full outcome deliverable body" endless task show "${tid}"
}

# ─── 2: --outcome reveals the full section ───────────────────────────────────

test_outcome_flag_reveals() {
    section "2 — '--outcome' reveals the full outcome section"

    local tid
    tid=$(add_task_get_id "Audit outcome-reveal")
    endless task update "${tid}" --outcome "the full outcome deliverable body" \
        >/dev/null 2>&1

    assert_contains "'--outcome' emits the '— Outcome —' section" \
        "— Outcome —" endless task show "${tid}" --outcome
    assert_contains "'--outcome' emits the outcome body" \
        "the full outcome deliverable body" endless task show "${tid}" --outcome
    assert_not_contains "'--outcome' suppresses the placeholder" \
        "(--outcome to display)" endless task show "${tid}" --outcome
}

# ─── 3: uniform suppression for declined ─────────────────────────────────────

test_declined_uniform_suppression() {
    section "3 — declined reason is suppressed by default (uniform rule)"

    local tid
    tid=$(add_task_get_id "Audit declined-suppression")
    endless task decline "${tid}" --reason "declined-suppression reason" \
        >/dev/null 2>&1

    assert_contains "declined default show emits a placeholder" \
        "(--outcome to display)" endless task show "${tid}"
    assert_not_contains "declined default show omits the '— Outcome —' section" \
        "— Outcome —" endless task show "${tid}"
    assert_not_contains "declined default show omits the reason body" \
        "declined-suppression reason" endless task show "${tid}"
    assert_contains "'--outcome' restores the declined reason" \
        "declined-suppression reason" endless task show "${tid}" --outcome
}

# ─── 4: text / analysis placeholders ─────────────────────────────────────────

test_text_analysis_placeholders() {
    section "4 — text and analysis collapse to flag-named placeholders"

    local tid text_file
    tid=$(add_task_get_id "Audit text-analysis-placeholders")
    endless task update "${tid}" --analysis "placeholder analysis body" \
        >/dev/null 2>&1
    text_file=$(mktemp)
    printf 'placeholder text body\n' > "${text_file}"
    endless task update "${tid}" --text-file "${text_file}" >/dev/null 2>&1
    rm -f "${text_file}"

    assert_contains "default show emits a 'Text:' placeholder" \
        "(--text to display)" endless task show "${tid}"
    assert_contains "default show emits an 'Analysis:' placeholder" \
        "(--analysis to display)" endless task show "${tid}"
    assert_not_contains "default show omits the text body" \
        "placeholder text body" endless task show "${tid}"
    assert_not_contains "default show omits the analysis body" \
        "placeholder analysis body" endless task show "${tid}"
}

# ─── 5: char-count accuracy ──────────────────────────────────────────────────

test_char_count_accuracy() {
    section "5 — the placeholder reports the exact char count"

    local tid body
    tid=$(add_task_get_id "Audit char-count")
    body=$(repeat_a 137)
    endless task update "${tid}" --outcome "${body}" >/dev/null 2>&1

    # Label column is padded, so match the count text independently of spacing.
    assert_contains "placeholder reports '137 chars (--outcome to display)'" \
        "137 chars (--outcome to display)" \
        endless task show "${tid}"
}

# ─── 6: --all-fields reveals everything ──────────────────────────────────────

test_all_fields_reveals() {
    section "6 — '--all-fields' reveals every section, no placeholder left"

    local tid text_file
    tid=$(add_task_get_id "Audit all-fields-reveal")
    endless task update "${tid}" --analysis "all-fields analysis body" \
        >/dev/null 2>&1
    text_file=$(mktemp)
    printf 'all-fields text body\n' > "${text_file}"
    endless task update "${tid}" --text-file "${text_file}" >/dev/null 2>&1
    rm -f "${text_file}"
    endless task update "${tid}" --outcome "all-fields outcome body" \
        >/dev/null 2>&1

    assert_contains "'--all-fields' emits '— Analysis —'" \
        "— Analysis —" endless task show "${tid}" --all-fields
    assert_contains "'--all-fields' emits '— Text —'" \
        "— Text —" endless task show "${tid}" --all-fields
    assert_contains "'--all-fields' emits '— Outcome —'" \
        "— Outcome —" endless task show "${tid}" --all-fields
    assert_not_contains "'--all-fields' leaves no placeholder behind" \
        "to display)" endless task show "${tid}" --all-fields
}

# ─── 7: --llm gating ─────────────────────────────────────────────────────────

test_llm_gating() {
    section "7 — '--llm' collapses outcome to a char marker unless flagged"

    local tid
    tid=$(add_task_get_id "Audit llm-gating")
    endless task update "${tid}" --outcome "llm outcome body" >/dev/null 2>&1

    assert_contains "default '--llm' emits an 'outcome_chars=' marker" \
        "outcome_chars=" endless task show "${tid}" --llm
    assert_not_contains "default '--llm' omits the full outcome body" \
        "llm outcome body" endless task show "${tid}" --llm
    assert_contains "'--llm --outcome' emits a '## Outcome' heading" \
        "## Outcome" endless task show "${tid}" --llm --outcome
    assert_contains "'--llm --outcome' emits the outcome body" \
        "llm outcome body" endless task show "${tid}" --llm --outcome
}

# ─── 8: --json gating ────────────────────────────────────────────────────────

test_json_gating() {
    section "8 — '--json' nulls outcome by default but keeps outcome_chars"

    local tid
    tid=$(add_task_get_id "Audit json-gating")
    endless task update "${tid}" --outcome "json outcome body" >/dev/null 2>&1

    assert_contains "default '--json' nulls the outcome" \
        '"outcome": null' endless task show "${tid}" --json
    assert_contains "default '--json' carries an 'outcome_chars' count" \
        '"outcome_chars": 17' endless task show "${tid}" --json
    assert_not_contains "default '--json' omits the outcome body" \
        "json outcome body" endless task show "${tid}" --json
    assert_contains "'--json --outcome' includes the outcome body" \
        '"outcome": "json outcome body"' \
        endless task show "${tid}" --json --outcome
}

# ─── 9: placeholder/section ordering around Description ──────────────────────

test_field_ordering() {
    section "9 — placeholder precedes Description; full section follows it"

    local tid
    tid=$(add_task_get_id "Audit field-ordering" \
        --description "a distinct multi-line description body")
    endless task update "${tid}" --outcome "ordering outcome body" \
        >/dev/null 2>&1

    # Hidden: the single-line 'Outcome:' placeholder renders ABOVE Description.
    assert_ordering "default: 'Outcome:' placeholder precedes '— Description —'" \
        "Outcome:" "— Description —" \
        endless task show "${tid}"
    # Shown: the full '— Outcome —' section renders BELOW Description.
    assert_ordering "'--outcome': '— Description —' precedes '— Outcome —'" \
        "— Description —" "— Outcome —" \
        endless task show "${tid}" --outcome
}

# ─── 10: pytest regression suite ─────────────────────────────────────────────

test_pytest_suite() {
    section "10 — Python suite ('just test') passes"

    local output rc
    output=$(just test 2>&1)
    rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "'just test' exits 0 (full Python suite green)"
        return
    fi
    report_fail "'just test' exits 0 (full Python suite green)" \
        "exit 0" \
        "exit=${rc} | tail: $(printf '%s\n' "${output}" | tail -20)"
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

    printf '%sE-1601 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_outcome_placeholder_default
    test_outcome_flag_reveals
    test_declined_uniform_suppression
    test_text_analysis_placeholders
    test_char_count_accuracy
    test_all_fields_reveals
    test_llm_gating
    test_json_gating
    test_field_ordering
    test_pytest_suite

    summary
}

main "$@"
