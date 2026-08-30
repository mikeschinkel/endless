#!/usr/bin/env bash
#
# E-1686 verification script — proves a session row stuck 'ended' is revived to
# a live state by the SAME session's continued activity, while the E-1530
# reused-pane safety and live-state authority are preserved.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1686-verify.sh
#
# Lever: the revive is reachable headlessly through the candidate Go binary —
#   ./bin/endless-go session-query ensure-claude-id --session-id <uuid> \
#       --project-root <root> --process <pane>
# calls monitor.EnsureClaudeSessionID → TouchSession (the per-hook heartbeat).
# Rows are seeded/mutated/read directly with sqlite3 against the worktree's
# self-detected sandbox DB (E-1281/E-1368), so no Python/tmux is involved.
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on environment errors. Each run uses unique session_ids/panes and
# deletes its own rows on the way out, so the sandbox stays clean.
#
# The `task bind` facet (execTaskClaimed reviving an ended row, E-1686) is
# covered by the Go executor test TestClaim_RevivesEndedSession — driving the
# event pipeline headlessly here would add no coverage the unit test lacks.

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()
SEEDED_IDS=()

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
    local desc="$1" expected="$2" actual="$3"
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

# Fire one hook-equivalent activity for a session: ensure-claude-id runs the
# same TouchSession path every Claude hook does. Output (the row id) suppressed.
touch_session() {
    local sid="$1" pane="$2"
    ./bin/endless-go session-query ensure-claude-id \
        --session-id "${sid}" --project-root "${REPO_ROOT}" --process "${pane}" \
        >/dev/null 2>&1
}

# Current state of a session row, or "<absent>" if no row.
state_of() {
    local sid="$1" s
    s=$(sqlite3 "${SANDBOX_DB}" \
        "SELECT state FROM sessions WHERE session_id='${sid}';" 2>/dev/null)
    printf '%s' "${s:-<absent>}"
}

# Force a row's state directly (stands in for EndSession / pane reaper / collision).
force_state() {
    local sid="$1" state="$2"
    sqlite3 "${SANDBOX_DB}" \
        "UPDATE sessions SET state='${state}' WHERE session_id='${sid}';" 2>/dev/null
}

# Track a seeded session_id for end-of-run cleanup.
track() { SEEDED_IDS+=("$1"); }

cleanup() {
    local sid
    for sid in "${SEEDED_IDS[@]:-}"; do
        [[ -n "${sid}" ]] && sqlite3 "${SANDBOX_DB}" \
            "DELETE FROM sessions WHERE session_id='${sid}';" 2>/dev/null
    done
}

# ─── assertions ─────────────────────────────────────────────────────────────

assert_state() {
    local desc="$1" sid="$2" want="$3" got
    got=$(state_of "${sid}")
    if [[ "${got}" == "${want}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "state=${want}" "state=${got}"
}

# ─── scenario 1: revive on own activity + reappears to a `!= ended` reader ───

test_revive_on_activity() {
    section "Ended row revives on the same session's next activity"

    local sid="e1686-revive-$$" pane="%9601"
    track "${sid}"

    touch_session "${sid}" "${pane}"
    assert_state "fresh touch creates a live (needs_input) row" "${sid}" "needs_input"

    force_state "${sid}" "ended"
    assert_state "row forced to 'ended' (reaper/collision/child-exit stand-in)" "${sid}" "ended"

    touch_session "${sid}" "${pane}"
    assert_state "same session's next hook revives 'ended' → 'needs_input'" "${sid}" "needs_input"

    # The load-bearing payoff: every reader filters `state != 'ended'`, so the
    # revived row must now satisfy that predicate (was invisible while ended).
    local visible
    visible=$(sqlite3 "${SANDBOX_DB}" \
        "SELECT count(*) FROM sessions WHERE session_id='${sid}' AND state != 'ended';" 2>/dev/null)
    if [[ "${visible}" == "1" ]]; then
        report_pass "revived session is visible to a 'state != ended' reader"
    else
        report_fail "revived session is visible to a 'state != ended' reader" \
            "count == 1" "count=${visible:-<error>}"
    fi
}

# ─── scenario 2: live states are NOT clobbered (revive is ended-only) ────────

test_live_states_preserved() {
    section "Live states pass through untouched (CASE revives ONLY 'ended')"

    local live
    for live in working idle needs_input; do
        local sid="e1686-keep-${live}-$$" pane="%960${RANDOM:0:1}2"
        track "${sid}"
        touch_session "${sid}" "${pane}"
        force_state "${sid}" "${live}"
        touch_session "${sid}" "${pane}"
        assert_state "live '${live}' not clobbered by a later touch" "${sid}" "${live}"
    done
}

# ─── scenario 3: E-1530 — a reused pane id must NOT revive a different session ─

test_reused_pane_safety() {
    section "Reused pane id does NOT revive a different session's ended row (E-1530)"

    local a="e1686-paneA-$$" b="e1686-paneB-$$" pane="%9603"
    track "${a}"; track "${b}"

    touch_session "${a}" "${pane}"
    force_state "${a}" "ended"

    # A DIFFERENT session_id arrives on the same pane (reused %N after a tmux
    # server restart). It takes the INSERT path; A's ended row must stay ended.
    touch_session "${b}" "${pane}"

    assert_state "prior occupant A stays 'ended' (not revived by B's touch)" "${a}" "ended"

    local bstate
    bstate=$(state_of "${b}")
    if [[ "${bstate}" != "ended" && "${bstate}" != "<absent>" ]]; then
        report_pass "new occupant B is live (state=${bstate})"
    else
        report_fail "new occupant B is live" "a live state" "state=${bstate}"
    fi
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${REPO_ROOT}" || exit 2

    if [[ ! -x ./bin/endless-go ]]; then
        printf 'ERROR: ./bin/endless-go not built — run `just build` first\n' >&2
        exit 2
    fi
    if ! command -v sqlite3 >/dev/null 2>&1; then
        printf 'ERROR: sqlite3 not on PATH\n' >&2
        exit 2
    fi

    # Per-worktree sandbox DB (basename matches the sandbox dir, E-1281/E-1368).
    SANDBOX_DB="${HOME}/.cache/endless/sandboxes/$(basename "${REPO_ROOT}")/endless/endless.db"
    if [[ ! -f "${SANDBOX_DB}" ]]; then
        printf 'ERROR: sandbox DB missing: %s\n' "${SANDBOX_DB}" >&2
        printf '       run `just dev-sandbox-init` from the worktree first\n' >&2
        exit 2
    fi

    trap cleanup EXIT

    printf '%sE-1686 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:    %s\n' "${REPO_ROOT}"
    printf '  db:     sandbox (%s)\n' "${SANDBOX_DB}"
    printf '  go bin: ./bin/endless-go\n'

    test_revive_on_activity
    test_live_states_preserved
    test_reused_pane_safety

    summary
}

main "$@"
