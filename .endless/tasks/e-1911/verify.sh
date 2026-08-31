#!/usr/bin/env bash
#
# E-1911 verification script — append a curated block, park the relay gate.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1911
#
# Single entry point (per E-1596). Fail-fast on the unit contract, then each of
# the task's four parts, driven end-to-end through the real CLI -> worktree
# endless-go -> sandbox DB wherever the behavior is observable there:
#
#   1. The Stop gate is parked IN CODE. A real checkpoint is armed and a real
#      violating final message is fed to the real Stop hook; the turn must be
#      allowed to end. The parking is asserted to live at the call site, NOT in
#      settings.json — `Stop` must still be a synchronous hook event.
#   2. The append contract. The separator prints in BOTH the empty and non-empty
#      case — that is the property that makes a swallowed block detectable — and
#      the retired BEGIN/END markers are gone.
#   3. No `Children:` line, from either end: the report omits it and the epic
#      handoff no longer asks the session to lead with it.
#   4. `task show --children` lists a `confirmed` child.
#
# Exit 0 on all-passed, 1 on any failure.
#
# Why the hook runs with an explicit --config-dir: `endless-go hook` calls
# PinMainDB (E-1450/E-1429) so hook-fired writes always hit the REAL DB
# regardless of cwd. HasExplicitDBContext is the documented seam for exactly
# this case — an explicit --config-dir beats the main pin, which is what lets a
# test drive the hook against the sandbox instead of the user's real ledger.
#
# Honest limit: nothing here can assert a live model actually appends the block
# rather than replacing its message with it. The gate that would have caught
# that is the thing this task parked, deliberately — what is verifiable, and is
# verified, is that the command PRINTS an appendable block with a detectable
# separator in every case, and that nothing in the harness still instructs the
# opposite.
#
# Model: .endless/tasks/e-1901/verify.sh.

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

SEPARATOR="----- ENDLESS REPORT -----"

# A fixed synthetic session UUID so the end-to-end path does not depend on this
# script being run from inside a Claude session (it is usually run from a plain
# shell via `esu`).
TEST_UUID="e1911e1911-0000-4000-8000-000000000911"
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

note() {
    printf '  %s•%s %s\n' "${DIM}" "${RESET}" "$1"
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

# The worktree-built binary, pointed at the sandbox config dir.
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

# Arm the (parked) gate directly with a known sanctioned text.
arm_gate() {
    endless sql "DELETE FROM session_gates" --write >/dev/null 2>&1
    printf '%s' "$1" | go_sandbox session-query relay-checkpoint --session-id "${SESSION_EID}"
}

sql1() {
    endless sql "$1" 2>/dev/null | sed -n '3p' | tr -d ' '
}

# Create a task and emit just its numeric id on stdout.
add_task_get_id() {
    local title="$1"; shift
    local output rc eid
    output=$(endless task add "${title}" --no-session "$@" 2>&1)
    rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        printf 'ERROR: add failed for %q: %s\n' "${title}" "${output}" >&2
        return 1
    fi
    eid=$(printf '%s\n' "${output}" | grep -oE 'E-[0-9]+' | head -1)
    printf '%s\n' "${eid#E-}"
}

# Everything after the separator: the appended block. There is no closing
# marker, by design — the block runs to the end of the message.
block_after_separator() {
    printf '%s\n' "$1" | sed -n "/${SEPARATOR}/,\$p" | sed '1d'
}

# ─── assertions ─────────────────────────────────────────────────────────────

assert_succeeds() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
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

assert_str_not_contains() {
    local desc="$1" pattern="$2" haystack="$3"
    if [[ "${haystack}" != *"${pattern}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "does NOT contain: ${pattern}" "${haystack}"
}

assert_file_contains() {
    local desc="$1" pattern="$2" file="$3"
    if [[ -f "${file}" ]] && grep -qF -- "${pattern}" "${file}"; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "${file} contains: ${pattern}" "absent"
}

# ─── build + automated suites (fail-fast on the unit contract) ──────────────

test_build_and_suites() {
    section "Build & automated suites"

    if [[ ! -f bin/endless-go ]] || [[ -n "$(find . -name '*.go' -not -path './vendor/*' -newer bin/endless-go 2>/dev/null | head -1)" ]]; then
        assert_succeeds "just build (binaries stale)" just build
    else
        report_pass "binaries up to date (skipping build)"
    fi

    # Fail-fast: the four parts reduce to these units. Everything slower below
    # is the end-to-end proof that the units are wired to the real surfaces.
    assert_succeeds "go test: gate parked, nudge inverted, separator in sync" \
        go test ./internal/hookcmd/... -count=1 \
        -run 'TestRelayGateIsParked|TestReportRelayResponse_Shape|TestReportSeparatorMatchesPython'
    assert_succeeds "go test: children off the report-facts wire" \
        go test ./internal/monitor/... -count=1 -run 'TestTaskReportFacts'
    assert_succeeds "go test: epic handoff drops the children directive" \
        go test ./internal/templatecmd/... -count=1 -run 'TestRender_HandoffClose_ExceptionRule'

    # Full packages — no regression in the surrounding hook/template logic.
    assert_succeeds "go test ./internal/hookcmd/... (full package)" \
        go test ./internal/hookcmd/... -count=1
    assert_succeeds "go test ./internal/templatecmd/... (full package)" \
        go test ./internal/templatecmd/... -count=1

    assert_succeeds "pytest report command (append contract, no Children line)" \
        uv run pytest tests/test_task_report.py -q
    assert_succeeds "pytest task show --children (confirmed children visible)" \
        uv run pytest tests/test_show_children.py -q
    assert_succeeds "pytest Stop-hook sync contract (Stop stays synchronous)" \
        uv run pytest tests/test_setup_hook_sync.py -q
}

# ─── Part 1: the gate is parked, in code ────────────────────────────────────

test_gate_parked() {
    section "Part 1 — the relay gate is parked at its call site"

    # The switch is a code-level constant, not a config edit.
    assert_file_contains "kill switch is a package constant" \
        "const relayGateEnabled = false" internal/hookcmd/relay_gate.go
    assert_file_contains "the gate short-circuits on it" \
        "if !relayGateEnabled {" internal/hookcmd/relay_gate.go

    # NOT parked via settings.json: Stop stays installed and synchronous, so a
    # revival is one line and not a re-derivation of E-1901's hook contract.
    assert_file_contains "Stop remains a synchronous hook event" \
        '"Stop"' src/endless/setup.py

    # The machinery survives — reviving is a flip, not a rewrite.
    for f in internal/hookcmd/relay_gate.go internal/hookcmd/relay_gate_test.go; do
        if [[ -f "${f}" ]]; then
            report_pass "kept for the revival: ${f}"
        else
            report_fail "kept for the revival: ${f}" "file exists" "deleted"
        fi
    done

    # End-to-end: arm a REAL checkpoint, then feed the REAL Stop hook a message
    # that appends prose beyond it. Live, this bounced with decision:"block".
    # Parked, the turn must be allowed to end.
    arm_gate 'Verify: `just test`' >/dev/null
    local resp
    resp=$(stop_hook '"Verify: `just test`\n\nI reproduced the bug, fixed it, and added a regression test."')
    assert_eq "an appended final message is NOT blocked" "" "${resp}"

    # And the checkpoint is left pending rather than cleared: the park returns
    # before any verdict, so a revival reads the state a live gate would have.
    assert_eq "the pending checkpoint is left untouched" "" \
        "$(sql1 "SELECT COALESCE(cleared_by,'') FROM session_gates WHERE kind_id=2")"
}

# ─── Part 1b: the harness no longer instructs the opposite ──────────────────

test_nudge_inverted() {
    section "Part 1b — the compose-time nudge states the append contract"

    assert_file_contains "PostToolUse nudge names the separator" \
        "${SEPARATOR}" internal/hookcmd/claude.go
    assert_str_not_contains "nudge no longer demands the whole reply" \
        "entire reply to the user" "$(cat internal/hookcmd/claude.go)"
    assert_str_not_contains "nudge no longer claims enforcement" \
        "This is now ENFORCED" "$(cat internal/hookcmd/claude.go)"
}

# ─── Part 2: the append contract, through the real CLI ──────────────────────

test_append_contract() {
    section "Part 2 — separator + block (real CLI -> Go -> sandbox)"

    local tid
    if ! tid=$(add_task_get_id "Probe the E-1911 append contract"); then
        report_fail "seed report target" "task add succeeds" "add failed"
        return
    fi

    # Non-empty case: the block carries the computed fact, after the separator.
    local out
    out=$(endless task report "${tid}" --json '{"verify":"just test"}' 2>&1)
    assert_str_contains "report prints the separator" "${SEPARATOR}" "${out}"
    assert_eq "verify command renders as the block" 'Verify: `just test`' \
        "$(block_after_separator "${out}" | tr -d '\r')"
    assert_str_contains "steer leaves the agent's own answer unconstrained" \
        "NOT constrained" "${out}"

    # The retired BEGIN/END pair is gone from both cases.
    assert_str_not_contains "no retired BEGIN marker" "BEGIN REPORT" "${out}"
    assert_str_not_contains "no retired END marker" "END REPORT" "${out}"

    # Empty case: the separator STILL prints, followed by the fixed null line.
    # This is the property that makes a swallowed block detectable — an absent
    # block cannot be told apart from a report that found nothing.
    local empty_out
    empty_out=$(endless task report "${tid}" 2>&1)
    assert_str_contains "empty report still prints the separator" \
        "${SEPARATOR}" "${empty_out}"
    assert_eq "empty report's block is exactly the null line" "Nothing to report." \
        "$(block_after_separator "${empty_out}" | tr -d '\r')"

    # Exactly one separator per report, or "everything after it" is ambiguous.
    assert_eq "exactly one separator in the empty case" "1" \
        "$(printf '%s\n' "${empty_out}" | grep -cF -- "${SEPARATOR}")"
    assert_eq "exactly one separator in the non-empty case" "1" \
        "$(printf '%s\n' "${out}" | grep -cF -- "${SEPARATOR}")"
}

# ─── Part 3: no children, from either end ───────────────────────────────────

test_no_children_line() {
    section "Part 3 — report and handoff both stop duplicating session status"

    # An epic with a confirmed child, plus an unrelated task elsewhere in the
    # tree: the exact shape that listed two non-children under E-1785.
    local epic child unrelated
    if ! epic=$(add_task_get_id "Probe the E-1911 epic report shape" --type epic); then
        report_fail "seed probe epic" "task add succeeds" "add failed"
        return
    fi
    child=$(add_task_get_id "Probe an E-1911 confirmed child" --parent "${epic}")
    unrelated=$(add_task_get_id "Probe an E-1911 task elsewhere in the tree")
    endless task update "${child}" --status confirmed >/dev/null 2>&1

    local out
    out=$(endless task report "${epic}" 2>&1)
    assert_str_not_contains "epic report has no Children line" "Children:" "${out}"
    assert_str_not_contains "epic report does not name its child" \
        "E-${child}" "${out}"
    assert_str_not_contains "epic report does not name an unrelated task" \
        "E-${unrelated}" "${out}"
    assert_eq "epic report's block is the null line" "Nothing to report." \
        "$(block_after_separator "${out}" | tr -d '\r')"

    # The other half: the epic handoff no longer asks for the children lead,
    # which is what made the report compute the list in the first place.
    local rendered
    rendered=$(printf '%s' '{"spawned_id":1,"label_prefix":"E-1","title":"T","worktree_path":"/w","branch":"b","child_count":0,"children_state":"none","bg":false,"task_type":"epic"}' \
        | ./bin/endless-go template render handoff/epic 2>&1)
    assert_str_not_contains "epic handoff drops the children directive" \
        "lead with the state of the children" "${rendered}"
    assert_str_contains "epic handoff still routes reporting through the command" \
        "endless task report" "${rendered}"
}

# ─── Part 4: confirmed children are not hidden ──────────────────────────────

test_confirmed_children_visible() {
    section "Part 4 — task show --children lists confirmed children"

    local epic done_child open_child
    if ! epic=$(add_task_get_id "Probe E-1911 children visibility" --type epic); then
        report_fail "seed visibility epic" "task add succeeds" "add failed"
        return
    fi
    done_child=$(add_task_get_id "Probe a verified and landed child" --parent "${epic}")
    open_child=$(add_task_get_id "Probe a still-open child" --parent "${epic}")
    endless task update "${done_child}" --status confirmed >/dev/null 2>&1

    local out
    out=$(endless task show "${epic}" --children 2>&1)
    assert_str_contains "confirmed child is listed" "E-${done_child}" "${out}"
    assert_str_contains "open child is still listed" "E-${open_child}" "${out}"

    out=$(endless task show "${epic}" --children --json 2>&1)
    assert_str_contains "confirmed child is in the JSON children array" \
        "\"E-${done_child}\"" "${out}"

    # The regression this was filed for, read from the REAL ledger: E-1906 is a
    # confirmed child of E-1785 and vanished from its children list.
    local real
    real=$(uv run endless task show 1785 --children --db main 2>&1)
    if [[ "${real}" == *"E-1785"* ]]; then
        assert_str_contains "regression: E-1785 --children lists E-1906" \
            "E-1906" "${real}"
    else
        note "skipped the E-1785 regression check — no such task in the main ledger"
    fi
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

    printf '%sE-1911 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox (%s)\n' "${SANDBOX_CFG}"

    test_build_and_suites

    # The Stop-hook section needs a session row to hang a checkpoint off.
    SESSION_EID=$(./bin/endless-go --config-dir "${SANDBOX_CFG}" session-query \
        ensure-claude-id --session-id "${TEST_UUID}" --project-root "${repo_root}" 2>/dev/null)
    if [[ ! "${SESSION_EID}" =~ ^[0-9]+$ ]]; then
        printf '\nERROR: could not create a sandbox session row (got %q)\n' "${SESSION_EID}" >&2
        exit 2
    fi

    test_gate_parked
    test_nudge_inverted
    test_append_contract
    test_no_children_line
    test_confirmed_children_visible

    summary
}

main "$@"
