#!/usr/bin/env bash
#
# E-1681 verification script — `endless session goto` / `session back`.
#
# Run from anywhere inside the worktree:
#   esu
#   ./tests/tasks/e-1681-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error.
#
# WHY this shape: goto/back are tmux-interactive — a real `switch-client` would
# move YOUR tmux focus, and a real `goto` needs live sessions whose panes exist.
# So the actual switch / back-stack / spawn-back BEHAVIOR is proven by the unit
# suite (run below), which drives the real code through a fake tmux that this
# script also confirms matches real tmux (the primitives section). The live CLI
# is then exercised only for the safe, non-switching paths (wiring + refusals)
# and the real-tmux primitives the back-stack depends on. Nothing here changes
# your focus or writes to your real ledger (refusals route through --db sandbox).
#
# Modeled on tests/tasks/e-1577-verify.sh (the E-1596 prototype shape).

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

note() {
    printf '  %s· %s%s\n' "${DIM}" "$1" "${RESET}"
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

# ─── runners ──────────────────────────────────────────────────────────────────

# Route the candidate CLI through the sandbox DB (these checks never switch
# panes, but the sandbox keeps them off the real ledger regardless).
endless() {
    uv run endless "$@" --db sandbox
}

# Same, but with $TMUX scrubbed so the not-in-tmux guard fires deterministically
# whether or not the script itself is run inside tmux.
endless_no_tmux() {
    env -u TMUX -u TMUX_PANE uv run endless "$@" --db sandbox
}

# ─── assertions ───────────────────────────────────────────────────────────────

# assert_succeeds DESC CMD [ARGS...]
assert_succeeds() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

# assert_refused DESC PATTERN CMD [ARGS...]
#   Pass if CMD exits non-zero AND combined output contains PATTERN.
assert_refused() {
    local desc="$1" pattern="$2"; shift 2
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]] && [[ "${output}" == *"${pattern}"* ]]; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" \
        "exit != 0 AND output contains: ${pattern}" \
        "exit=${rc} | output=${output}"
}

# assert_contains DESC PATTERN CMD [ARGS...]
assert_contains() {
    local desc="$1" pattern="$2"; shift 2
    local output
    output=$("$@" 2>&1)
    if [[ "${output}" == *"${pattern}"* ]]; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "output contains: ${pattern}" "${output}"
}

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    local desc="$1" expected="$2" actual="$3"
    if [[ "${expected}" == "${actual}" ]]; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "${expected}" "${actual}"
}

# ─── checks ───────────────────────────────────────────────────────────────────

test_unit_behavior() {
    section "Unit behavior — full goto/back/spawn-back coverage (fake tmux)"
    note "the switch, back-stack push/pop, session-id→current-pane resolution,"
    note "stale-token skipping, ambiguity, and spawn-back fallback all live here"
    assert_succeeds "tests/test_session_goto_back.py — all pass" \
        uv run pytest tests/test_session_goto_back.py -q
}

test_cli_wiring() {
    section "CLI wiring — verbs registered under \`session\`"
    assert_contains "\`session --help\` lists \`goto\`" "goto" \
        uv run endless session --help
    assert_contains "\`session --help\` lists \`back\`" "back" \
        uv run endless session --help
    assert_contains "\`session goto --help\` documents the back-stack" \
        "back-stack" uv run endless session goto --help
    assert_contains "\`session back --help\` documents spawn-back" \
        "spawning session" uv run endless session back --help
}

test_guard_rails() {
    section "Guard rails — outside tmux both verbs refuse"
    assert_refused "goto refuses without tmux" "requires tmux" \
        endless_no_tmux session goto E-1681
    assert_refused "back refuses without tmux" "requires tmux" \
        endless_no_tmux session back
}

test_resolution_refusals() {
    section "Resolution refusals — in tmux, no live target, no focus change"
    assert_refused "goto unknown task → refused (no switch)" \
        "No live session on E-99999999" \
        endless session goto E-99999999
    assert_refused "goto unknown id → refused (no switch)" \
        "No live session matches" \
        endless session goto 99999999
}

test_tmux_primitives() {
    section "Real-tmux primitives the back-stack depends on"
    note "proves the fake tmux in the unit suite matches THIS machine's tmux"
    local key="@endless_backstack_probe_$$"
    tmux set-option -g "${key}" "5 %12" 2>/dev/null
    assert_eq "server option set/read round-trips (push/pop storage)" \
        "5 %12" "$(tmux show-options -gqv "${key}" 2>/dev/null)"
    tmux set-option -gu "${key}" 2>/dev/null
    assert_eq "unset option reads empty (empty back-stack)" \
        "" "$(tmux show-options -gqv "${key}" 2>/dev/null)"
    assert_eq "current pane id resolves (switch-target validity check)" \
        "${TMUX_PANE}" \
        "$(tmux display-message -p -t "${TMUX_PANE}" '#{pane_id}' 2>/dev/null)"
    assert_eq "nonexistent pane yields empty (stale-token detection)" \
        "" \
        "$(tmux display-message -p -t '%99999999' '#{pane_id}' 2>/dev/null)"
}

# ─── main ─────────────────────────────────────────────────────────────────────

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

    printf '%sE-1681 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox (CLI refusals); fixtures (unit suite)\n'
    printf '  tmux:    %s\n' "$([[ -n "${TMUX:-}" ]] && echo inside || echo 'not inside')"

    test_unit_behavior
    test_cli_wiring
    test_guard_rails

    if [[ -n "${TMUX:-}" ]]; then
        test_resolution_refusals
        test_tmux_primitives
    else
        section "In-tmux checks — SKIPPED (not inside tmux)"
        note "run via \`esu\` from a tmux pane to exercise resolution + primitives"
    fi

    summary
}

main "$@"
