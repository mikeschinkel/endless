#!/usr/bin/env bash
#
# E-1699 verification suite — the SINGLE entry point for verifying E-1699.
#
#   esu
#   ./tests/tasks/e-1699-verify.sh
#
# Bug: the live `session monitor` redraw (internal/sessionstatuscmd/session_status.go)
# did `\x1b[H` + frame + `\x1b[J`. The trailing `\x1b[J` erases to end-of-DISPLAY
# (killing whole rows below a now-shorter frame) but does NOT erase each line to
# end-of-LINE, so when a row's new title is shorter than the prior frame's on that
# row, the old tail survived (E-1461's row kept E-1698's leftover title text).
# Fix: wrap the frame through eraseEachLineToEOL(), which inserts `\x1b[K` before
# every newline plus one after the last line; the trailing `\x1b[J` still handles
# the fewer-rows case. The one-shot `session status` snapshot renderer is untouched.
#
# What this proves:
#   1. Worktree builds clean.
#   2. eraseEachLineToEOL produces the exact escape-wrapped bytes (unit test),
#      and a modeled VT100 line buffer confirms a shorter title leaves no stale
#      tail — TestEraseEachLineToEOL.
#   3. The wider sessionstatuscmd package still passes (no regression).
#   4. End-to-end in a REAL terminal (tmux capture-pane): repainting a shorter
#      frame with the OLD escapes leaves a stale tail; with the fix's escapes it
#      does not. This proves the escape choice is correct in an actual VTE, not
#      just in the Go string transform.

set -u

# ─── locate worktree ──────────────────────────────────────────────────────────

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
WT_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
cd "${WT_ROOT}" || { printf 'ERROR: cannot cd to %s\n' "${WT_ROOT}" >&2; exit 1; }

# ─── output ─────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}

# assert_cmd DESC CMD [ARGS...] — pass if CMD exits 0; on failure show the last
# few lines of its output so a regression is diagnosable from this report alone.
assert_cmd() {
    local desc="$1"; shift
    local out rc
    out=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "exit 0" "exit=${rc} | $(printf '%s\n' "${out}" | tail -8 | tr '\n' '⏎')"
    fi
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── checks ─────────────────────────────────────────────────────────────────

section "Build — worktree binaries (just build)"
if ! build_out=$(just build 2>&1); then
    report_fail "just build" "exit 0" "$(printf '%s\n' "${build_out}" | tail -8 | tr '\n' '⏎')"
    summary
    exit 1
fi
report_pass "just build"

section "Go unit tests — repaint erases each line to end-of-line"
assert_cmd "sessionstatuscmd: eraseEachLineToEOL bytes + no stale tail (TestEraseEachLineToEOL)" \
    go test ./internal/sessionstatuscmd/ -count=1 -run 'TestEraseEachLineToEOL'
assert_cmd "sessionstatuscmd: full package still passes (no regression)" \
    go test ./internal/sessionstatuscmd/ -count=1

# ─── end-to-end terminal proof (tmux) ─────────────────────────────────────────
#
# Render a long frame, then repaint a SHORTER frame over it and capture the pane's
# cell contents. The OLD escapes (home + frame + \x1b[J) leave a stale tail; the
# fix's escapes (\x1b[K before each newline + one after) do not. This exercises a
# real terminal emulator, complementing the Go string-transform unit test.

section "End-to-end — real terminal repaint (tmux capture-pane)"
if ! command -v tmux >/dev/null; then
    report_fail "tmux available for terminal proof" "tmux on PATH" "tmux not found (skipped)"
else
    K=$'\033[K'
    LONG='Row A: E-1698 leftover long title here'
    NEWTITLE='Row A: E-1461'
    STALE='leftover long title here'

    run_repaint() {  # $1 = repaint payload (frame2 with escapes); echoes captured pane
        local payload="$1" sess="e1699_$$_${RANDOM}" script
        script=$(mktemp)
        {
            printf '#!/bin/sh\n'
            printf "printf 'Row A: E-1698 leftover long title here\\\\n'\n"
            printf 'sleep 0.4\n'
            printf 'printf %q\n' "${payload}"
            printf 'sleep 2\n'
        } > "${script}"
        chmod +x "${script}"
        tmux kill-session -t "${sess}" 2>/dev/null
        tmux new-session -d -s "${sess}" -x 80 -y 6 "${script}"
        sleep 0.9
        tmux capture-pane -t "${sess}" -p
        tmux kill-session -t "${sess}" 2>/dev/null
        rm -f "${script}"
    }

    # Buggy escapes: home + shorter frame + clear-to-end-of-display (no per-line \x1b[K).
    buggy=$(run_repaint $'\033[H'"${NEWTITLE}"$'\n\033[J')
    if printf '%s\n' "${buggy}" | grep -qF "${STALE}"; then
        report_pass "OLD escapes reproduce the stale tail (bug present without fix)"
    else
        report_fail "OLD escapes reproduce the stale tail" \
            "captured pane contains '${STALE}'" "$(printf '%s' "${buggy}" | head -2 | tr '\n' '⏎')"
    fi

    # Fixed escapes: home + \x1b[K before each newline + one after + \x1b[J.
    fixed=$(run_repaint $'\033[H'"${NEWTITLE}${K}"$'\n'"${K}"$'\033[J')
    if printf '%s\n' "${fixed}" | grep -qF "${STALE}"; then
        report_fail "FIX escapes erase the stale tail" \
            "captured pane free of '${STALE}'" "$(printf '%s' "${fixed}" | head -2 | tr '\n' '⏎')"
    else
        report_pass "FIX escapes erase the stale tail (no leftover after shorter repaint)"
    fi
fi

summary
