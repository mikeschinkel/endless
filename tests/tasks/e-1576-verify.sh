#!/usr/bin/env bash
#
# E-1576 verification script — confirms `endless task show` / `task relations`
# render the reformatted, direction-disambiguated links section.
#
# Change: the old links rows were 'E-NNN (relation) [status]' under a 'Links:'
# heading, where the parenthetical's subject was implicit ('E-1537 (blocks)' —
# does it block this task, or does this task block it?). They are now a
# 'This task:' heading followed by rows that LEAD with the full directional
# phrase, left-aligned so the ids line up:
#
#   This task:
#     Blocks:       E-2 [needs_plan]
#     Relates to:   E-2 [needs_plan]
#     Blocked by:   E-3 [needs_plan]
#     Cleans up:    E-3 [needs_plan]
#
# Rows stay id-ascending. The machine-readable `--llm` `links=` line is
# intentionally UNCHANGED (still 'E-NNN (rel)').
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1576-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on an environment error. Each run seeds fresh tasks in the sandbox;
# the script does NOT wipe the sandbox between runs (pollution is bounded and
# inspectable via `uv run endless task list --db sandbox`).
#
# Follows the shape/output convention prototyped in tests/tasks/e-1577-verify.sh
# (formalization tracked under E-1596).

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

TASK_CMD="src/endless/task_cmd.py"

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
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
        printf '  %s%d passed%s\n' "${GREEN}" "${PASS_COUNT}" "${RESET}"
        printf '\n  %sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}"
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

# Create a task and emit just its E-NNN id on stdout; diagnostics to stderr.
add_task_get_id() {
    local title="$1"; shift
    local output rc
    output=$(endless task add "${title}" "$@" 2>&1)
    rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        printf 'ERROR: add failed for %q: %s\n' "${title}" "${output}" >&2
        return 1
    fi
    printf '%s\n' "${output}" | grep -oE 'E-[0-9]+' | head -1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# Bound a haystack to its last few lines (joined) for readable failure output.
_snippet() { printf '%s' "$1" | tail -6 | tr '\n' '|'; }

# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then
        report_pass "$1"; return
    fi
    report_fail "$1" "output contains: $2" "$(_snippet "$3")"
}

# assert_not_contains DESC NEEDLE HAYSTACK
assert_not_contains() {
    if [[ "$3" != *"$2"* ]]; then
        report_pass "$1"; return
    fi
    report_fail "$1" "output does NOT contain: $2" "$(_snippet "$3")"
}

# assert_row DESC PHRASE ID HAYSTACK — a single line carries '<PHRASE>:' and 'ID'.
assert_row() {
    local desc="$1" phrase="$2" id="$3" hay="$4" line
    line=$(printf '%s\n' "${hay}" | grep -F "${phrase}:" | grep -F "${id} ")
    if [[ -n "${line}" ]]; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "a line with '${phrase}:' and '${id}'" "$(_snippet "${hay}")"
}

# assert_aligned DESC SHOW_OUTPUT — every row under 'This task:' starts its
# id ('E-') at the same column (left-aligned phrase labels).
assert_aligned() {
    local desc="$1" hay="$2"
    local rows cols line pre
    rows=$(printf '%s\n' "${hay}" \
        | awk '/^This task:/{f=1;next} f&&/^  /{print} f&&!/^  /{f=0}')
    cols=()
    while IFS= read -r line; do
        [[ -z "${line}" ]] && continue
        pre="${line%%E-*}"
        cols+=("${#pre}")
    done <<< "${rows}"
    if [[ "${#cols[@]}" -lt 2 ]]; then
        report_fail "${desc}" "≥2 aligned rows" "found ${#cols[@]} row(s)"
        return
    fi
    local c first="${cols[0]}"
    for c in "${cols[@]}"; do
        if [[ "${c}" != "${first}" ]]; then
            report_fail "${desc}" "all 'E-' columns == ${first}" "columns: ${cols[*]}"
            return
        fi
    done
    report_pass "${desc} (E- column = ${first})"
}

# ─── checks ───────────────────────────────────────────────────────────────────

A=""; B=""; C=""; N=""

test_seed() {
    section "Seed — four sandbox tasks with mixed-direction relations"

    # Titles must lead with an actionable verb (the task-title gate); the
    # 'distinctive-*-marker' tokens let the title-omission check be specific.
    A=$(add_task_get_id "Build alpha link holder") || { exit 1; }
    B=$(add_task_get_id "Document distinctive-beta-marker") || { exit 1; }
    C=$(add_task_get_id "Review distinctive-gamma-marker") || { exit 1; }
    N=$(add_task_get_id "Check no-link task") || { exit 1; }

    local errs=""
    errs+=$(endless task link "${A#E-}" --to "${B#E-}" --type blocks     2>&1 >/dev/null)
    errs+=$(endless task link "${C#E-}" --to "${A#E-}" --type blocks     2>&1 >/dev/null)
    errs+=$(endless task link "${A#E-}" --to "${B#E-}" --type relates_to 2>&1 >/dev/null)
    errs+=$(endless task link "${A#E-}" --to "${C#E-}" --type cleans_up  2>&1 >/dev/null)

    if [[ -z "${errs}" ]]; then
        report_pass "seeded ${A} blocks ${B}; ${C} blocks ${A}; ${A} relates_to ${B}; ${A} cleans_up ${C}"
    else
        report_fail "seed relations" "no link errors" "${errs}"
        exit 1
    fi
}

test_show_human() {
    section "task show (human) — 'This task:' heading, directional rows, aligned ids"

    local out
    out=$(endless task show "${A#E-}" 2>&1)

    assert_contains "renders 'This task:' subject heading" "This task:" "${out}"
    assert_row  "'Blocks:' row points to ${B}"     "Blocks"      "${B}" "${out}"
    assert_row  "'Relates to:' row points to ${B}" "Relates to"  "${B}" "${out}"
    assert_row  "'Blocked by:' row points to ${C}" "Blocked by"  "${C}" "${out}"
    assert_row  "'Cleans up:' row points to ${C}"  "Cleans up"   "${C}" "${out}"
    assert_aligned "links rows are column-aligned" "${out}"

    # Old implicit-subject parenthetical format is gone from the human view.
    assert_not_contains "no '(blocks)' parenthetical"     "(blocks)"     "${out}"
    assert_not_contains "no '(blocked by)' parenthetical" "(blocked by)" "${out}"
    assert_not_contains "no '(cleans up)' parenthetical"  "(cleans up)"  "${out}"
    assert_not_contains "no '(relates to)' parenthetical" "(relates to)" "${out}"

    # Titles stay omitted to keep every row on one line.
    assert_not_contains "related-task titles omitted from rows" \
        "distinctive-beta-marker" "${out}"
}

test_show_llm() {
    section "task show --llm — machine 'links=' line UNCHANGED (still parenthetical)"

    local out
    out=$(endless task show "${A#E-}" --llm 2>&1)

    assert_contains "emits a 'links=' line"          "links="                "${out}"
    assert_contains "llm keeps '${B} (blocks)'"      "${B} (blocks)"         "${out}"
    assert_contains "llm keeps '${C} (blocked by)'"  "${C} (blocked by)"     "${out}"
    assert_not_contains "llm has no 'This task:' prose" "This task:"         "${out}"
}

test_relations() {
    section "task relations — same 'This task:' block; '(none)' when empty"

    local out
    out=$(endless task relations "${A#E-}" 2>&1)
    assert_contains "relations renders 'This task:'" "This task:" "${out}"
    assert_row "relations 'Blocks:' row points to ${B}" "Blocks" "${B}" "${out}"

    local none
    none=$(endless task relations "${N#E-}" 2>&1)
    assert_contains "relation-less task reports '(none)'" "(none)" "${none}"
    assert_not_contains "relation-less task has no 'This task:'" "This task:" "${none}"
}

test_source_pinned() {
    section "Source — _echo_links_section emits the new heading & label"

    local src
    src=$(cat "${TASK_CMD}")
    assert_contains "task_cmd.py styles 'This task:' heading" \
        'click.style("This task:", fg="cyan")' "${src}"
    assert_contains "_flatten_relations carries proper-cased rel_label" \
        '"rel_label": rel_label' "${src}"
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

    printf '%sE-1576 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_seed
    test_show_human
    test_show_llm
    test_relations
    test_source_pinned

    summary
}

main "$@"
