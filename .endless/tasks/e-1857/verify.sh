#!/usr/bin/env bash
#
# E-1857 verification — the machine-local diagnostic log (user-machine.jsonl).
#
# Run from anywhere inside the worktree:
#   endless task verify E-1857
#
# Stage 1 (fail-fast): the Go unit tests that exercise every session-write log
# site against a real SQLite DB and assert the real JSONL output. If they fail,
# the script stops before the slower end-to-end stage.
#
# Stage 2: a real end-to-end run — drive SessionStart+SessionEnd through the
# worktree-built endless-go hook and confirm a "session" line lands in the
# machine-local log with the expected shape (kind discriminator, old->new state,
# reason). Uses the worktree's self-dev sandbox (cwd self-detect, E-1368); it
# cleans up the probe lines it writes.
#
# Exit 0 on all-passed, 1 on any failure.

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
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── locate the worktree root ─────────────────────────────────────────────────

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WT_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"
cd "${WT_ROOT}" || { echo "cannot cd to worktree root ${WT_ROOT}"; exit 1; }

# ─── output helpers ───────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}
report_skip() { printf '  %s∼%s %s %s(%s)%s\n' "${DIM}" "${RESET}" "$1" "${DIM}" "$2" "${RESET}"; }

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'; return 1
}

# ─── Stage 1: Go unit tests (fail-fast) ───────────────────────────────────────

section "Stage 1 — Go unit tests (fail-fast)"

MONITOR_TESTS='TestLogSessionTxn_WritesJSONLine|TestSnapshotSession_CapturesOldState|TestSnapshotSession_MissingRow|TestIdleSession_LogsTransition|TestEndSession_LogsTransition|TestBindSessionToTask_LogsDedup'
EVENTS_TESTS='TestClaim_LogsActiveTaskRebind|TestRelease_LogsClear'

if go test ./internal/monitor/ -run "${MONITOR_TESTS}" -count=1 >/tmp/e1857-mon.log 2>&1; then
    report_pass "monitor log tests (LogSessionTxn, SnapshotSession, Idle/End, dedup)"
else
    report_fail "monitor log tests" "go test PASS" "$(tail -5 /tmp/e1857-mon.log)"
fi

if go test ./internal/events/ -run "${EVENTS_TESTS}" -count=1 >/tmp/e1857-evt.log 2>&1; then
    report_pass "events log tests (claim rebind, release clear)"
else
    report_fail "events log tests" "go test PASS" "$(tail -5 /tmp/e1857-evt.log)"
fi

if [[ "${FAIL_COUNT}" -ne 0 ]]; then
    printf '\n%sfail-fast: unit tests failed; skipping end-to-end stage%s\n' "${RED}" "${RESET}"
    summary; exit 1
fi

# ─── Stage 2: real end-to-end through the binary ──────────────────────────────

section "Stage 2 — end-to-end through endless-go hook"

GOBIN="${WT_ROOT}/bin/endless-go"
if [[ ! -x "${GOBIN}" ]]; then
    report_fail "worktree binary present" "bin/endless-go executable" "missing — run: just build"
    summary; exit 1
fi

# The binary self-detects the self-dev sandbox from cwd (E-1368) when XDG is
# unset. Derive the same sandbox's log path so we can read what it writes.
gobin() { env -u XDG_CONFIG_HOME "${GOBIN}" "$@"; }
SANDBOX_DIR="${HOME}/.cache/endless/sandboxes/$(basename "${WT_ROOT}")"
LOG_FILE="${SANDBOX_DIR}/endless/log/user-machine.jsonl"

if [[ ! -d "${SANDBOX_DIR}" ]]; then
    report_skip "end-to-end hook probe" "no self-dev sandbox at ${SANDBOX_DIR}"
    summary; exit $?
fi

PROBE="e2e-1857-probe-$$-${RANDOM}"
BEFORE=0
[[ -f "${LOG_FILE}" ]] && BEFORE=$(wc -l < "${LOG_FILE}")

printf '{"session_id":"%s","cwd":"%s","hook_event_name":"SessionStart","source":"startup"}\n' \
    "${PROBE}" "${WT_ROOT}" | gobin hook claude >/dev/null 2>&1
printf '{"session_id":"%s","cwd":"%s","hook_event_name":"SessionEnd"}\n' \
    "${PROBE}" "${WT_ROOT}" | gobin hook claude >/dev/null 2>&1

PROBE_LINES=""
[[ -f "${LOG_FILE}" ]] && PROBE_LINES=$(grep -F "\"${PROBE}\"" "${LOG_FILE}" 2>/dev/null)

END_LINE=$(printf '%s\n' "${PROBE_LINES}" | grep '"reason":"end"' | head -1)

if [[ -n "${END_LINE}" ]]; then
    report_pass "hook SessionEnd appended a diagnostic line"
else
    report_fail "hook SessionEnd appended a diagnostic line" \
        "a line with session_id=${PROBE} reason=end" "${PROBE_LINES:-<none>}"
fi

if printf '%s' "${END_LINE}" | grep -q '"kind":"session"'; then
    report_pass "line carries the kind discriminator (kind=session)"
else
    report_fail "line carries the kind discriminator" '"kind":"session"' "${END_LINE:-<none>}"
fi

if printf '%s' "${END_LINE}" | grep -q '"old_state":"working","new_state":"ended"'; then
    report_pass "line records the old->new state transition (working->ended)"
else
    report_fail "line records old->new state" '"old_state":"working","new_state":"ended"' "${END_LINE:-<none>}"
fi

# ─── cleanup: strip the probe's lines from the sandbox log ─────────────────────
#
# A live self-dev Claude session writes to this same log concurrently, so the
# absolute line count is not a reliable check; assert instead that none of THIS
# probe's lines survive. grep -v is not gated on its exit status: it returns 1
# when it filters out every line (which happens when the log held only probe
# lines), and that is success here, not an error.

if [[ -f "${LOG_FILE}" ]]; then
    grep -vF "\"${PROBE}\"" "${LOG_FILE}" > "${LOG_FILE}.tmp" 2>/dev/null
    mv "${LOG_FILE}.tmp" "${LOG_FILE}"
    REMAINING=$(grep -cF "\"${PROBE}\"" "${LOG_FILE}" 2>/dev/null || true)
    if [[ "${REMAINING}" -eq 0 ]]; then
        report_pass "probe lines cleaned up (none remain)"
    else
        report_fail "probe lines cleaned up" "0 probe lines remain" "${REMAINING} remain"
    fi
fi

summary
