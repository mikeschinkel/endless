#!/usr/bin/env bash
#
# E-1682 verification script — durable session-navigation trail (manual + goto).
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-1682
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error.
#
# WHY this shape: the recorder fires on real tmux focus changes and writes the
# nav DB. To stay non-disruptive, the end-to-end checks drive the recorder
# directly against a THROWAWAY DB (--config-dir <tempdir>, which the binary
# routes to instead of the real ledger — the E-1682 PinMainDB carve-out) and,
# where a tmux server is needed (the @endless_nav_via marker), an ISOLATED
# `tmux -L` server that is killed at the end. Nothing here changes your focus,
# touches your real tmux server, or writes to your real ledger.
#
# This is the single comprehensive gate for E-1682: it runs `just build`, the
# full `go test ./...` and `just test` suites (which subsume the E-1682 unit
# tests — recorder, reader, enum/schema integrity, hook wiring, trail rendering,
# goto marker), then the binary/tmux E2E checks those suites can't cover. So
# `endless task verify E-1682` proves the whole task end to end.
# Modeled on .endless/tasks/e-1681/verify.sh.

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

BIN=""        # absolute path to bin/endless-go (set in main)
TMPDB=""      # throwaway --config-dir for the E2E checks (set in main)

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

test_build() {
    section "Build — \`just build\` (Go binaries)"
    note "produces bin/endless-go, which the E2E sections below drive"
    assert_succeeds "just build" just build
}

test_full_suites() {
    section "Full test suites — complete regression gate"
    note "go test ./... and the full Python suite; these subsume the E-1682 unit"
    note "tests — the recorder write path (endpoint resolution, from-chaining,"
    note "no-op dedup, via tag), the reader (newest-first, scoping, labels), the"
    note "NavVia enum + schema integrity, the apply hook wiring, trail rendering,"
    note "and the goto marker"
    assert_succeeds "go test ./..." \
        go test ./...
    assert_succeeds "just test (full Python suite)" \
        just test
}

test_cli_wiring() {
    section "CLI wiring — \`session trail\` registered"
    assert_contains "\`session --help\` lists \`trail\`" "trail" \
        uv run endless session --help
    assert_contains "\`session trail --help\` documents the via tag" \
        "via" uv run endless session trail --help
    assert_contains "\`session trail --help\` documents --all" \
        "--all" uv run endless session trail --help
}

test_recorder_e2e() {
    section "Recorder E2E — writes an edge to a throwaway DB, reader reads it"
    note "drives \`tmux record-nav\` directly against --config-dir ${TMPDB##*/};"
    note "no tmux server needed, nothing touches the real ledger"

    assert_succeeds "record-nav (untracked pane) exits 0" \
        "${BIN}" --config-dir "${TMPDB}" tmux record-nav \
            --client verify-cli --pane "%verify1"
    assert_contains "trail (scoped) shows the recorded pane" "%verify1" \
        "${BIN}" --config-dir "${TMPDB}" session-query trail --client verify-cli
    assert_contains "trail (scoped) tags it via=manual (no goto marker)" "manual" \
        "${BIN}" --config-dir "${TMPDB}" session-query trail --client verify-cli

    # A second move to the SAME pane is a no-op (collapses paired focus hooks).
    "${BIN}" --config-dir "${TMPDB}" tmux record-nav \
        --client verify-cli --pane "%verify1" >/dev/null 2>&1
    local count
    count=$("${BIN}" --config-dir "${TMPDB}" session-query trail \
        --client verify-cli 2>/dev/null | grep -o '"id":' | wc -l | tr -d ' ')
    assert_eq "repeat move to same pane does not insert a second row" "1" "${count}"
}

test_marker_e2e() {
    section "Goto marker E2E — isolated tmux server, recorder reads + clears it"
    note "uses \`tmux -L\` on a throwaway socket (killed below); never touches"
    note "your real tmux server or focus"

    local sock="endless-nav-verify-$$"
    if ! tmux -L "${sock}" new-session -d -s s0 -x 80 -y 24 2>/dev/null; then
        report_fail "isolated tmux server starts" "tmux -L new-session exit 0" "failed"
        return
    fi
    report_pass "isolated tmux server starts"

    tmux -L "${sock}" set-option -g @endless_nav_via goto 2>/dev/null
    # run-shell executes with TMUX pointed at this isolated server, so the
    # recorder's marker read/clear targets it (not your real server).
    tmux -L "${sock}" run-shell \
        "${BIN} --config-dir ${TMPDB} tmux record-nav --client verify-goto --pane %verifyG" \
        2>/dev/null
    sleep 0.3

    assert_eq "recorder cleared the one-shot @endless_nav_via marker" \
        "" "$(tmux -L "${sock}" show-options -gqv @endless_nav_via 2>/dev/null)"
    assert_contains "the goto move is tagged via=goto in the trail" "goto" \
        "${BIN}" --config-dir "${TMPDB}" session-query trail --client verify-goto
    assert_contains "the goto move records its destination pane" "%verifyG" \
        "${BIN}" --config-dir "${TMPDB}" session-query trail --client verify-goto

    tmux -L "${sock}" kill-server 2>/dev/null
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

    local tool
    for tool in just go uv; do
        if ! command -v "${tool}" >/dev/null 2>&1; then
            printf 'ERROR: %s not on PATH\n' "${tool}" >&2
            exit 2
        fi
    done

    # bin/endless-go is (re)built by test_build below; the E2E sections drive it.
    BIN="${repo_root}/bin/endless-go"

    TMPDB=$(mktemp -d) || exit 2
    trap 'rm -rf "${TMPDB}"' EXIT

    printf '%sE-1682 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      throwaway (%s); real ledger untouched\n' "${TMPDB}"
    printf '  tmux:    %s\n' "$([[ -n "${TMUX:-}" ]] && echo inside || echo 'not inside')"

    test_build
    test_full_suites
    test_cli_wiring
    test_recorder_e2e

    if command -v tmux >/dev/null 2>&1; then
        test_marker_e2e
    else
        section "Goto marker E2E — SKIPPED (tmux not on PATH)"
    fi

    summary
}

main "$@"
