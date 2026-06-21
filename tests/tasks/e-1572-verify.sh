#!/usr/bin/env bash
#
# E-1572 verification script — exercises the `task spawn --bg` soft-throttle
# warning end-to-end against the real compiled code:
#   - the new `endless-go session-query count-bg-agents` helper
#     (monitor.CountActiveBgAgents): integer output, the missing-flag refusal,
#     and the scope filters (kind=background, state='working', per-project).
#   - the Python `_bg_throttle_warn`: threshold read from
#     .endless/config.json:bg_throttle_warn (default 3, <=0 disables), the
#     3-line stderr advisory, warn-once-not-per-overage, and that it never
#     blocks (exit 0) nor raises on its own.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1572-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error.
#
# Design (following the E-1568 sibling): builds a throwaway DB under a temp dir
# via `endless-go --config-dir`, so runs are deterministic, pollution-free, and
# repeatable. It does NOT launch a real `claude --bg` agent — that live smoke
# test costs tokens and is not cleanly repeatable; it is documented as a manual
# step at the end. The Python warning is driven for real (real config read +
# real Go count subprocess), with `_resolve_endless_go` pinned to THIS
# worktree's binary so the new subcommand is exercised rather than a stale
# global install.
#
# Shape/output follow the E-1577 prototype referenced by E-1596.

set -u

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ───────────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }

report_pass() {
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" \
        "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"; else report_fail "$1" "$2" "$3"; fi
}

# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output contains: $2" "$3"; fi
}

# assert_not_contains DESC NEEDLE HAYSTACK
assert_not_contains() {
    if [[ "$3" != *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output does NOT contain: $2" "$3"; fi
}

# assert_fails DESC PATTERN -- CMD...  (CMD must exit non-zero AND match PATTERN)
assert_fails() {
    local desc="$1" pattern="$2"; shift 3   # drop desc, pattern, the literal --
    local out rc; out=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -ne 0 && "${out}" == *"${pattern}"* ]]; then report_pass "${desc}"
    else report_fail "${desc}" "exit!=0 AND output ~ ${pattern}" "exit=${rc} | ${out}"; fi
}

# ─── globals wired in main ───────────────────────────────────────────────────

REPO_ROOT=""
EGO=""          # path to the worktree-built endless-go
TMP=""

# eg: run endless-go against the throwaway DB at $TMP/endless.db
eg() { "${EGO}" --config-dir "${TMP}" "$@"; }

# q: run a SQL statement against $TMP/endless.db, print the scalar/row
q() { sqlite3 "${TMP}/endless.db" "$1"; }

# count: the integer printed by the count-bg-agents helper for a task id
count() { eg session-query count-bg-agents --task-id "$1" 2>&1; }

# insert_session PROJECT_ID KIND_ID STATE SHORT_ID
#   Raw-insert a session row so exclusion filters (ended / foreground / other
#   project) can be exercised directly, beyond what record-bg-agent produces.
insert_session() {
    q "INSERT INTO sessions (session_id, project_id, platform, state, kind_id, short_id, started_at)
       VALUES (NULL, $1, 'claude', '$3', $2, '$4', '2026-06-20T00:00:00');" >/dev/null 2>&1
}

# seed a fresh new-schema DB with three projects + one task each, carrying a
# controlled number of *working background* agents (recorded via the real
# record-bg-agent helper): p10/task110→2, p11/task111→3, p12/task112→5, plus
# p13/task113→0. The threshold-config dir lives at $TMP/.endless/config.json.
seed_fresh_db() {
    sqlite3 "${TMP}/endless.db" < "${REPO_ROOT}/internal/schema/schema.sql" >/dev/null
    sqlite3 "${TMP}/endless.db" "
        INSERT INTO projects (id, name, path, status) VALUES
            (10, 'p10', '/p10', 'active'),
            (11, 'p11', '/p11', 'active'),
            (12, 'p12', '/p12', 'active'),
            (13, 'p13', '/p13', 'active');
        INSERT INTO tasks (id, project_id, title, status, type_id) VALUES
            (110, 10, 't110', 'in_progress', 1),
            (111, 11, 't111', 'in_progress', 1),
            (112, 12, 't112', 'in_progress', 1),
            (113, 13, 't113', 'in_progress', 1);
    "
    local i
    for i in 1 2;          do eg session-query record-bg-agent --task-id 110 --short-id "p10-h${i}" >/dev/null 2>&1; done
    for i in 1 2 3;        do eg session-query record-bg-agent --task-id 111 --short-id "p11-h${i}" >/dev/null 2>&1; done
    for i in 1 2 3 4 5;    do eg session-query record-bg-agent --task-id 112 --short-id "p12-h${i}" >/dev/null 2>&1; done

    mkdir -p "${TMP}/.endless"
}

# write the project config the Python threshold read picks up. "unset" omits
# the key entirely (exercises the default).
write_threshold() {
    if [[ "$1" == "unset" ]]; then
        printf '{"name":"p10"}\n' > "${TMP}/.endless/config.json"
    else
        printf '{"name":"p10","bg_throttle_warn":%s}\n' "$1" > "${TMP}/.endless/config.json"
    fi
}

# warn_stderr TASK_ID  — run the real _bg_throttle_warn against the throwaway DB
# and echo only its stderr. _resolve_endless_go is pinned to this worktree's
# binary so the new count-bg-agents subcommand is exercised, not a stale global.
# Sets WARN_RC to the process exit code (must stay 0 — advisory never blocks).
WARN_RC=0
warn_stderr() {
    local err
    err=$(cd "${REPO_ROOT}" && uv run python -c '
import os, sys
from pathlib import Path
os.chdir(sys.argv[1])                       # threshold read via resolution_cwd()
from endless import config, task_cmd
import endless.event_bridge as eb
eb._resolve_endless_go = lambda: sys.argv[2] # pin worktree endless-go
config.set_db_context(Path(sys.argv[1]))     # DB + --config-dir context
task_cmd._bg_throttle_warn(int(sys.argv[3]))
' "${TMP}" "${EGO}" "$1" 2>&1 >/dev/null)
    WARN_RC=$?
    printf '%s' "${err}"
}

# ─── checks ──────────────────────────────────────────────────────────────────

test_count_helper_basics() {
    section "count-bg-agents — CLI surface"
    assert_fails "missing --task-id refused" "--task-id is required" -- \
        eg session-query count-bg-agents
    assert_eq "task with zero bg agents prints 0" "0" "$(count 113)"
    assert_contains "subcommand advertised in usage" "count-bg-agents" \
        "$(eg session-query --help 2>&1)"
}

test_count_correctness() {
    section "count-bg-agents — counts only working background, per project"
    assert_eq "project with 2 recorded bg agents → 2" "2" "$(count 110)"
    assert_eq "project with 3 recorded bg agents → 3" "3" "$(count 111)"
    assert_eq "project with 5 recorded bg agents → 5" "5" "$(count 112)"

    # An ended background agent must NOT count.
    insert_session 10 2 "ended" "p10-ended"
    assert_eq "ended background session excluded (still 2)" "2" "$(count 110)"

    # A working foreground (tmux, kind 1) session must NOT count.
    insert_session 10 1 "working" "p10-fg"
    assert_eq "working foreground session excluded (still 2)" "2" "$(count 110)"

    # A working background agent in another project must NOT bleed in.
    insert_session 13 2 "working" "p13-bg"
    assert_eq "bg agent in another project excluded (p10 still 2)" "2" "$(count 110)"
    assert_eq "that other project reflects its own (p13 → 1)" "1" "$(count 113)"
}

test_python_warning() {
    section "_bg_throttle_warn — threshold gate, advisory text, never blocks"

    # threshold 3, 2 active → silent
    write_threshold 3
    local out; out=$(warn_stderr 110)
    assert_eq "below threshold: no warning" "" "${out}"
    assert_eq "below threshold: exit 0 (never blocks)" "0" "${WARN_RC}"

    # threshold 3, 3 active → warns, full 3-line advisory
    out=$(warn_stderr 111)
    assert_contains "at threshold: warns with count"       "3 bg agents already active" "${out}"
    assert_contains "at threshold: warns with threshold"   "threshold: 3"               "${out}"
    assert_contains "at threshold: line 2 quota note"      "parallel-execution slot"    "${out}"
    assert_contains "at threshold: line 3 sweet-spot note" "sweet spot"                 "${out}"
    assert_contains "at threshold: line 3 names the config key" "bg_throttle_warn"      "${out}"
    assert_eq "at threshold: exit 0 (never blocks)" "0" "${WARN_RC}"

    # threshold 3, 5 active → warns ONCE (not per-overage)
    out=$(warn_stderr 112)
    assert_contains "over threshold: warns with count" "5 bg agents already active" "${out}"
    assert_eq "over threshold: exactly one 'warning:' line" \
        "1" "$(printf '%s\n' "${out}" | grep -c 'warning:')"

    # threshold 0 → disabled regardless of count
    write_threshold 0
    out=$(warn_stderr 112)
    assert_eq "threshold 0 disables (5 active, silent)" "" "${out}"

    # negative threshold → disabled
    write_threshold -1
    out=$(warn_stderr 112)
    assert_eq "negative threshold disables (5 active, silent)" "" "${out}"

    # unset key → default of 3, so 3 active warns
    write_threshold unset
    out=$(warn_stderr 111)
    assert_contains "unset key falls back to default 3 (warns)" "threshold: 3" "${out}"
}

# ─── main ────────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    cd "${REPO_ROOT}" || exit 2

    EGO="${REPO_ROOT}/bin/endless-go"
    [[ -x "${EGO}" ]] || { printf 'ERROR: %s not built — run `just go` first\n' "${EGO}" >&2; exit 2; }
    command -v sqlite3 >/dev/null 2>&1 || { printf 'ERROR: sqlite3 not on PATH\n' >&2; exit 2; }
    command -v uv      >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }

    TMP=$(mktemp -d)
    trap 'rm -rf "${TMP}"' EXIT
    seed_fresh_db

    printf '%sE-1572 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  repo:       %s\n  endless-go: %s\n  scratch:    %s (throwaway)\n' \
        "${REPO_ROOT}" "${EGO}" "${TMP}"

    test_count_helper_basics
    test_count_correctness
    test_python_warning

    printf '\n%sManual live smoke (not automated — launches a real bg agent, costs tokens):%s\n' "${DIM}" "${RESET}"
    printf '%s  1. set "bg_throttle_warn": 1 in .endless/config.json%s\n' "${DIM}" "${RESET}"
    printf '%s  2. claim an epic, then dispatch one child: endless task spawn --bg <child-id>%s\n' "${DIM}" "${RESET}"
    printf '%s     → first dispatch: no warning%s\n' "${DIM}" "${RESET}"
    printf '%s  3. dispatch a second child: endless task spawn --bg <child-id>%s\n' "${DIM}" "${RESET}"
    printf '%s     → stderr shows the 3-line warning, dispatch still succeeds%s\n' "${DIM}" "${RESET}"

    summary
}

main "$@"
