#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1901 and records what was true when E-1901
# landed. Edit it only if you ARE E-1901. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1901 verification script — the verbatim-report-relay Stop gate.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1901
#
# Single entry point (per E-1596). Fail-fast on the unit contract (the
# comparison matrix is where correctness actually lives), then the schema, the
# report's new shape, and finally the whole mechanism driven end-to-end through
# the real CLI -> worktree endless-go -> sandbox DB: run `task report`, confirm
# it armed the gate, then feed the Stop hook a compliant message and a violating
# one and assert what each does. Exit 0 on all-passed, 1 on any failure.
#
# Why the hook runs with an explicit --config-dir: `endless-go hook` calls
# PinMainDB (E-1450/E-1429) so hook-fired writes always hit the REAL DB
# regardless of cwd. HasExplicitDBContext is the documented seam for exactly
# this case — an explicit --config-dir beats the main pin, which is what lets a
# test drive the hook against the sandbox instead of the user's real ledger.
#
# Honest limit (by design, per the task's analysis): these tests assert the gate
# BLOCKS and names the violation. They cannot assert a live model never appends
# again — a re-prompted model can comply now and drift later, and the bounce cap
# means a determined violation eventually lands. What is verifiable, and is
# verified here, is that appending is caught, quantified, and named to both the
# agent and the user every time.
#
# Model: .endless/tasks/e-1803/verify.sh.

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

# A fixed synthetic session UUID so the end-to-end path does not depend on this
# script being run from inside a Claude session (it is usually run from a plain
# shell via `esu`).
TEST_UUID="e1901e1901-0000-4000-8000-000000000901"
SANDBOX_CFG=""
SESSION_EID=""

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

endless() {
    uv run endless "$@" --db sandbox
}

# Run the worktree binary against the sandbox, overriding the hook's main pin.
go_sandbox() {
    ./bin/endless-go --config-dir "${SANDBOX_CFG}" "$@"
}

# Feed one Stop payload to the hook; echo its stdout (the hook response, or
# empty when the turn is allowed to end).
stop_hook() {
    local last_msg_json="$1"
    printf '{"session_id":"%s","cwd":"%s","hook_event_name":"Stop","transcript_path":"","last_assistant_message":%s}' \
        "${TEST_UUID}" "$(pwd)" "${last_msg_json}" \
        | go_sandbox hook claude 2>/dev/null
}

# Arm the gate directly with a known sanctioned text.
arm_gate() {
    endless sql "DELETE FROM session_gates" --write >/dev/null 2>&1
    printf '%s' "$1" | go_sandbox session-query relay-checkpoint --session-id "${SESSION_EID}"
}

sql1() {
    endless sql "$1" 2>/dev/null | sed -n '3p' | tr -d ' '
}

# ─── assertions ─────────────────────────────────────────────────────────────

assert_succeeds() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

assert_contains() {
    local desc="$1" pattern="$2"; shift 2
    local output
    output=$("$@" 2>&1)
    if [[ "${output}" == *"${pattern}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output contains: ${pattern}" "${output}"
}

assert_not_contains() {
    local desc="$1" pattern="$2"; shift 2
    local output
    output=$("$@" 2>&1)
    if [[ "${output}" != *"${pattern}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output does NOT contain: ${pattern}" "${output}"
}

assert_eq() {
    local desc="$1" want="$2" got="$3"
    if [[ "${got}" == "${want}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${want}" "${got}"
}

assert_str_contains() {
    local desc="$1" pattern="$2" haystack="$3"
    if [[ "${haystack}" == *"${pattern}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "contains: ${pattern}" "${haystack}"
}

# ─── build + automated suites (fail-fast on the E-1901 unit contract) ────────

test_build_and_suites() {
    section "Build & automated suites"

    if [[ ! -f bin/endless-go ]] || [[ -n "$(find . -name '*.go' -not -path './vendor/*' -newer bin/endless-go 2>/dev/null | head -1)" ]]; then
        assert_succeeds "just build (binaries stale)" just build
    else
        report_pass "binaries up to date (skipping build)"
    fi

    # Fail-fast: the comparison matrix. Correctness of the whole feature reduces
    # to "does relayVerdict classify this message right", so it is checked before
    # anything slower runs.
    assert_succeeds "go test relay gate (comparison matrix, block shape, cap)" \
        go test ./internal/hookcmd/... \
        -run 'TestRelay|TestPlural|TestNormalize'

    assert_succeeds "go test ./internal/hookcmd/... (full package, no regression)" \
        go test ./internal/hookcmd/...
    assert_succeeds "go test ./internal/gatekind/... (enum ↔ table integrity)" \
        go test ./internal/gatekind/...

    assert_succeeds "pytest report command (verify field, render split, arming)" \
        uv run pytest tests/test_task_report.py -q
    assert_succeeds "pytest Stop-hook sync contract" \
        uv run pytest tests/test_setup_hook_sync.py -q
}

# ─── schema is current in the sandbox ───────────────────────────────────────

test_schema() {
    section "Schema (sandbox built from schema.sql)"

    local cols
    cols=$(endless sql "SELECT name FROM pragma_table_info('session_gates')" 2>&1)
    if [[ "${cols}" == *"sanctioned_text"* && "${cols}" == *"bounces"* ]]; then
        report_pass "session_gates has sanctioned_text + bounces"
    else
        report_fail "session_gates has sanctioned_text + bounces" \
            "both columns present" \
            "MISSING — this sandbox predates the change. CREATE TABLE IF NOT EXISTS cannot add columns, and sandboxes never apply change files. Recreate it: ./bin/endless-go sandbox init --force --mode worktree e-1901 && just dev-sandbox-init"
    fi

    assert_contains "gate_kinds seeds the 'relay' kind" "relay" \
        endless sql "SELECT slug FROM gate_kinds WHERE id=2"

    # The land-time migration for the populated real DB must exist and be
    # additive — the sandbox proves schema.sql, not the upgrade path.
    if [[ -f internal/schema/changes/e-1901-relay-gate.sql ]]; then
        report_pass "land-time change file present for the real DB"
    else
        report_fail "land-time change file present for the real DB" \
            "internal/schema/changes/e-1901-relay-gate.sql exists" "absent"
    fi
}

# ─── the report's new shape ─────────────────────────────────────────────────

test_report_shape() {
    section "Report output: sanctioned block vs agent-facing addendum"

    local tid
    tid=$(endless task add "Probe the E-1901 report block shape" --no-session 2>&1 \
          | grep -oE 'E-[0-9]+' | head -1)
    tid="${tid#E-}"
    if [[ -z "${tid}" ]]; then
        report_fail "seed report target" "task add succeeds" "add failed"
        return
    fi

    local out
    out=$(endless task report "${tid}" --json '{"verify":"just test"}' 2>&1)

    assert_str_contains "block is delimited by BEGIN/END markers" \
        "----- BEGIN REPORT -----" "${out}"
    assert_str_contains "verify command renders inside the block" \
        'Verify: `just test`' "${out}"
    assert_str_contains "steer names the gate as enforced, not advisory" \
        "This is enforced, not advisory" "${out}"

    # The empty case must still produce a sanctioned string — silence would give
    # the equality gate nothing to match, and freehand prose is what it removes.
    local empty_out body
    empty_out=$(endless task report "${tid}" 2>&1)
    body=$(printf '%s\n' "${empty_out}" \
           | sed -n '/----- BEGIN REPORT -----/,/----- END REPORT -----/p' \
           | sed '1d;$d')
    assert_eq "empty report renders the fixed 'Nothing to report.' line" \
        "Nothing to report." "$(printf '%s' "${body}" | tr -d '\r')"

    # A multi-line verify is a checklist wearing a field's clothes.
    assert_contains "multi-line verify rejected" "ONE command" \
        endless task report "${tid}" --json '{"verify":"just build\njust test"}'
    assert_contains "unknown payload field still rejected" "unknown report field" \
        endless task report "${tid}" --json '{"summary":"hi"}'
}

# ─── report arms the gate (real CLI -> Go -> sandbox DB) ────────────────────

test_gate_arming() {
    section "Report arms the relay checkpoint"

    local tid
    tid=$(endless task add "Probe the E-1901 checkpoint arming path" --no-session 2>&1 \
          | grep -oE 'E-[0-9]+' | head -1)
    tid="${tid#E-}"
    if [[ -z "${tid}" ]]; then
        report_fail "seed arming target" "task add succeeds" "add failed"
        return
    fi

    endless sql "DELETE FROM session_gates" --write >/dev/null 2>&1
    # ENDLESS_SESSION_ID is resolution layer 1, so the recorded checkpoint lands
    # against a known session regardless of how this script was invoked.
    ENDLESS_SESSION_ID="${SESSION_EID}" \
        endless task report "${tid}" --json '{"verify":"esu && ./tests/tasks/e-1901-verify.sh"}' >/dev/null 2>&1

    local n text
    n=$(sql1 "SELECT count(*) FROM session_gates WHERE kind_id=2 AND cleared_at IS NULL")
    assert_eq "report opened exactly one relay checkpoint" "1" "${n}"

    text=$(endless sql "SELECT sanctioned_text FROM session_gates WHERE kind_id=2" 2>/dev/null | sed -n '3p')
    assert_str_contains "checkpoint stores the verify line" \
        'Verify: `esu && ./tests/tasks/e-1901-verify.sh`' "${text}"
    # The steer frames the block; storing it would gate against a string the
    # user was never meant to receive, and every relay would bounce.
    if [[ "${text}" == *"Relay the block"* ]]; then
        report_fail "checkpoint stores the block ALONE (not the steer)" \
            "no steer text in sanctioned_text" "${text}"
    else
        report_pass "checkpoint stores the block ALONE (not the steer)"
    fi
}

# ─── the Stop gate, driven through the real hook binary ─────────────────────

test_stop_gate() {
    section "Stop gate (real hook binary -> sandbox DB)"

    local resp

    # Compliant: verbatim relay ends the turn and closes the checkpoint.
    arm_gate 'Verify: `just test`' >/dev/null
    resp=$(stop_hook '"Verify: `just test`"')
    assert_eq "verbatim relay is allowed (no block response)" "" "${resp}"
    assert_eq "compliant relay closes the checkpoint" "relay_complied" \
        "$(sql1 "SELECT cleared_by FROM session_gates WHERE kind_id=2")"

    # Silence is allowed: the offense is appending, not under-speaking.
    arm_gate 'Verify: `just test`' >/dev/null
    assert_eq "an empty final message is allowed" "" "$(stop_hook '""')"

    # The canonical violation.
    arm_gate 'Verify: `just test`' >/dev/null
    resp=$(stop_hook '"Verify: `just test`\n\nI reproduced the bug, fixed it, and added a regression test."')
    assert_str_contains "appended prose returns decision:block" '"decision":"block"' "${resp}"
    assert_str_contains "block reason quantifies the append" "appended 1 line" "${resp}"
    assert_str_contains "block reason carries the text to resend" "Verify:" "${resp}"
    assert_str_contains "block reason names the --json escape route" "--json" "${resp}"
    assert_str_contains "violation is named to the USER too (systemMessage)" \
        '"systemMessage"' "${resp}"
    assert_eq "a bounce is recorded" "1" \
        "$(sql1 "SELECT bounces FROM session_gates WHERE kind_id=2")"

    # Cosmetic formatting is not a violation — a false positive here teaches the
    # agent the gate is noise, which costs more than the prose it would catch.
    arm_gate 'Verify: `just test`' >/dev/null
    assert_eq "a code-fenced relay is NOT bounced" "" \
        "$(stop_hook '"```\nVerify: `just test`\n```"')"

    arm_gate 'Verify: `just test`' >/dev/null
    assert_eq "relaying the markers too is NOT bounced" "" \
        "$(stop_hook '"----- BEGIN REPORT -----\nVerify: `just test`\n----- END REPORT -----"')"

    # No checkpoint means no gate: an ordinary turn is never touched.
    endless sql "DELETE FROM session_gates" --write >/dev/null 2>&1
    assert_eq "a turn with no checkpoint is never gated" "" \
        "$(stop_hook '"Some perfectly ordinary reply."')"

    # The loop guard. Without a cap, a model that will not comply holds the turn
    # hostage forever.
    arm_gate 'Verify: `just test`' >/dev/null
    local bad='"Verify: `just test`\nAppended."'
    stop_hook "${bad}" >/dev/null
    stop_hook "${bad}" >/dev/null
    resp=$(stop_hook "${bad}")
    assert_not_contains "bounce budget stops blocking after the cap" '"decision":"block"' echo "${resp}"
    assert_str_contains "giving up is announced to the user, not silent" \
        "NOT the sanctioned report" "${resp}"
    assert_eq "exhausted checkpoint is closed" "relay_exhausted" \
        "$(sql1 "SELECT cleared_by FROM session_gates WHERE kind_id=2")"

    # A new user turn retires an unconsumed checkpoint (this is also what keeps
    # the FULL STATUS escape hatch working).
    arm_gate 'Verify: `just test`' >/dev/null
    printf '{"session_id":"%s","cwd":"%s","hook_event_name":"UserPromptSubmit","prompt":"FULL STATUS"}' \
        "${TEST_UUID}" "$(pwd)" | go_sandbox hook claude >/dev/null 2>&1
    assert_eq "a new user prompt clears the pending checkpoint" "relay_superseded" \
        "$(sql1 "SELECT cleared_by FROM session_gates WHERE kind_id=2")"
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

    SANDBOX_CFG="$(uv run endless db path --db sandbox 2>/dev/null | xargs dirname)"
    if [[ -z "${SANDBOX_CFG}" || ! -d "${SANDBOX_CFG}" ]]; then
        printf 'ERROR: cannot resolve the sandbox config dir; run `just dev-sandbox-init`\n' >&2
        exit 2
    fi

    printf '%sE-1901 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox (%s)\n' "${SANDBOX_CFG}"

    test_build_and_suites

    # The end-to-end sections need a session row to hang checkpoints off.
    SESSION_EID=$(./bin/endless-go --config-dir "${SANDBOX_CFG}" session-query \
        ensure-claude-id --session-id "${TEST_UUID}" --project-root "${repo_root}" 2>/dev/null)
    if [[ ! "${SESSION_EID}" =~ ^[0-9]+$ ]]; then
        printf '\nERROR: could not create a sandbox session row (got %q)\n' "${SESSION_EID}" >&2
        exit 2
    fi

    test_schema
    test_report_shape
    test_gate_arming
    test_stop_gate

    summary
}

main "$@"
