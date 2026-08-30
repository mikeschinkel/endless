#!/usr/bin/env bash
#
# E-1616 verification script — exercises the maybe-parent gate (ED-1510)
# end-to-end against the worktree's sandbox DB.
#
# Invariant: a task may NOT be both phase=maybe AND have a parent. Enforced as
# a hard gate (no --force) on every write path: task add, task update, task
# move — at both the Python CLI and the Go executor boundary.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1616-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on environment error. Each run creates fresh tasks in the sandbox
# DB; the script does NOT wipe the sandbox between runs (pollution is bounded
# and inspectable via `uv run endless task list --db sandbox`).

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

# Fragment every rejection must contain (shared by the Python and Go messages).
ERR_FRAGMENT="maybe-phase task cannot have a parent"

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

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_refused DESC CMD [ARGS...]
#   Pass if CMD exits non-zero AND its output contains the shared ERR_FRAGMENT.
assert_refused() {
    local desc="$1"
    shift
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -ne 0 ]] && [[ "${output}" == *"${ERR_FRAGMENT}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" \
        "exit != 0 AND output contains: ${ERR_FRAGMENT}" \
        "exit=${rc} | output=${output}"
}

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

# ─── checks ─────────────────────────────────────────────────────────────────

test_maybe_parent_gate() {
    section "Maybe-parent gate — phase=maybe + parent rejected on every write path"

    local parent mid cid

    parent=$(add_task_get_id "Anchor parent task")

    # ── task add ────────────────────────────────────────────────────────────
    assert_refused "task add --parent X --phase maybe is refused" \
        endless task add "Implement maybe child" --parent "${parent}" --phase maybe

    # ── task update: reparent an existing maybe task ─────────────────────────
    mid=$(add_task_get_id "Implement standalone maybe" --phase maybe)
    assert_refused "task update <maybe> --parent X is refused" \
        endless task update "${mid}" --parent "${parent}"

    # ── task update: set phase=maybe on a parented task ──────────────────────
    cid=$(add_task_get_id "Implement parented child" --parent "${parent}")
    assert_refused "task update <child> --phase maybe is refused" \
        endless task update "${cid}" --phase maybe

    # ── task move: move a maybe task under a parent ──────────────────────────
    mid=$(add_task_get_id "Implement another standalone maybe" --phase maybe)
    assert_refused "task move <maybe> --parent X is refused" \
        endless task move "${mid}" --parent "${parent}"

    # ── happy paths (must NOT be blocked) ────────────────────────────────────
    assert_succeeds "task add --phase maybe (no parent) succeeds" \
        endless task add "Implement parentless maybe" --phase maybe

    assert_succeeds "task add --parent X (default phase) succeeds" \
        endless task add "Implement committed child" --parent "${parent}"

    mid=$(add_task_get_id "Implement promotable maybe" --phase maybe)
    assert_succeeds "atomic task update --phase next --parent X succeeds" \
        endless task update "${mid}" --phase next --parent "${parent}"
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

    printf '%sE-1616 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_maybe_parent_gate

    summary
}

main "$@"
