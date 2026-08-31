#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1762 and records what was true when E-1762
# landed. Edit it only if you ARE E-1762. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1762 verification script — auto-revisit on plan-text edit.
#
# Editing tasks.text on a task in a done state (assumed/confirmed/completed) adds
# unshipped scope: the done-status becomes a lie and the task hides from session
# monitor. `task update` now auto-transitions a done task to `revisit` (and
# announces why) when its plan text ACTUALLY changes. Guards:
#   - only a real text change (identical re-write is a no-op),
#   - only from the completed-successfully set {assumed, confirmed, completed},
#   - an explicit --status in the same update wins; --keep-status suppresses,
#   - epics are excluded (their only done-state is `completed`; flipping an epic
#     would trip the E-1542 pause gate for descendant sessions).
#
# Run from anywhere inside the worktree:
#   endless task verify E-1762
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure. Uses the worktree's sandbox DB via `uv run endless ... --db sandbox`;
# no build required. Each run creates fresh tasks; the sandbox is not wiped
# between runs (pollution is bounded, inspectable via
#   uv run endless task list --db sandbox).

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
# stderr so callers can capture only the id. Extra args pass through to add.
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
    printf '%s\n' "${output}" | command grep -oE 'E-[0-9]+' | head -1
}

# Emit the current status string of a task (e.g. "revisit").
status_of() {
    endless task show "$1" 2>&1 \
        | command grep -iE '^Status' | head -1 \
        | sed -E 's/^Status:[[:space:]]*//' | tr -d '[:space:]'
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_status DESC TID EXPECTED
#   Pass if the task's current status equals EXPECTED.
assert_status() {
    local desc="$1" tid="$2" expected="$3"
    local actual
    actual=$(status_of "${tid}")
    if [[ "${actual}" == "${expected}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "status == ${expected}" "status == ${actual}"
}

# assert_output_contains DESC PATTERN CMD...
#   Pass if CMD's combined output contains PATTERN.
assert_output_contains() {
    local desc="$1" pattern="$2"
    shift 2
    local output
    output=$("$@" 2>&1)
    if [[ "${output}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output contains: ${pattern}" "output=${output}"
}

# assert_output_lacks DESC PATTERN CMD...
#   Pass if CMD's combined output does NOT contain PATTERN.
assert_output_lacks() {
    local desc="$1" pattern="$2"
    shift 2
    local output
    output=$("$@" 2>&1)
    if [[ "${output}" != *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output lacks: ${pattern}" "output=${output}"
}

# ─── the flip: real text edit on a done task → revisit + announce ───────────

test_flip_and_announce() {
    section "Flip + announce — real --text edit on an 'assumed' task"

    local tid
    tid=$(add_task_get_id "Verify e1762 flip target")
    endless task update "${tid}" --status assumed >/dev/null 2>&1

    assert_output_contains "text edit announces the auto-flip" \
        "status set to revisit" \
        endless task update "${tid}" --text "new unshipped scope added here"
    assert_status "task is now revisit" "${tid}" "revisit"

    # Same from 'confirmed'.
    local cid
    cid=$(add_task_get_id "Verify e1762 confirmed flip")
    endless task update "${cid}" --status confirmed >/dev/null 2>&1
    endless task update "${cid}" --text "reopened scope" >/dev/null 2>&1
    assert_status "confirmed task also flips to revisit" "${cid}" "revisit"
}

# ─── guards: no flip when the change is not a real plan-text edit ───────────

test_no_flip_guards() {
    section "No flip — identical re-write, outcome edit, --keep-status"

    # Identical re-write is a no-op (no flip).
    local tid
    tid=$(add_task_get_id "Verify e1762 identical rewrite")
    endless task update "${tid}" --text "steady content" >/dev/null 2>&1
    endless task update "${tid}" --status assumed >/dev/null 2>&1
    assert_output_lacks "identical --text re-write does not announce a flip" \
        "status set to revisit" \
        endless task update "${tid}" --text "steady content"
    assert_status "identical re-write leaves status assumed" "${tid}" "assumed"

    # Editing --outcome on a done task is result-recording, not a reopen.
    local oid
    oid=$(add_task_get_id "Verify e1762 outcome edit")
    endless task update "${oid}" --status assumed >/dev/null 2>&1
    assert_output_lacks "--outcome edit does not announce a flip" \
        "status set to revisit" \
        endless task update "${oid}" --outcome "recording the result"
    assert_status "--outcome edit leaves status assumed" "${oid}" "assumed"

    # --keep-status suppresses the flip for a typo/formatting edit.
    local kid
    kid=$(add_task_get_id "Verify e1762 keep-status")
    endless task update "${kid}" --status assumed >/dev/null 2>&1
    assert_output_lacks "--keep-status suppresses the announce" \
        "status set to revisit" \
        endless task update "${kid}" --text "typo-fixed text" --keep-status
    assert_status "--keep-status leaves status assumed" "${kid}" "assumed"
}

# ─── scope: non-done unaffected, explicit --status wins, epics excluded ─────

test_scope() {
    section "Scope — non-done, explicit --status, epics"

    # A non-done (underway) task is untouched by a text edit.
    local uid
    uid=$(add_task_get_id "Verify e1762 underway")
    endless task update "${uid}" --status underway >/dev/null 2>&1
    endless task update "${uid}" --text "new text on underway" >/dev/null 2>&1
    assert_status "underway task is unaffected by a text edit" "${uid}" "underway"

    # An explicit --status in the same update wins over the auto-flip.
    local eid
    eid=$(add_task_get_id "Verify e1762 explicit status")
    endless task update "${eid}" --status assumed >/dev/null 2>&1
    endless task update "${eid}" --text "new text" --status confirmed >/dev/null 2>&1
    assert_status "explicit --status confirmed wins over auto-revisit" "${eid}" "confirmed"

    # A completed epic is excluded — editing its text must NOT flip it.
    local pid
    pid=$(add_task_get_id "Complete e1762 epic exclusion" --type epic)
    endless task update "${pid}" --status completed --outcome "epic done" >/dev/null 2>&1
    assert_output_lacks "completed epic text edit does not announce a flip" \
        "status set to revisit" \
        endless task update "${pid}" --text "new epic strategy text"
    assert_status "completed epic stays completed" "${pid}" "completed"
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

    printf '%sE-1762 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_flip_and_announce
    test_no_flip_guards
    test_scope

    summary
}

main "$@"
