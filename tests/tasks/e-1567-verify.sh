#!/usr/bin/env bash
#
# E-1567 verification script — exercises the children-state breakdown that gets
# computed and injected into the epic spawn handoff at render time, end-to-end
# against the worktree's sandbox DB via `endless task handoff`.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1567-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem. Each run mints fresh task IDs; the script does
# NOT wipe the sandbox between runs (pollution is bounded and inspectable via
#   uv run endless task list --db sandbox).
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
# stderr so callers can capture only the id. Titles must lead with an
# actionable verb (the verb gate), so callers pass verb-led titles.
add_task_get_id() {
    local title="$1"
    shift
    local output rc
    output=$(endless task add "${title}" "$@" 2>&1)
    rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        printf 'ERROR: add failed for %q: %s\n' "${title}" "${output}" >&2
        return 1
    fi
    printf '%s\n' "${output}" | grep -oE 'E-[0-9]+' | head -1
}

# add_epic TITLE → create an epic-typed task, emit its id.
add_epic() {
    add_task_get_id "$1" --type epic
}

# add_child PARENT TITLE STATUS [OUTCOME]
#   Create a child under PARENT and move it to STATUS. New tasks default to
#   needs_plan, so pass STATUS="" to leave it there. `completed` needs an
#   outcome (and a completable lead verb in the title) per the status gates.
add_child() {
    local parent="$1" title="$2" status="$3" outcome="${4:-}"
    local id out rc
    id=$(add_task_get_id "${title}" --parent "${parent}") || return 1
    if [[ -n "${status}" ]]; then
        if [[ -n "${outcome}" ]]; then
            out=$(endless task update "${id}" --status "${status}" \
                --outcome "${outcome}" 2>&1); rc=$?
        else
            out=$(endless task update "${id}" --status "${status}" 2>&1); rc=$?
        fi
        # Surface setup failures (e.g. a status gate rejecting the transition)
        # rather than letting a silently-unchanged status skew the breakdown.
        if [[ "${rc}" -ne 0 ]]; then
            printf 'WARN: could not set %s -> %s: %s\n' \
                "${id}" "${status}" "$(printf '%s' "${out}" | tail -1)" >&2
        fi
    fi
    printf '%s\n' "${id}"
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

# ─── 1: zero children → "no children yet" + decomposition guidance ──────────

test_zero_children() {
    section "1 — epic with no children renders 'no children yet' + guidance"

    local epic
    epic=$(add_epic "Coordinate empty epic") || { report_fail \
        "setup: create empty epic" "epic id" "add failed"; return; }

    assert_contains "epic handoff names 'Children: no children yet.'" \
        "Children: no children yet." \
        endless task handoff "${epic}"

    assert_contains "epic handoff carries the operational-mode block" \
        "Pick your operational mode" \
        endless task handoff "${epic}"

    assert_contains "zero-children case steers to decomposition" \
        "drive decomposition" \
        endless task handoff "${epic}"
}

# ─── 2: mixed children → ordered breakdown + total, terminal collapse ───────

test_mixed_breakdown() {
    section "2 — mixed children: ordered breakdown, total, terminal collapse"

    local epic
    epic=$(add_epic "Coordinate mixed epic") || { report_fail \
        "setup: create mixed epic" "epic id" "add failed"; return; }

    add_child "${epic}" "Implement m-ready-1"  "ready"       >/dev/null || true
    add_child "${epic}" "Implement m-ready-2"  "ready"       >/dev/null || true
    add_child "${epic}" "Implement m-ready-3"  "ready"       >/dev/null || true
    add_child "${epic}" "Implement m-plan-1"   ""            >/dev/null || true
    add_child "${epic}" "Implement m-plan-2"   ""            >/dev/null || true
    add_child "${epic}" "Implement m-prog-1"   "in_progress" >/dev/null || true
    # Four distinct terminal statuses must collapse into one `terminal` bucket.
    # Four distinct terminal statuses; declined/completed also need an outcome,
    # and completed needs a completable lead verb ("Audit") in its title.
    add_child "${epic}" "Implement m-term-conf" "confirmed"  >/dev/null || true
    add_child "${epic}" "Implement m-term-asmd" "assumed"    >/dev/null || true
    add_child "${epic}" "Implement m-term-decl" "declined" "no longer needed" >/dev/null || true
    add_child "${epic}" "Audit m-term-compl" "completed" "rolled up" >/dev/null || true

    assert_contains "breakdown is lifecycle-ordered with a reconciling total" \
        "Children: 2 needs_plan, 3 ready, 1 in_progress, 4 terminal (10 total)." \
        endless task handoff "${epic}"

    # Every operational mode is named in the embedded block.
    assert_contains "mode block lists 'Zero children'" \
        "Zero children" endless task handoff "${epic}"
    assert_contains "mode block lists 'All \`needs_plan\`'" \
        "All \`needs_plan\`" endless task handoff "${epic}"
    assert_contains "mode block lists 'All \`ready\`'" \
        "All \`ready\`" endless task handoff "${epic}"
    assert_contains "mode block lists 'All \`in_progress\`'" \
        "All \`in_progress\`" endless task handoff "${epic}"
    assert_contains "mode block lists 'All terminal'" \
        "All terminal" endless task handoff "${epic}"
    assert_contains "mode block lists 'Mixed'" \
        "Mixed" endless task handoff "${epic}"
}

# ─── 3: single bucket still gets the total ──────────────────────────────────

test_single_bucket() {
    section "3 — single-bucket breakdown still carries '(N total)'"

    local epic
    epic=$(add_epic "Coordinate single-bucket epic") || { report_fail \
        "setup: create single-bucket epic" "epic id" "add failed"; return; }

    add_child "${epic}" "Implement sb-1" "ready" >/dev/null || true
    add_child "${epic}" "Implement sb-2" "ready" >/dev/null || true
    add_child "${epic}" "Implement sb-3" "ready" >/dev/null || true

    assert_contains "single bucket renders 'Children: 3 ready (3 total).'" \
        "Children: 3 ready (3 total)." \
        endless task handoff "${epic}"
}

# ─── 4: blocked + revisit are their own buckets (no silent drop) ────────────

test_blocked_and_revisit() {
    section "4 — blocked/revisit get their own buckets; total reconciles"

    local epic
    epic=$(add_epic "Coordinate blocked-revisit epic") || { report_fail \
        "setup: create blocked-revisit epic" "epic id" "add failed"; return; }

    add_child "${epic}" "Implement br-plan"    ""          >/dev/null || true
    add_child "${epic}" "Implement br-blocked" "blocked"   >/dev/null || true
    add_child "${epic}" "Implement br-revisit" "revisit"   >/dev/null || true
    add_child "${epic}" "Implement br-verify"  "verify"    >/dev/null || true
    add_child "${epic}" "Implement br-conf"    "confirmed" >/dev/null || true

    assert_contains "in-flight 'blocked'/'revisit' render as their own buckets" \
        "Children: 1 needs_plan, 1 blocked, 1 revisit, 1 verify, 1 terminal (5 total)." \
        endless task handoff "${epic}"
}

# ─── 5: the redundant child_count block is dropped for epics ────────────────

test_epic_drops_child_count_block() {
    section "5 — epic handoff drops the old child_count line"

    local epic
    epic=$(add_epic "Coordinate count-drop epic") || { report_fail \
        "setup: create count-drop epic" "epic id" "add failed"; return; }
    add_child "${epic}" "Implement cd-1" "ready" >/dev/null || true
    add_child "${epic}" "Implement cd-2" "ready" >/dev/null || true

    assert_not_contains "epic handoff no longer says 'This task has N children'" \
        "This task has" \
        endless task handoff "${epic}"
}

# ─── 6: non-epic renders consume the var as a no-op ─────────────────────────

test_non_epic_no_breakdown() {
    section "6 — task handoff omits the breakdown + mode block (var is a no-op)"

    local parent
    parent=$(add_task_get_id "Implement leaf parent") || { report_fail \
        "setup: create task parent" "task id" "add failed"; return; }
    add_child "${parent}" "Implement leaf child" "ready" >/dev/null || true

    assert_not_contains "task handoff omits the 'Children:' breakdown line" \
        "Children:" \
        endless task handoff "${parent}"

    assert_not_contains "task handoff omits the operational-mode block" \
        "Pick your operational mode" \
        endless task handoff "${parent}"

    # The task variant keeps its OWN child_count line — only epic dropped it.
    assert_contains "task variant still carries its child_count line" \
        "This task has" \
        endless task handoff "${parent}"
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

    printf '%sE-1567 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_zero_children
    test_mixed_breakdown
    test_single_bucket
    test_blocked_and_revisit
    test_epic_drops_child_count_block
    test_non_epic_no_breakdown

    summary
}

main "$@"
