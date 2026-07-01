#!/usr/bin/env bash
#
# E-1698 verification suite — the SINGLE entry point for verifying E-1698.
#
#   esu
#   ./tests/tasks/e-1698-verify.sh
#
# Bug: in a tmux window whose status line resolves NO active task (renders the
# placeholder `·`), `endless session status` / `monitor` still rendered a task
# list — the rows of an UNRELATED task (the most-recently-active session's), via
# ResolveSessionStatusFocal's step-3 machine-wide last resort (ED-1523). Fix:
# route focal resolution through the SAME pane-scoped path the status line uses
# (monitor.GetPaneStatus), drop the machine-wide + @endless_task_id fallbacks for
# the list, and render a claim/bind hint (mirroring the bar) with ZERO rows.
#
# What this proves:
#   1. Worktree builds clean.
#   2. monitor.ResolveSessionStatusFocal: an unrelated live session is NOT
#      returned for a pane with no session of its own (→ focal 0, PaneStatusNone);
#      an active pane resolves its OWN task; a session with no task → focal 0 +
#      PaneStatusNoTask — the resolver unit tests.
#   3. The renderer prints the claim/bind hint (never an unrelated list) when no
#      focal resolves — sessionstatuscmd unit test.
#   4. End-to-end: the headless `--task <id>` path is UNCHANGED — a named task
#      still renders its own rows (no regression).
#
# Note on scope: the LIVE (non-`--task`) no-task path pins the real main DB
# (monitor.PinMainDB), so it cannot be driven against a throwaway temp DB without
# polluting the real ledger. That path is covered by the resolver unit tests in
# (2) plus the manual contrast documented at the end of this script.

set -u

# ─── locate worktree ──────────────────────────────────────────────────────────

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
WT_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
cd "${WT_ROOT}" || { printf 'ERROR: cannot cd to %s\n' "${WT_ROOT}" >&2; exit 1; }
BIN="${WT_ROOT}/bin/endless-go"
SCHEMA="${WT_ROOT}/internal/schema/schema.sql"

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

# ─── temp environment ─────────────────────────────────────────────────────────

command -v sqlite3 >/dev/null || { printf 'ERROR: sqlite3 required to seed the test DB\n' >&2; exit 1; }

TMP=$(mktemp -d)
trap 'rm -rf "${TMP}"' EXIT
CFG="${TMP}/config"
mkdir -p "${CFG}"
DB="${CFG}/endless.db"

# ─── checks ─────────────────────────────────────────────────────────────────

section "Build — worktree binaries (just build)"
if ! build_out=$(just build 2>&1); then
    report_fail "just build" "exit 0" "$(printf '%s\n' "${build_out}" | tail -8 | tr '\n' '⏎')"
    summary
    exit 1
fi
report_pass "just build"

section "Go unit tests — resolver drops the unrelated-task fallback"
assert_cmd "monitor: unrelated pane → no focal (TestRepro_E1698_UnrelatedFocalFallback)" \
    go test ./internal/monitor/ -count=1 -run 'TestRepro_E1698_UnrelatedFocalFallback'
assert_cmd "monitor: active pane resolves own task (TestResolveSessionStatusFocal_ActivePaneResolves)" \
    go test ./internal/monitor/ -count=1 -run 'TestResolveSessionStatusFocal_ActivePaneResolves'
assert_cmd "monitor: session-no-task → PaneStatusNoTask (TestResolveSessionStatusFocal_SessionNoTaskResolvesNoTaskKind)" \
    go test ./internal/monitor/ -count=1 -run 'TestResolveSessionStatusFocal_SessionNoTaskResolvesNoTaskKind'
assert_cmd "sessionstatuscmd: no-focal render shows claim/bind hint (TestRenderEmptyFocal)" \
    go test ./internal/sessionstatuscmd/ -count=1 -run 'TestRenderEmptyFocal'

section "End-to-end — headless --task path is unchanged (no regression)"
# Seed a throwaway DB with a single task E-77 and render it via the headless
# --task path (which names the focal directly and skips PinMainDB, so it reads
# this --config-dir DB). The fix must NOT touch this path: E-77's own row shows.
rm -f "${DB}"
sqlite3 "${DB}" < "${SCHEMA}" >/dev/null
sqlite3 "${DB}" "
    INSERT INTO projects (id, name, path, status, created_at, updated_at)
      VALUES (1, 'p', '${TMP}', 'active', '2026-06-30T00:00:00', '2026-06-30T00:00:00');
    INSERT INTO tasks (id, project_id, title, status, phase, type_id, created_at, updated_at)
      VALUES (77, 1, 'named-focal', 'underway', 'now', 1, '2026-06-30T00:00:00', '2026-06-30T00:00:00');
"
OUT=$(NO_COLOR=1 "${BIN}" --config-dir "${CFG}" session-status --cols 200 --task 77 2>&1)
if printf '%s\n' "${OUT}" | grep -qE 'E-77 '; then
    report_pass "--task 77 still renders E-77's own row"
else
    report_fail "--task 77 still renders E-77's own row" "a row for E-77" "${OUT}"
fi
# And it must NOT print the claim/bind hint when a task IS named.
if printf '%s\n' "${OUT}" | grep -qi 'claim or bind'; then
    report_fail "--task 77 does NOT show the no-task hint" "no claim/bind hint" "${OUT}"
else
    report_pass "--task 77 does NOT show the no-task hint"
fi

summary
