#!/usr/bin/env bash
#
# E-1797 verification script — `endless session goto --resume`.
#
# Run from anywhere inside the worktree:
#   esu
#   ./tests/tasks/e-1797-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error.
#
# WHY this shape: `goto --resume` is tmux-interactive — actually opening a new
# window and resuming a real `claude` can't be asserted cleanly in a script. So
# the BEHAVIOR (opens a detached new window in the target worktree running
# `claude --resume <uuid>` then focuses it; is a no-op when the target is live;
# the not-live error names `--resume` only when the target is resumable, else
# keeps the `session list` hint; the resumable-vs-unknown classifier) is proven
# by the unit suite (tests/test_session_goto_back.py), which drives the real
# code through a fake tmux and stubbed resume resolver. This script runs that
# suite FIRST as a fail-fast gate, then exercises the safe live-CLI paths
# (wiring + the outside-tmux refusal). The interactive new-window/focus behavior
# is left to the MANUAL steps printed at the end.
#
# Modeled on tests/tasks/e-1681-verify.sh.

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
# panes or resume, but the sandbox keeps them off the real ledger regardless).
endless() {
    uv run endless "$@" --db sandbox
}

# Same, but with $TMUX scrubbed so the not-in-tmux guard fires deterministically
# whether or not the script itself is run inside tmux.
endless_no_tmux() {
    env -u TMUX -u TMUX_PANE uv run endless "$@" --db sandbox
}

# ─── assertions ───────────────────────────────────────────────────────────────

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

# ─── checks ───────────────────────────────────────────────────────────────────

# Fail-fast gate: the whole behavioral contract lives in the unit suite. If it
# fails, stop here — the live-CLI checks below only cover wiring.
test_unit_behavior() {
    section "Unit behavior — goto --resume + not-live error (fake tmux)"
    note "opens detached new window in the worktree running claude --resume,"
    note "focuses it; no-op when live; error names --resume iff resumable;"
    note "resumable-vs-unknown classifier — all proven here"
    local output rc
    output=$(uv run pytest tests/test_session_goto_back.py -q 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "tests/test_session_goto_back.py — all pass"
        return 0
    fi
    report_fail "tests/test_session_goto_back.py — all pass" \
        "pytest exit == 0" "exit=${rc}"
    printf '%s\n' "${output}" | tail -25
    printf '\n%sABORTING — unit contract failed; skipping live-CLI checks.%s\n' \
        "${RED}${BOLD}" "${RESET}"
    summary
    exit 1
}

test_cli_wiring() {
    section "CLI wiring — --resume flag registered on \`goto\`"
    assert_contains "\`session goto --help\` documents --resume" "--resume" \
        uv run endless session goto --help
    assert_contains "\`session goto --help\` still documents the back-stack" \
        "back-stack" uv run endless session goto --help
}

test_guard_rails() {
    section "Guard rails — outside tmux, --resume still refuses first"
    assert_refused "goto --resume refuses without tmux" "requires tmux" \
        endless_no_tmux session goto E-1797 --resume
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

    printf '%sE-1797 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox (CLI wiring); stubs (unit suite)\n'
    printf '  tmux:    %s\n' "$([[ -n "${TMUX:-}" ]] && echo inside || echo 'not inside')"

    test_unit_behavior      # fail-fast gate
    test_cli_wiring
    test_guard_rails

    section "MANUAL (interactive — tmux + a real dead session)"
    note "these can't be scripted without hijacking your tmux focus / a real claude"
    note "1. Find a task whose session is dead (ended, no live pane), e.g. E-NNNN."
    note "2. Run \`endless session goto E-NNNN\` (no flag): it errors AND the"
    note "   message names \`--resume\` (because that session is resumable)."
    note "3. Run \`endless session goto E-NNNN --resume\`: a NEW tmux window opens,"
    note "   \`claude --resume\` runs in the task's worktree, and focus moves to it."
    note "4. Run \`endless session goto <LIVE-ref> --resume\`: behaves like plain"
    note "   goto — focus switches to the existing pane, no new window."

    summary
}

main "$@"
