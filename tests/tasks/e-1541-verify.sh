#!/usr/bin/env bash
#
# E-1541 verification script — exercises epic status auto-derivation end-to-end
# against the worktree's sandbox DB, through the real `endless` CLI.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1541-verify.sh
#
# What it checks (the §1 derivation rule + consequences): an epic's status is a
# pure function of its children's statuses — in_progress > ready > needs_plan >
# all-terminal(completed) — except when the epic itself is in a sticky-override
# status (revisit/declined/obsolete/blocked), which freezes derivation. Adding a
# non-terminal child to a completed epic reopens it, and a deep child change
# propagates up nested epics.
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error. Each run creates fresh task IDs; the sandbox is NOT
# wiped between runs (pollution is bounded and inspectable via
#   uv run endless task list --db sandbox).
#
# NOTE on coverage: the --db sandbox path derives correctly in the DB but
# bypasses the ledger writer + git auto-commit by design, so the
# epic.status_derived *ledger* emission and the projector replay are NOT
# observable here — those are covered by the Go tests
# (internal/eventcmd/epic_derivation_test.go). This script verifies the
# user-facing CLI behavior: the derived status the user sees after each change.
#
# Ad-hoc verify script in the shape established by E-1577 / formalized by E-1596.

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
    local output rc
    output=$(endless task add "${title}" "$@" 2>&1)
    rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        printf 'ERROR: add failed for %q: %s\n' "${title}" "${output}" >&2
        return 1
    fi
    printf '%s\n' "${output}" | grep -oE 'E-[0-9]+' | head -1
}

# Read a task's current status (the bare value from the `Status:` line).
task_status() {
    endless task show "$1" 2>&1 | grep -E '^Status:' | head -1 | awk '{print $2}'
}

# Force a task's status. Returns the update command's exit code; output silenced.
set_status() {
    endless task update "$1" --status "$2" >/dev/null 2>&1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_status DESC TASK_ID WANT
#   Pass if the task's current derived status equals WANT.
assert_status() {
    local desc="$1"
    local id="$2"
    local want="$3"
    local got
    got=$(task_status "${id}")
    if [[ "${got}" == "${want}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "status=${want}" "status=${got:-<empty>} (task ${id})"
}

# ─── rule: status derives from children ─────────────────────────────────────

test_rule_derivation() {
    section "§1 rule — epic status derives from children"

    local epic a b
    epic=$(add_task_get_id "Implement derive-rule epic" --type epic) || exit 2
    a=$(add_task_get_id "Implement child A" --parent "${epic}") || exit 2
    b=$(add_task_get_id "Implement child B" --parent "${epic}") || exit 2

    # Two needs_plan children -> epic needs_plan.
    assert_status "two needs_plan children -> epic needs_plan" "${epic}" "needs_plan"

    # Promote one child to ready -> epic ready (no in_progress yet).
    set_status "${a}" "ready"
    assert_status "needs_plan + ready -> epic ready" "${epic}" "ready"

    # Any in_progress child wins -> epic in_progress.
    set_status "${b}" "in_progress"
    assert_status "any in_progress child -> epic in_progress" "${epic}" "in_progress"

    # All children terminal (confirmed) -> epic completed.
    set_status "${a}" "confirmed"
    set_status "${b}" "confirmed"
    assert_status "all terminal children -> epic completed" "${epic}" "completed"
}

# ─── reopen: non-terminal child to a completed epic ─────────────────────────

test_reopen() {
    section "Reopen — adding a non-terminal child to a completed epic"

    local epic done_child new_child
    epic=$(add_task_get_id "Implement reopen epic" --type epic) || exit 2
    done_child=$(add_task_get_id "Implement reopen done-child" --parent "${epic}") || exit 2
    set_status "${done_child}" "confirmed"
    assert_status "single confirmed child -> epic completed" "${epic}" "completed"

    # Adding a fresh needs_plan child reopens the epic (falls out of the rule).
    new_child=$(add_task_get_id "Implement reopen new-child" --parent "${epic}") || exit 2
    assert_status "needs_plan child added to completed epic -> epic needs_plan" \
        "${epic}" "needs_plan"
}

# ─── sticky-override: derivation frozen while epic is sticky ─────────────────

test_sticky_override() {
    section "Sticky-override — blocked epic ignores child changes"

    local epic child
    epic=$(add_task_get_id "Implement sticky epic" --type epic) || exit 2
    child=$(add_task_get_id "Implement sticky child" --parent "${epic}") || exit 2

    # Put the epic into a sticky-override status, then change the child.
    set_status "${epic}" "blocked"
    set_status "${child}" "in_progress"
    assert_status "child flip while epic blocked -> epic stays blocked" \
        "${epic}" "blocked"

    # Clearing the override is a manual set on the epic itself, which is never
    # re-derived in place (that's what preserves explicit sets like a cascade
    # confirm). So clearing does NOT retroactively re-derive — per plan §1,
    # derivation *resumes* on the next child change.
    set_status "${epic}" "needs_plan"
    assert_status "clearing override does not retro-derive -> epic stays needs_plan" \
        "${epic}" "needs_plan"

    # The next child change re-derives normally now that the override is gone.
    set_status "${child}" "ready"
    assert_status "after override cleared, next child change derives -> epic ready" \
        "${epic}" "ready"
}

# ─── nested: a deep child change propagates up nested epics ──────────────────

test_nested_propagation() {
    section "Nested — grand-child change propagates up two epic levels"

    local grand parent child
    grand=$(add_task_get_id "Implement nested grandparent epic" --type epic) || exit 2
    parent=$(add_task_get_id "Implement nested parent epic" --type epic --parent "${grand}") || exit 2
    child=$(add_task_get_id "Implement nested child" --parent "${parent}") || exit 2

    set_status "${child}" "in_progress"
    assert_status "child in_progress -> parent epic in_progress" "${parent}" "in_progress"
    assert_status "child in_progress -> grandparent epic in_progress (propagated)" \
        "${grand}" "in_progress"
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

    printf '%sE-1541 verification — epic status auto-derivation%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_rule_derivation
    test_reopen
    test_sticky_override
    test_nested_propagation

    summary
}

main "$@"
