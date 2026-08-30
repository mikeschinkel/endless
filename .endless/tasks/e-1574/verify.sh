#!/usr/bin/env bash
#
# E-1574 verification script — confirms the research-task field model is
# documented in the endless guide (`endless guide tasks`), that the two
# reconciled lines read coherently, that the cross-reference index lists the
# new topic, and that the guide map stays valid.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1574-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure. This task is documentation-only — the script makes NO DB writes and
# needs no sandbox; it reads rendered guide output and runs `just guide-check`.
#
# Shape borrowed from the E-1599 ad-hoc prototype (the formalization task is
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

# Captured once in main(): the rendered `endless guide tasks` and `endless
# guide` (index) output, and the Research-task field model subsection only.
GUIDE_TASKS=""
GUIDE_INDEX=""
FIELD_MODEL_SECTION=""

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

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_haystack_contains DESC HAYSTACK PATTERN
#   Pass if HAYSTACK contains the literal substring PATTERN.
assert_haystack_contains() {
    local desc="$1"
    local haystack="$2"
    local pattern="$3"
    if [[ "${haystack}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output contains: ${pattern}" "<not found in rendered output>"
}

# assert_haystack_lacks DESC HAYSTACK PATTERN
#   Pass if HAYSTACK does NOT contain the literal substring PATTERN.
assert_haystack_lacks() {
    local desc="$1"
    local haystack="$2"
    local pattern="$3"
    if [[ "${haystack}" != *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output does NOT contain: ${pattern}" \
        "found '${pattern}' in rendered output"
}

# assert_no_task_ids DESC HAYSTACK
#   Pass if HAYSTACK contains no E-NNN ticket id (shipped-doc constraint).
assert_no_task_ids() {
    local desc="$1"
    local haystack="$2"
    local hits
    hits=$(printf '%s\n' "${haystack}" | grep -oE 'E-[0-9]+' | sort -u | tr '\n' ' ')
    if [[ -z "${hits}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "no E-NNN ids in the added prose" "found: ${hits}"
}

# assert_exit_zero DESC CMD [ARGS...]
assert_exit_zero() {
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

# ─── 1: the subsection renders ──────────────────────────────────────────────

test_subsection_renders() {
    section "1 — 'guide tasks' renders the Research-task field model subsection"

    assert_haystack_contains "subsection header present" \
        "${GUIDE_TASKS}" "Research-task field model"
    assert_haystack_contains "states text = the research request" \
        "${FIELD_MODEL_SECTION}" "research **request**"
    assert_haystack_contains "states outcome = the deliverable" \
        "${FIELD_MODEL_SECTION}" "The **deliverable**"
    assert_haystack_contains "names research's only terminal status" \
        "${FIELD_MODEL_SECTION}" "only terminal status is \`completed\`"
    assert_haystack_contains "documents file-alongside deliverable convention" \
        "${FIELD_MODEL_SECTION}" "docs/research-<date>-<slug>.md"
}

# ─── 2: the two reconciled lines read coherently ────────────────────────────

test_reconciled_lines() {
    section "2 — reconciled field-table and distinction lines point at the model"

    assert_haystack_contains "text table row notes the research-task role" \
        "${GUIDE_TASKS}" "On a research task, \`text\` instead holds the research"
    assert_haystack_contains "Analysis-vs-text bullet covers deliverable-shaped tasks" \
        "${GUIDE_TASKS}" "deliverable-shaped task"
}

# ─── 3: shipped-doc constraint — no E-NNN ids in the added prose ─────────────

test_no_task_ids() {
    section "3 — added prose carries no internal task ids (shipped-doc rule)"

    assert_no_task_ids "Research-task field model subsection is id-free" \
        "${FIELD_MODEL_SECTION}"
}

# ─── 4: the cross-reference index lists the new topic ───────────────────────

test_index_topic() {
    section "4 — 'guide' index lists the research-task field model topic"

    assert_haystack_contains "topic row present in the index table" \
        "${GUIDE_INDEX}" "research-task field model"
    assert_haystack_contains "topic row maps to the tasks section" \
        "${GUIDE_INDEX}" "research-task field model | tasks"
}

# ─── 5: the guide map stays valid ───────────────────────────────────────────

test_guide_map_valid() {
    section "5 — 'just guide-check' validates the regenerated map / index"

    assert_exit_zero "just guide-check exits 0" just guide-check
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

    printf '%sE-1574 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  checks:  rendered guide output + guide-check (no DB writes)\n'

    # Capture rendered guide output once. --db main is read-only here; the
    # guide command reads docs/guide/*.md from disk regardless of DB.
    GUIDE_TASKS=$(uv run endless guide tasks --db main 2>&1)
    GUIDE_INDEX=$(uv run endless guide --db main 2>&1)

    # Slice out just the Research-task field model subsection so the id-free
    # and content checks scope to the added prose, not the whole page.
    FIELD_MODEL_SECTION=$(printf '%s\n' "${GUIDE_TASKS}" \
        | awk '/### Research-task field model/{f=1} f{print} f&&/^## Updating tasks/{exit}')

    test_subsection_renders
    test_reconciled_lines
    test_no_task_ids
    test_index_topic
    test_guide_map_valid

    summary
}

main "$@"
