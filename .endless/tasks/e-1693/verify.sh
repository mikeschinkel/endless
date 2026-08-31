#!/usr/bin/env bash
#
# E-1693 verification suite — the SINGLE entry point for verifying E-1693.
#
#   esu
#   endless task verify E-1693
#
# Self-contained: it builds the worktree binaries, runs the Go unit tests for
# the landed-column query + classify routing, then drives the real worktree-built
# endless-go binary end-to-end against a throwaway temp DB built from the shipped
# internal/schema/schema.sql. Nothing in the sandbox or real ledger is touched.
# Exit 0 on all-passed, 1 on any failure (with detail to diagnose).
#
# What it proves (E-1693: a landed task must not be offered as a fresh action):
#   1. Worktree builds clean.
#   2. monitor.SessionStatusRows selects the `landed` fact and classify() routes
#      a landed non-terminal task to ⁇ other? — internal/{monitor,sessionstatuscmd}
#      unit tests.
#   3. `session-status --task E-NNN` rendered end-to-end:
#        - a landed ready child renders ⁇ (other?), NOT ▶ (do),
#        - it is STILL visible (a landed non-terminal task is not omitted),
#        - an un-landed ready sibling still renders ▶ (do) — no regression,
#        - decoration precedence holds: a landed task a live session is on still
#          renders ⟳ (doing), proving the landed check yields to the in-flight
#          decoration.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

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

# render — run session-status against the temp DB for focal epic E-99. The
# headless --task flag (E-1685) names the focal directly AND skips PinMainDB, so
# session-status reads the resolved --config-dir context (this seeded DB) instead
# of main. NO_COLOR + a wide --cols keep the output ANSI-free and untruncated so
# the leading-icon checks are reliable. --task takes a bare integer id.
render() { NO_COLOR=1 "${BIN}" --config-dir "${CFG}" session-status --cols 200 --task 99; }

# row_for ID OUTPUT — the rendered row line for task E-ID, or "" if absent. The id
# is left-justified to 6 cols then a space, so the trailing space disambiguates
# E-100 from E-1000.
row_for() { printf '%s\n' "$2" | grep -E "E-$1 " | head -1; }

# assert_row_icon DESC ID ICON OUTPUT — pass if the E-ID row exists AND its leading
# glyph (the action icon, first field of the row) is ICON.
assert_row_icon() {
    local desc="$1" id="$2" icon="$3" output="$4" row
    row=$(row_for "${id}" "${output}")
    if [[ "${row}" == "${icon} "* ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "E-${id} row begins with '${icon} '" "${row:-<no E-${id} row>}"
    fi
}

# seed_base builds a fresh DB with a project, a focal epic E-99 (underway), a
# ready child E-100 (the one we land), and an un-landed ready sibling child E-101.
# Children surface in the focal's view via the read-time children UNION (E-1691),
# so no session_tasks rows are needed.
seed_base() {
    rm -f "${DB}"
    sqlite3 "${DB}" < "${SCHEMA}" >/dev/null
    sqlite3 "${DB}" "
        INSERT INTO projects (id, name, path, status, created_at, updated_at)
          VALUES (1, 'p', '${TMP}', 'active', '2026-06-30T00:00:00', '2026-06-30T00:00:00');
        INSERT INTO tasks (id, project_id, title, status, phase, type_id, parent_id, created_at, updated_at) VALUES
          (99,  1, 'focal-epic',      'underway', 'now', 4, NULL, '2026-06-30T00:00:00', '2026-06-30T00:00:00'),
          (100, 1, 'landed-child',    'ready',    'now', 1, 99,   '2026-06-30T00:00:00', '2026-06-30T00:00:00'),
          (101, 1, 'unlanded-child',  'ready',    'now', 1, 99,   '2026-06-30T00:00:00', '2026-06-30T00:00:00');
    "
}

# land ID — append one task_landings row for task ID (its work has merged). Mirrors
# what the task.landed projection writes; here we insert directly since a real
# `worktree land` is out of reach in a test. landed_at takes its schema default.
land() {
    sqlite3 "${DB}" "INSERT INTO task_landings (id, task_id, session_id, branch, merge_commit_sha)
                     VALUES ($1, $1, NULL, 'task/$1-x', 'deadbeef$1');"
}

# live_session_on ID — a working session whose active_task_id = ID, making ID
# in_flight in the query (drives the ⟳ doing decoration).
live_session_on() {
    sqlite3 "${DB}" "INSERT INTO sessions (id, session_id, state, project_id, active_task_id)
                     VALUES ($1, 'verify-1693-$1', 'working', 1, $1);"
}

# ─── checks ─────────────────────────────────────────────────────────────────

section "Build — worktree binaries (just build)"
if ! build_out=$(just build 2>&1); then
    report_fail "just build" "exit 0" "$(printf '%s\n' "${build_out}" | tail -8 | tr '\n' '⏎')"
    summary
    exit 1
fi
report_pass "just build"

section "Go unit tests — landed column + classify routing + icon"
assert_cmd "go test internal/monitor (TestSessionStatusRows_LandedColumn)" \
    go test ./internal/monitor/ -count=1 -run 'TestSessionStatusRows_LandedColumn'
assert_cmd "go test internal/sessionstatuscmd (TestClassify + TestActionIcons)" \
    go test ./internal/sessionstatuscmd/ -count=1 -run 'TestClassify|TestActionIcons'

section "Landed ready child renders ⁇ other? (NOT ▶ do) and stays visible"
# E-100 is a ready child of the focal epic AND has landed; E-101 is an identical
# ready child that has NOT landed.
seed_base
land 100
OUT=$(render)
assert_row_icon "landed child E-100 renders ⁇ (other?)" 100 "⁇" "${OUT}"
assert_row_icon "un-landed sibling E-101 still renders ▶ (do)" 101 "▶" "${OUT}"
# Explicit not-▶ on the landed row (the false-spawn this task fixes).
if [[ "$(row_for 100 "${OUT}")" == "▶ "* ]]; then
    report_fail "landed child E-100 is NOT offered as ▶ do" "E-100 row does not begin with '▶ '" "$(row_for 100 "${OUT}")"
else
    report_pass "landed child E-100 is NOT offered as ▶ do"
fi
# Still visible: a landed non-terminal task is shown, not omitted.
if [[ -n "$(row_for 100 "${OUT}")" ]]; then
    report_pass "landed child E-100 is still visible (not omitted)"
else
    report_fail "landed child E-100 is still visible (not omitted)" "a row for E-100" "<no E-100 row>"
fi

section "Decoration precedence — a landed task a live session is on still reads ⟳ doing"
# Same landed child, but now a working session is active on it: the in-flight
# decoration must win over the landed check.
seed_base
land 100
live_session_on 100
OUT=$(render)
assert_row_icon "landed + in-flight child E-100 renders ⟳ (doing), not ⁇" 100 "⟳" "${OUT}"

summary
