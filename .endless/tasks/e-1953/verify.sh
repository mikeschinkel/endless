#!/usr/bin/env bash
#
# E-1953 verification — `task report` rebuilt as an enforced minimizer.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1953
#
# Single entry point (per E-1596). Fail-fast on the unit contracts, then each
# part driven end-to-end through the real CLI -> worktree endless-go -> sandbox
# DB wherever the behavior is observable there.
#
#   0. Build + the automated suites the parts reduce to.
#   1. Increment 1 — the payload-and-render surface is GONE. `--json` is not a
#      flag, the separator is not in shipped code, and neither half of the old
#      report channel still instructs the append contract.
#   2. Increment 2 — the minimizer, run LIVE against tests/fixtures/report-draft.md.
#      Six properties: table byte-for-byte, code block byte-for-byte, verify
#      command survives, direct answer survives, narrating paragraphs gone,
#      output shorter than input. This is the only part that calls a model, and
#      it is the only part that can say anything about judgment.
#   3. The raw draft round-trips byte-for-byte, so nothing the minimizer cut is
#      lost. Driven through the real persistence path, not a stub.
#   4. Enforcement at Stop: verbatim allowed, +1 appended sentence blocked,
#      never-called blocked, budget surrender audible, subagent exempt,
#      `$FULL` exempt.
#   5. The signal vocabulary through the REAL UserPromptSubmit hook: `$CUT`
#      records against the preceding turn, a bare complaint is refused, `$GOOD`
#      stands alone, and `CUT the scope` / `WRONG: ...` stay inert.
#   6. The switch is configuration, defaults on, and resolves nearest-first.
#
# Exit 0 on all-passed, 1 on any failure.
#
# Why the hook runs with an explicit --config-dir: `endless-go hook` calls
# PinMainDB (E-1450/E-1429) so hook-fired writes always hit the REAL DB
# regardless of cwd. HasExplicitDBContext is the documented seam for exactly
# this case — an explicit --config-dir beats the main pin, which is what lets a
# test drive the hook against the sandbox instead of the user's real ledger.
#
# Why parts 4 and 5 run from a synthetic cwd: this repo ships
# `"report_gate": false` in its own .endless/config.json (it is where the
# minimizer prompt is tuned), so a gate test run from the worktree root would
# fail open and prove nothing. The script creates a throwaway directory under
# the gitignored .endless/tmp/ carrying `"report_gate": true` and hands it to
# the hook as cwd. That turns the gate on by exercising the REAL nearest-config
# resolution rather than by editing the repo's config and hoping the restore
# runs.
#
# Model: .endless/tasks/e-1911/verify.sh.

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
TEST_UUID="e1953e1953-0000-4000-8000-000000000953"
SANDBOX_CFG=""
SESSION_EID=""
GATE_DIR=""
FIXTURE="tests/fixtures/report-draft.md"
MINIMIZED=""

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

endless() {
    uv run endless "$@" --db sandbox
}

go_sandbox() {
    ./bin/endless-go --config-dir "${SANDBOX_CFG}" "$@"
}

# Feed one Stop payload to the hook from the gate-ON cwd; echo its stdout (the
# hook response, or empty when the turn is allowed to end).
stop_hook() {
    local last_msg_json="$1" agent_id="${2:-}"
    printf '{"session_id":"%s","cwd":"%s","hook_event_name":"Stop","transcript_path":"","agent_id":"%s","last_assistant_message":%s}' \
        "${TEST_UUID}" "${GATE_DIR}" "${agent_id}" "${last_msg_json}" \
        | go_sandbox hook claude 2>/dev/null
}

# Feed one UserPromptSubmit payload; echo the hook's stdout.
prompt_hook() {
    local prompt_json="$1"
    printf '{"session_id":"%s","cwd":"%s","hook_event_name":"UserPromptSubmit","transcript_path":"","prompt":%s}' \
        "${TEST_UUID}" "${GATE_DIR}" "${prompt_json}" \
        | go_sandbox hook claude 2>/dev/null
}

# Feed one PostToolUse Bash payload; echo the hook's stdout. cwd decides whether
# the project has the gate on.
posttooluse_hook() {
    local cwd="$1" cmd="$2"
    printf '{"session_id":"%s","cwd":"%s","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"%s"}}' \
        "${TEST_UUID}" "${cwd}" "${cmd}" \
        | go_sandbox hook claude 2>/dev/null
}

# Arm the gate with a known minimized text (and optionally a raw draft).
arm_gate() {
    local minimized="$1" draft_file="${2:-}"
    endless sql "DELETE FROM session_gates" --write >/dev/null 2>&1
    if [[ -n "${draft_file}" ]]; then
        printf '%s' "${minimized}" | go_sandbox session-query relay-checkpoint \
            --session-id "${SESSION_EID}" --draft-file "${draft_file}"
    else
        printf '%s' "${minimized}" | go_sandbox session-query relay-checkpoint \
            --session-id "${SESSION_EID}"
    fi
}

# Clear every trace of a turn: no checkpoint, no spent bounce budget.
reset_turn() {
    endless sql "DELETE FROM session_gates" --write >/dev/null 2>&1
    endless sql "UPDATE sessions SET report_bounces=0, report_exempt=0, report_runs=0" \
        --write >/dev/null 2>&1
}

sql1() {
    endless sql "$1" 2>/dev/null | sed -n '3p' | sed 's/^ *//; s/ *$//'
}

# ─── assertions ─────────────────────────────────────────────────────────────

assert_succeeds() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

assert_fails() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit != 0" "exit=0 | output=${output}"
}

assert_eq() {
    local desc="$1" want="$2" got="$3"
    if [[ "${got}" == "${want}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${want}" "${got}"
}

assert_str_contains() {
    local desc="$1" pattern="$2" haystack="$3"
    if [[ "${haystack}" == *"${pattern}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "contains: ${pattern}" "${haystack:0:400}"
}

assert_str_not_contains() {
    local desc="$1" pattern="$2" haystack="$3"
    if [[ "${haystack}" != *"${pattern}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "does NOT contain: ${pattern}" "${haystack:0:400}"
}

assert_file_contains() {
    local desc="$1" pattern="$2" file="$3"
    if [[ -f "${file}" ]] && grep -qF -- "${pattern}" "${file}"; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "${file} contains: ${pattern}" "absent"
}

assert_file_not_contains() {
    local desc="$1" pattern="$2" file="$3"
    if [[ ! -f "${file}" ]] || ! grep -qF -- "${pattern}" "${file}"; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "${file} does NOT contain: ${pattern}" "still present"
}

# ─── Part 0: build + automated suites ───────────────────────────────────────

test_build_and_suites() {
    section "Build & automated suites"

    if [[ ! -f bin/endless-go ]] || [[ -n "$(find . -name '*.go' -not -path './vendor/*' -newer bin/endless-go 2>/dev/null | head -1)" ]]; then
        assert_succeeds "just build (binaries stale)" just build
    else
        report_pass "binaries up to date (skipping build)"
    fi

    # Fail-fast: everything slower below is the end-to-end proof that these
    # units are wired to the real surfaces.
    assert_succeeds "go test: gate live, bypass bounce, audible surrender" \
        go test ./internal/hookcmd/... -count=1 \
        -run 'TestReportGateIsLive|TestReportMissingReason_AsksForTheWholeDraft|TestReportExhaustedMessage_IsNotSilent|TestReportGate_Skips'
    assert_succeeds "go test: the sigil vocabulary" \
        go test ./internal/hookcmd/... -count=1 -run 'TestScanSigils|TestSigilRejectionNotice'
    assert_succeeds "go test: the switch defaults on and resolves nearest-first" \
        go test ./internal/monitor/... -count=1 -run 'TestReportGate'
    assert_succeeds "go test: handoffs teach the draft contract, and drop it when the gate is off" \
        go test ./internal/templatecmd/... -count=1 \
        -run 'TestRender_HandoffClose_ExceptionRule|TestRender_HandoffClose_OmitsReportingWhenGateOff'

    assert_succeeds "go test ./internal/hookcmd/... (full package)" \
        go test ./internal/hookcmd/... -count=1
    assert_succeeds "go test ./internal/monitor/... (full package)" \
        go test ./internal/monitor/... -count=1
    assert_succeeds "go test ./internal/templatecmd/... (full package)" \
        go test ./internal/templatecmd/... -count=1

    assert_succeeds "pytest the minimizer command (plumbing, fail-closed, appeal)" \
        uv run pytest tests/test_task_report.py -q
}

# ─── Part 1: the payload-and-render surface is gone ─────────────────────────

test_retired_surface() {
    section "Part 1 — the payload-and-render surface is gone (increment 1)"

    local help
    help=$(endless task report --help 2>&1)
    assert_str_contains "--draft-file is the input" "--draft-file" "${help}"
    assert_str_contains "--raw is offered" "--raw" "${help}"
    assert_str_not_contains "no --json payload flag" "--json" "${help}"

    # A payload flag must be REJECTED, not silently ignored: an agent that still
    # passes one has to learn the surface changed.
    assert_fails "--json is rejected outright" \
        env -u ENDLESS_SESSION_ID uv run endless task report 1 --json '{"verify":"x"}' --db sandbox

    # The bare form no longer renders anything — there is nothing to report
    # without a draft, and inventing a block is the failure mode.
    local bare
    bare=$(endless task report 2>&1)
    assert_str_contains "a draftless run says what to pass" "--draft-file is required" "${bare}"

    # The separator was the append contract's marker. It must be gone from
    # shipped code, or an agent will keep looking for a block that never comes.
    assert_file_not_contains "no separator in the Python command" \
        "ENDLESS REPORT" src/endless/report_prompts.py
    assert_file_not_contains "no separator in the hook" \
        "ENDLESS REPORT" internal/hookcmd/claude.go

    # The defect the analysis named: the PostToolUse branch keyed on the command
    # NAME, so `--help` fired the reinforcement. It now keys on a checkpoint
    # having been written, which only a successful render does.
    assert_file_contains "PostToolUse keys on a successful render" \
        "reportRendered(payload.SessionID)" internal/hookcmd/claude.go
}

# ─── Part 2: the minimizer, live ────────────────────────────────────────────

test_minimizer_live() {
    section "Part 2 — the minimizer, run live over the fixture"

    if ! command -v claude >/dev/null 2>&1; then
        report_fail "claude is on PATH" "a `claude` binary to run the minimizer" \
            "not found — the only part that can test judgment cannot run"
        return
    fi

    local out
    out=$(env -u ENDLESS_SESSION_ID uv run endless task report \
        --draft-file "${FIXTURE}" --db sandbox 2>&1)
    if [[ -z "${out}" ]]; then
        report_fail "the minimizer produced output" "non-empty stdout" "empty"
        return
    fi
    MINIMIZED="${out}"
    report_pass "the minimizer produced output"

    # 1 + 2. Table and code block survive BYTE FOR BYTE. Both are asserted line
    # by line rather than as a blob: a reflowed table that kept every cell would
    # pass a substring check on any single row.
    local table_ok=1 row
    while IFS= read -r row; do
        [[ -z "${row}" ]] && continue
        if [[ "${out}" != *"${row}"* ]]; then table_ok=0; break; fi
    done < <(grep -E '^\|' "${FIXTURE}")
    if [[ "${table_ok}" -eq 1 ]]; then
        report_pass "the markdown table survives byte for byte"
    else
        report_fail "the markdown table survives byte for byte" \
            "every table row present verbatim" "row altered or dropped: ${row}"
    fi

    local code_ok=1 line
    while IFS= read -r line; do
        [[ -z "${line}" ]] && continue
        if [[ "${out}" != *"${line}"* ]]; then code_ok=0; break; fi
    done < <(sed -n '/^```go$/,/^```$/p' "${FIXTURE}")
    if [[ "${code_ok}" -eq 1 ]]; then
        report_pass "the fenced code block survives byte for byte"
    else
        report_fail "the fenced code block survives byte for byte" \
            "every code line present verbatim" "line altered or dropped: ${line}"
    fi

    # 3. The verify command survives — the most expensive thing to lose, because
    # the user cannot reconstruct it.
    assert_str_contains "the verify command survives" \
        'go test ./internal/parser/ -run TestNestedQuotes' "${out}"

    # 4. The direct answer to the direct question survives.
    local answered=0
    [[ "${out}" == *"yes"* || "${out}" == *"Yes"* ]] && answered=1
    if [[ "${answered}" -eq 1 && "${out}" == *"stack"* ]]; then
        report_pass "the direct answer survives"
    else
        report_fail "the direct answer survives" \
            "the yes/stack answer to the question asked" "${out:0:300}"
    fi

    # 5. The narration goes. These are the phrases the generative rule targets
    # and the denylist anchors name — a draft that keeps them has not been
    # minimized, it has been trimmed.
    local phrase
    for phrase in "reframes the decision" "load-bearing" \
                  "the thing that survives from your instinct" \
                  "It is worth noting" "this is where it gets interesting" \
                  "no stray files" "happy to keep going"; do
        assert_str_not_contains "narration deleted: \"${phrase}\"" "${phrase}" "${out}"
    done

    # 6. Shorter than the input. Necessary, nowhere near sufficient — which is
    # why it is last and why the five properties above carry the weight.
    local in_len out_len
    in_len=$(wc -c < "${FIXTURE}" | tr -d ' ')
    out_len=${#out}
    if [[ "${out_len}" -lt "${in_len}" ]]; then
        report_pass "output is shorter than input (${out_len} < ${in_len} bytes)"
    else
        report_fail "output is shorter than input" "< ${in_len} bytes" "${out_len} bytes"
    fi
}

# ─── Part 3: the raw draft round-trips ──────────────────────────────────────

test_raw_round_trip() {
    section "Part 3 — the raw draft round-trips, so nothing cut is lost"

    arm_gate "minimized text" "${FIXTURE}" >/dev/null

    local got want
    got=$(go_sandbox session-query report-draft --session-id "${SESSION_EID}")
    want=$(cat "${FIXTURE}")
    assert_eq "the persisted draft round-trips byte for byte" "${want}" "${got}"

    # The corpus triple, on the row the gate already needed. The prompting
    # message is staged by the hook, so it is asserted after part 5 runs one.
    assert_eq "the minimized output is stored alongside it" "minimized text" \
        "$(sql1 "SELECT sanctioned_text FROM session_gates WHERE kind_id=2 ORDER BY id DESC LIMIT 1")"

    # No draft at all is distinguishable from an empty one — `--raw` has to be
    # able to say "you never reported" rather than printing nothing.
    endless sql "DELETE FROM session_gates" --write >/dev/null 2>&1
    assert_fails "no persisted draft exits non-zero" \
        go_sandbox session-query report-draft --session-id "${SESSION_EID}"
}

# ─── Part 4: enforcement at Stop ────────────────────────────────────────────

test_enforcement() {
    section "Part 4 — the Stop gate"

    local sanctioned='Verify: `just test`'

    # (a) Verbatim → allowed.
    reset_turn
    arm_gate "${sanctioned}" >/dev/null
    assert_eq "a verbatim final message is allowed" "" \
        "$(stop_hook '"Verify: `just test`"')"
    assert_eq "and the checkpoint is closed as complied" "relay_complied" \
        "$(sql1 "SELECT cleared_by FROM session_gates WHERE kind_id=2 ORDER BY id DESC LIMIT 1")"

    # (b) Verbatim + one appended sentence → blocked. This is the whole habit
    # the gate exists to catch: appending does not feel like defiance, it feels
    # like thoroughness.
    reset_turn
    arm_gate "${sanctioned}" >/dev/null
    local resp
    resp=$(stop_hook '"Verify: `just test`\n\nI also refactored three unrelated files."')
    assert_str_contains "one appended sentence is blocked" '"decision":"block"' "${resp}"
    assert_str_contains "the bounce quantifies what was added" "1 line" "${resp}"
    assert_str_contains "the bounce names the appeal channel" "--draft-file" "${resp}"
    assert_str_contains "the user is told too, not just the agent" "systemMessage" "${resp}"

    # (c) Never called it → blocked. The trivial bypass: an enforcement you only
    # meet by opting in enforces nothing. Under E-1901 this was the FAIL-OPEN
    # case, so it is the single most important assertion in this file.
    reset_turn
    resp=$(stop_hook '"Here is a long answer that never went through the minimizer."')
    assert_str_contains "a turn that never reported is blocked" '"decision":"block"' "${resp}"
    assert_str_contains "and is told to submit its whole draft" "no summarizing" "${resp}"
    assert_str_contains "and that the task id is optional" "optional" "${resp}"

    # (d) The budget is finite and surrender is audible. A silent livelock and a
    # silent surrender are indistinguishable from outside.
    resp=$(stop_hook '"Still not reporting."')
    assert_str_contains "second bounce still blocks" '"decision":"block"' "${resp}"
    resp=$(stop_hook '"Still not reporting."')
    assert_str_not_contains "third time the turn is released" '"decision":"block"' "${resp}"
    assert_str_contains "and the release is announced to the user" \
        "did NOT go through the minimizer" "${resp}"

    # (e) A tool-only turn has nothing to minimize.
    reset_turn
    assert_eq "an empty final message is never blocked" "" "$(stop_hook '""')"

    # (f) A subagent's final message is a return value, not a handoff.
    reset_turn
    assert_eq "an Agent-tool subagent is exempt" "" \
        "$(stop_hook '"A subagent return value."' 'agent-abc')"

    # (g) `$FULL` licenses one turn to bypass the minimizer entirely. Routing
    # the licensed reply through it would contradict the license.
    reset_turn
    prompt_hook '"$FULL why did the rebase conflict?"' >/dev/null
    assert_eq "a \$FULL turn is exempt" "" \
        "$(stop_hook '"A long unconstrained answer about the rebase."')"
    # ...and the license is spent, not sticky.
    resp=$(stop_hook '"Another long answer, no report."')
    assert_str_contains "the \$FULL license covers one turn only" '"decision":"block"' "${resp}"
}

# ─── Part 5: the signal vocabulary ──────────────────────────────────────────

label_of() {
    sql1 "SELECT COALESCE(label,'') FROM session_gates WHERE kind_id=2 ORDER BY id DESC LIMIT 1"
}

test_signals() {
    section "Part 5 — \$CUT / \$BLOAT / \$WRONG / \$GOOD"

    # A corpus row to label, and a prompt to stage against it.
    reset_turn
    arm_gate "minimized text" "${FIXTURE}" >/dev/null

    prompt_hook '"$CUT you dropped the verify command"' >/dev/null
    assert_eq "\$CUT labels the preceding turn" "cut" "$(label_of)"
    assert_eq "and stores what was wrong with it" "you dropped the verify command" \
        "$(sql1 "SELECT label_text FROM session_gates WHERE kind_id=2 ORDER BY id DESC LIMIT 1")"

    # The prompting message is the third leg of the corpus triple, staged by the
    # hook so the command never has to ask the agent what the user said.
    assert_str_contains "the prompt is staged for the next report" "you dropped the verify command" \
        "$(go_sandbox session-query report-prompt --session-id "${SESSION_EID}")"

    # `$GOOD` may stand alone — a corpus of only complaints trains toward
    # verbosity, so approval has to be as cheap to give as disapproval.
    arm_gate "minimized text" "${FIXTURE}" >/dev/null
    prompt_hook '"$GOOD"' >/dev/null
    assert_eq "\$GOOD alone is accepted" "good" "$(label_of)"

    # A bare complaint gives the corpus nothing to learn from: refused out loud,
    # and nothing recorded. Silence here would be worse — the user would believe
    # the corpus was learning from them.
    arm_gate "minimized text" "${FIXTURE}" >/dev/null
    local resp
    resp=$(prompt_hook '"$CUT"')
    assert_str_contains "a bare \$CUT is refused out loud" "was not recorded" "${resp}"
    assert_eq "and nothing is written to the corpus" "" "$(label_of)"

    # The sigil is what buys immunity. These are what a user naturally types.
    arm_gate "minimized text" "${FIXTURE}" >/dev/null
    prompt_hook '"CUT the scope down to the parser"' >/dev/null
    assert_eq "\`CUT the scope\` does not fire" "" "$(label_of)"

    arm_gate "minimized text" "${FIXTURE}" >/dev/null
    prompt_hook '"WRONG: I meant the other file"' >/dev/null
    assert_eq "\`WRONG: ...\` does not fire" "" "$(label_of)"
}

# ─── Part 5b: the reinforcement respects the switch ─────────────────────────

test_reinforcement_respects_switch() {
    section "Part 5b — the reinforcement never claims a gate that is off"

    # Regression for a defect E-1953 itself shipped: the PostToolUse
    # reinforcement fired regardless of `report_gate`, so a project with the
    # gate OFF was still told "a Stop hook compares your final message against
    # it". That is a claim about enforcement, not a request — and it was false
    # wherever the switch was off, Endless's own repo included.
    reset_turn
    arm_gate "minimized text" "${FIXTURE}" >/dev/null

    # Gate ON: a real render, so the reinforcement is both true and expected.
    local on
    on=$(posttooluse_hook "${GATE_DIR}" "endless task report --draft-file /tmp/d.md")
    assert_str_contains "gate ON: the reinforcement fires" "Stop hook" "${on}"

    # Gate OFF (the repo root, which ships report_gate false): silence. Same
    # session, same armed checkpoint, same command — only cwd differs, so this
    # isolates the switch as the cause.
    local off
    off=$(posttooluse_hook "$(pwd)" "endless task report --draft-file /tmp/d.md")
    assert_str_not_contains "gate OFF: no claim that a Stop hook is watching" \
        "Stop hook" "${off}"
    assert_eq "gate OFF: nothing is injected at all" "" "${off}"

    # The spawn handoff is the third surface that could impose the channel. A
    # gate-off project must not be handed the instructions at all — an
    # instruction nothing enforces and nothing reads is pure per-turn overhead.
    local off_handoff
    off_handoff=$(printf '{"spawned_id":1,"label_prefix":"E-1","title":"T","worktree_path":"/w","branch":"b","child_count":0,"children_state":"none","report_gate":false,"bg":false}' \
        | go_sandbox template render handoff/todo 2>/dev/null)
    assert_str_not_contains "gate OFF: the spawn handoff omits the channel" \
        "--draft-file" "${off_handoff}"
    assert_str_contains "gate OFF: but keeps the worktree check" \
        "endless worktree check" "${off_handoff}"

    # And the render-keyed half still holds where the gate IS on: `--help`
    # renders nothing, so nothing is reinforced.
    reset_turn
    local helped
    helped=$(posttooluse_hook "${GATE_DIR}" "endless task report --help")
    assert_eq "gate ON but nothing rendered: still silent" "" "${helped}"
}

# ─── Part 6: the switch ─────────────────────────────────────────────────────

test_switch() {
    section "Part 6 — the switch is configuration, and defaults on"

    # It is NOT a code constant any more, and it is NOT in .claude/settings.json:
    # a gate an agent edits in the course of normal work is not a gate.
    assert_file_not_contains "no code-level kill switch survives" \
        "relayGateEnabled" internal/hookcmd/relay_gate.go
    assert_file_contains "the switch is read from .endless/config.json" \
        'json:"report_gate"' internal/monitor/db.go
    assert_file_not_contains "and not from .claude/settings.json" \
        "report_gate" src/endless/setup.py

    # Endless's own checkout opts out — this is where the prompt is tuned, and a
    # session tuning the prompt cannot be governed by the prompt it is editing.
    assert_file_contains "this repo ships the gate OFF" \
        '"report_gate": false' .endless/config.json

    # The gate-ON fixture parts 4 and 5 ran against proves the default and the
    # nearest-first resolution in one shot: it declares true BELOW a repo that
    # declares false, and the gate fired.
    assert_file_contains "the verify fixture declares it ON" \
        '"report_gate": true' "${GATE_DIR}/.endless/config.json"

    # Stop stays a synchronous hook event, or none of part 4 can happen at all.
    assert_file_contains "Stop remains a synchronous hook event" \
        '"Stop"' src/endless/setup.py
}

# ─── main ───────────────────────────────────────────────────────────────────

cleanup() {
    [[ -n "${GATE_DIR}" && -d "${GATE_DIR}" ]] && rm -rf "${GATE_DIR}"
}

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

    # The gate-ON cwd. Under .endless/tmp/ because that path is gitignored — the
    # sanctioned home for throwaway agent-authored content — and inside the repo
    # so project resolution still finds this project.
    GATE_DIR="${repo_root}/.endless/tmp/e-1953-gate-on"
    trap cleanup EXIT
    mkdir -p "${GATE_DIR}/.endless"
    printf '{"report_gate": true}\n' > "${GATE_DIR}/.endless/config.json"

    printf '%sE-1953 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox (%s)\n' "${SANDBOX_CFG}"
    printf '  gate on: %s\n' "${GATE_DIR}"

    test_build_and_suites

    SESSION_EID=$(go_sandbox session-query ensure-claude-id \
        --session-id "${TEST_UUID}" --project-root "${repo_root}" 2>/dev/null)
    if [[ ! "${SESSION_EID}" =~ ^[0-9]+$ ]]; then
        printf '\nERROR: could not create a sandbox session row (got %q)\n' "${SESSION_EID}" >&2
        exit 2
    fi

    test_retired_surface
    test_minimizer_live
    test_raw_round_trip
    test_enforcement
    test_signals
    test_reinforcement_respects_switch
    test_switch

    summary
}

main "$@"
