#!/usr/bin/env bash
#
# E-1542 verification script — pause-on-revisit hook + `endless task
# continue` / `endless task pause` verbs.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1542-verify.sh
#
# This is the single entry point for verifying E-1542 (per E-1596): it ensures
# the binaries are current, runs the Go + Python automated suites, and then
# drives the verbs end-to-end through the real CLI -> worktree endless-go ->
# sandbox DB path. Output: pass/fail per check, then a summary. Exit 0 on
# all-passed, 1 on any failure.
#
# What it does NOT cover (one irreducible manual step): whether a *live* Claude
# session honors the PreToolUse decision:"block" + additionalContext response
# and renders the AskUserQuestion. The block-response SHAPE and the gate
# decision logic are covered by the Go tests run below
# (TestRevisitBlockResponse_Shape, TestRevisitGateDecision_Lifecycle); the live
# render is the E-1542 §4/§5 verification the operator confirms by hand. The
# hook is NOT exercised here directly because `endless-go hook` pins to the real
# main DB (PinMainDB) and would write to the real ledger.
#
# Model: tests/tasks/e-1577-verify.sh (the E-1596 reference prototype).

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
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
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
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}"
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

# Create a task and emit just its numeric id on stdout (E-NNN with the prefix
# stripped). Other output goes to stderr.
add_task_get_id() {
    local title="$1"; shift
    local output rc eid
    output=$(endless task add "${title}" "$@" 2>&1)
    rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        printf 'ERROR: add failed for %q: %s\n' "${title}" "${output}" >&2
        return 1
    fi
    eid=$(printf '%s\n' "${output}" | grep -oE 'E-[0-9]+' | head -1)
    printf '%s\n' "${eid#E-}"
}

# sandbox SQL: read a single scalar / write a mutation.
sql_read()  { endless sql "$1" --tsv 2>/dev/null | tail -1; }
sql_write() { endless sql --write "$1" >/dev/null 2>&1; }

seed_gate()       { sql_write "INSERT INTO session_gates (session_id,kind_id,epic_id,triggered_at) VALUES ($1,1,$2,'2026-06-23T00:00:00')"; }
open_gates()      { sql_read "SELECT count(*) FROM session_gates WHERE session_id=$1 AND cleared_at IS NULL"; }
last_cleared_by() { sql_read "SELECT COALESCE(cleared_by,'') FROM session_gates WHERE session_id=$1 ORDER BY id DESC LIMIT 1"; }

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_succeeds DESC CMD [ARGS...]
assert_succeeds() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

# assert_contains DESC PATTERN CMD [ARGS...]
assert_contains() {
    local desc="$1" pattern="$2"; shift 2
    local output
    output=$("$@" 2>&1)
    if [[ "${output}" == *"${pattern}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output contains: ${pattern}" "${output}"
}

# assert_eq DESC EXPECTED ACTUAL  (literal string equality of captured DB state)
assert_eq() {
    local desc="$1" expected="$2" actual="$3"
    if [[ "${actual}" == "${expected}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${expected}" "${actual}"
}

# ─── build + automated suites (folds former build/unit-test/hook-shape steps) ─

test_build_and_suites() {
    section "Build & automated suites"

    if [[ ! -f bin/endless-go ]] || [[ -n "$(find . -name '*.go' -not -path './vendor/*' -newer bin/endless-go 2>/dev/null | head -1)" ]]; then
        assert_succeeds "just build (binaries stale)" just build
    else
        report_pass "binaries up to date (skipping build)"
    fi

    # Go: enum, monitor helpers, hook decision + block-response shape.
    assert_succeeds "go test ./internal/{gatekind,monitor,hookcmd}" \
        go test ./internal/gatekind/... ./internal/monitor/... ./internal/hookcmd/...

    # Python: verb wiring + no-pending message + gate-clear arg threading.
    assert_succeeds "pytest tests/test_revisit_verbs.py" \
        uv run pytest tests/test_revisit_verbs.py -q
}

# ─── schema + enum mirror present in the sandbox ────────────────────────────

test_schema() {
    section "Schema & enum mirror (sandbox)"
    # A real `task add` routes through the worktree endless-go, which applies
    # schema.sql on connect — so the new tables exist in the sandbox.
    assert_eq "gate_kinds + session_gates exist" "gate_kinds session_gates" \
        "$(sql_read "SELECT group_concat(name,' ') FROM (SELECT name FROM sqlite_master WHERE type='table' AND name IN ('gate_kinds','session_gates') ORDER BY name)")"
    assert_eq "gate_kinds seeds revisit=1" "revisit" \
        "$(sql_read "SELECT slug FROM gate_kinds WHERE id=1")"
}

# ─── verb end-to-end (continue / pause / friendly no-op) ────────────────────

test_verbs_e2e() {
    section "Verbs E2E — continue / pause / no-pending (real CLI -> Go -> sandbox)"

    local sid=990542 epicn pid
    epicn=$(add_task_get_id "Build e-1542 verify epic" --type epic) || return
    pid=$(sql_read "SELECT project_id FROM tasks WHERE id=${epicn}")

    # A session bound to the epic task. INSERT OR REPLACE keeps the script
    # idempotent across reruns with the same fixed sid.
    sql_write "INSERT OR REPLACE INTO sessions (id,session_id,project_id,platform,state,active_task_id,started_at) VALUES (${sid},'e1542-verify',${pid},'claude','working',${epicn},'2026-06-23T00:00:00')"

    export ENDLESS_SESSION_ID=${sid}

    # 1. continue with no open gate -> friendly no-op.
    assert_contains "continue is friendly with no pending gate" \
        "No pending revisit prompt" endless task continue

    # 2. open gate -> continue clears it (revisit_continue), no release.
    seed_gate "${sid}" "${epicn}"
    assert_contains "continue clears the gate" \
        "continuing under the current plan" endless task continue
    assert_eq "no open gate after continue" "0" "$(open_gates "${sid}")"
    assert_eq "cleared_by=revisit_continue" "revisit_continue" "$(last_cleared_by "${sid}")"
    assert_eq "continue does NOT release the task" "${epicn}" \
        "$(sql_read "SELECT COALESCE(active_task_id,'') FROM sessions WHERE id=${sid}")"

    # 3. open gate + bound task -> pause clears it (revisit_pause) AND releases.
    seed_gate "${sid}" "${epicn}"
    sql_write "UPDATE sessions SET active_task_id=${epicn} WHERE id=${sid}"
    assert_contains "pause clears the gate" \
        "pausing until the strategy is re-set" endless task pause
    assert_eq "no open gate after pause" "0" "$(open_gates "${sid}")"
    assert_eq "cleared_by=revisit_pause" "revisit_pause" "$(last_cleared_by "${sid}")"
    assert_eq "pause releases the task (active_task_id NULL)" "NULL" \
        "$(sql_read "SELECT COALESCE(active_task_id,'NULL') FROM sessions WHERE id=${sid}")"

    unset ENDLESS_SESSION_ID

    # cleanup the synthetic session + its gate rows (the epic task is left in
    # the sandbox like e-1577 — bounded, inspectable).
    sql_write "DELETE FROM session_gates WHERE session_id=${sid}"
    sql_write "DELETE FROM sessions WHERE id=${sid}"
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

    printf '%sE-1542 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'

    test_build_and_suites
    test_schema
    test_verbs_e2e

    summary
}

main "$@"
