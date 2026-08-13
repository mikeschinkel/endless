#!/usr/bin/env bash
#
# E-1962 verification — Endless runs against SUPPORTED agent harnesses only.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1962-verify.sh
#
# Single entry point (per E-1596). Fail-fast on the unit contracts, then drive
# the REAL hook binary once per harness and watch what it does.
#
#   0. Build + the automated suites: internal/agentenv (the detector table and
#      the allow-list), internal/hookcmd (call-site placement), and the Python
#      mirror plus the CLI refusal.
#   1. Harness discrimination through the real hook, on all three consumers:
#      the SessionStart rule, the Stop gate, and the PostToolUse reinforcement.
#      Claude Code CLI fires; Desktop is silent on every one of them.
#   2. The harness VETOES, it does not override: a terminal session in a project
#      carrying `"report_gate": false` stays off.
#   3. The claim handoff carries the same discrimination; the Python spawn
#      handoff provably does not.
#   4. The CLI refuses an unsupported harness on EVERY command — and still
#      runs for a human at a shell prompt.
#
# Exit 0 on all-passed, 1 on any failure.
#
# How the harnesses are simulated: by the environment, because that is literally
# the whole mechanism. Claude Code CLI exports CLAUDE_CODE_ENTRYPOINT=cli to its
# subprocesses; the Desktop app hosts the agent through the Agent SDK and exports
# CLAUDE_AGENT_SDK_VERSION with none of the CLI's variables. These are not stubs
# — they reproduce the observed conditions exactly (dumps taken 2026-08-13).
#
# What this suite does NOT do: run any other task's verify script. Those are
# pre-land gates for their own task in their own worktree, not a regression
# suite. Project-wide regression here is `go build/vet/test ./...` + `just test`.
#
# Why the hook runs with an explicit --config-dir: `endless-go hook` calls
# PinMainDB (E-1450/E-1429) so hook-fired writes always hit the REAL DB
# regardless of cwd. HasExplicitDBContext is the documented seam for exactly
# this case, which is what lets a test drive the hook against the sandbox
# instead of the user's real ledger.
#
# Why a synthetic cwd: this repo ships `"report_gate": false` in its own
# .endless/config.json, so a gate test run from the worktree root would fail
# open and prove nothing. Two throwaway directories under the gitignored
# .endless/tmp/ carry an explicit `true` and `false`, exercising the REAL
# nearest-config resolution.
#
# Model: tests/tasks/e-1953-verify.sh.

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
# script being run from inside a Claude session.
TEST_UUID="e1962e1962-0000-4000-8000-000000000962"
SANDBOX_CFG=""
SESSION_EID=""
GATE_ON_DIR=""
GATE_OFF_DIR=""

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

# hook HARNESS PAYLOAD -> the hook's stdout.
#
# HARNESS is `terminal` or `desktop`, and is the ONLY difference between the two
# calls: same binary, same DB, same payload, same cwd.
hook() {
    local harness="$1" payload="$2"
    case "${harness}" in
        terminal) printf '%s' "${payload}" | env CLAUDE_CODE_ENTRYPOINT=cli \
                      ./bin/endless-go --config-dir "${SANDBOX_CFG}" hook claude 2>/dev/null ;;
        desktop)  printf '%s' "${payload}" | env -u CLAUDE_CODE_ENTRYPOINT \
                      CLAUDE_AGENT_SDK_VERSION=0.3.222 \
                      __CFBundleIdentifier=com.anthropic.claudefordesktop \
                      ./bin/endless-go --config-dir "${SANDBOX_CFG}" hook claude 2>/dev/null ;;
        *)        printf 'BAD HARNESS %s' "${harness}" ;;
    esac
}

# cli HARNESS ARGS... -> `endless ARGS...` output under that harness, with its
# exit status preserved as this function's.
cli() {
    local harness="$1"; shift
    case "${harness}" in
        terminal) env CLAUDE_CODE_ENTRYPOINT=cli uv run endless "$@" 2>&1 ;;
        desktop)  env -u CLAUDE_CODE_ENTRYPOINT CLAUDE_AGENT_SDK_VERSION=0.3.222 \
                      __CFBundleIdentifier=com.anthropic.claudefordesktop \
                      uv run endless "$@" 2>&1 ;;
        human)    env -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_AGENT_SDK_VERSION \
                      -u __CFBundleIdentifier -u CLAUDECODE \
                      uv run endless "$@" 2>&1 ;;
        *)        printf 'BAD HARNESS %s' "${harness}" ;;
    esac
}

session_start_payload() {
    printf '{"session_id":"%s","cwd":"%s","hook_event_name":"SessionStart","transcript_path":"","source":"startup"}' \
        "${TEST_UUID}" "$1"
}

stop_payload() {
    printf '{"session_id":"%s","cwd":"%s","hook_event_name":"Stop","transcript_path":"","last_assistant_message":"I finished the thing."}' \
        "${TEST_UUID}" "$1"
}

posttooluse_payload() {
    printf '{"session_id":"%s","cwd":"%s","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"endless task report --draft-file /tmp/d.md"}}' \
        "${TEST_UUID}" "$1"
}

# Arm a rendered-report checkpoint, which is what the PostToolUse reinforcement
# keys on (E-1953 made it key on the render, not the command name).
arm_checkpoint() {
    printf 'a minimized reply' | ./bin/endless-go --config-dir "${SANDBOX_CFG}" \
        session-query relay-checkpoint --session-id "${SESSION_EID}" >/dev/null 2>&1
}

# `sql` is a PYTHON CLI subcommand — endless-go has no such verb. Routing it
# through endless-go silently no-ops (the error goes to the /dev/null these calls
# used to carry), which is how an earlier version of this script "reset" nothing
# and still passed.
sql() {
    uv run endless sql "$1" --db sandbox 2>/dev/null
}

sql_write() {
    uv run endless sql "$1" --write --db sandbox >/dev/null 2>&1
}

# sql_scalar QUERY -> the single value in the first result row.
sql_scalar() {
    sql "$1" | sed -n '3p' | tr -d ' '
}

reset_turn() {
    sql_write "DELETE FROM session_gates"
    sql_write "UPDATE sessions SET report_bounces=0, report_exempt=0, report_runs=0"
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

assert_text_contains() {
    local desc="$1" needle="$2" haystack="$3"
    if [[ "${haystack}" == *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output contains '${needle}'" "${haystack:0:200}"
}

assert_empty() {
    local desc="$1" got="$2"
    if [[ -z "${got}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "no hook output at all" "${got:0:200}"
}

# ─── Part 0: build + automated suites ───────────────────────────────────────

test_build_and_suites() {
    section "Part 0 — build + automated suites (fail-fast)"

    # `just build`, NOT `go build ./...`. The latter compiles and discards; it
    # leaves bin/endless-go untouched, so every end-to-end part below would drive
    # whatever binary happened to be lying there. That is not hypothetical — it
    # bit this suite during development, and a stale binary passing looks exactly
    # like the change working.
    assert_succeeds "just build (refreshes bin/endless-go)" just build
    assert_succeeds "go vet ./internal/agentenv/... ./internal/hookcmd/..." \
        go vet ./internal/agentenv/... ./internal/hookcmd/...
    assert_succeeds "go test ./internal/agentenv/... (detector table + allow-list)" \
        go test ./internal/agentenv/...
    assert_succeeds "go test ./internal/hookcmd/... (call-site placement)" \
        go test ./internal/hookcmd/...
    assert_succeeds "pytest tests/test_agent_env.py (python mirror + banner)" \
        uv run pytest tests/test_agent_env.py -q

    if [[ "${FAIL_COUNT}" -gt 0 ]]; then
        printf '\n  %sFail-fast: the unit contracts are broken; skipping the live parts.%s\n' \
            "${RED}${BOLD}" "${RESET}"
        summary
        exit 1
    fi
}

# ─── Part 1: the three consumers discriminate ───────────────────────────────

# The load-bearing part. E-1953's invariant is that a session is never TOLD to
# use a channel that will not gate it, nor gated without having been told — so
# it is not enough that Desktop stops being blocked at Stop. All three consumers
# have to move together, which is why the harness check lives in
# reportChannelOn rather than at any one call site.
test_harness_discrimination() {
    section "Part 1 — Claude Code CLI fires, Desktop is silent (real hook binary)"

    local out

    out=$(hook terminal "$(session_start_payload "${GATE_ON_DIR}")")
    assert_text_contains "SessionStart/terminal: the report rule is injected" \
        'Report channel: every reply you send the user' "${out}"
    assert_text_contains "SessionStart/terminal: names the command" \
        '--draft-file' "${out}"

    out=$(hook desktop "$(session_start_payload "${GATE_ON_DIR}")")
    assert_empty "SessionStart/desktop: nothing injected" "${out}"

    reset_turn
    out=$(hook terminal "$(stop_payload "${GATE_ON_DIR}")")
    assert_text_contains "Stop/terminal: an unreported reply is blocked" \
        '"decision":"block"' "${out}"
    assert_text_contains "Stop/terminal: names the command it wanted" \
        'endless task report' "${out}"

    reset_turn
    out=$(hook desktop "$(stop_payload "${GATE_ON_DIR}")")
    assert_empty "Stop/desktop: the turn is allowed to end" "${out}"

    reset_turn
    arm_checkpoint
    out=$(hook terminal "$(posttooluse_payload "${GATE_ON_DIR}")")
    assert_text_contains "PostToolUse/terminal: the reinforcement fires" \
        'verbatim' "${out}"

    reset_turn
    arm_checkpoint
    out=$(hook desktop "$(posttooluse_payload "${GATE_ON_DIR}")")
    assert_empty "PostToolUse/desktop: no reinforcement" "${out}"
    reset_turn

    # The stronger claim: on an unsupported harness the hook is a NO-OP, not
    # merely quiet. Silence alone would also be produced by a hook that ran, wrote
    # a session row and had nothing to say — and that row is the actual harm
    # (E-1505: a Desktop session gets an empty `process`, so every pane→session
    # lookup misses, and a SessionStart once registered the home directory as a
    # project). Proved by writing to the DB, not by reading stdout.
    local fresh="e1962dead-0000-4000-8000-00000000dead" rows
    # Clear first: the sandbox DB survives between runs, so a row left by an
    # earlier run would make this assert the past instead of the present.
    sql_write "DELETE FROM sessions WHERE session_id = '${fresh}'"
    rm -f .endless/worktree.lock
    hook desktop "$(printf '{"session_id":"%s","cwd":"%s","hook_event_name":"SessionStart","transcript_path":"","source":"startup"}' \
        "${fresh}" "${GATE_ON_DIR}")" >/dev/null
    rows=$(sql_scalar "SELECT COUNT(*) FROM sessions WHERE session_id = '${fresh}'")
    assert_eq "SessionStart/desktop: registers no session row" "0" "${rows}"

    # And it leaves no worktree lock. Same point one layer out: the lock is
    # written during the same event, and a stray one blocks the next real claim
    # with "this worktree is already owned by <a session that never existed>".
    if [[ -f .endless/worktree.lock ]] && grep -q "${fresh}" .endless/worktree.lock; then
        report_fail "SessionStart/desktop: writes no worktree lock" \
            "no lock naming the desktop session" "$(cat .endless/worktree.lock)"
        rm -f .endless/worktree.lock
    else
        report_pass "SessionStart/desktop: writes no worktree lock"
    fi
}

# ─── Part 2: the harness vetoes, never overrides ────────────────────────────────

# The direction that protects Endless's own checkout. The harness and `report_gate`
# are independent veto axes: a terminal session is necessary for the channel,
# never sufficient. If this regressed, the one repo that deliberately opted out
# would have the gate switched back on under it.
test_harness_does_not_override_config() {
    section "Part 2 — the harness VETOES; it does not override report_gate"

    local out
    out=$(hook terminal "$(session_start_payload "${GATE_OFF_DIR}")")
    assert_empty "terminal + report_gate:false: still no rule" "${out}"

    reset_turn
    out=$(hook terminal "$(stop_payload "${GATE_OFF_DIR}")")
    assert_empty "terminal + report_gate:false: Stop still not gated" "${out}"
    reset_turn

    note "this repo's own .endless/config.json ships report_gate:false"
}

# ─── Part 3: the claim handoff ──────────────────────────────────────────────

# The claim handoff renders in the CLAIMING session's own hook, so its
# environment is that session's. A Desktop session that claims a task must not
# be handed a contract its own Stop hook will not enforce — the same defect,
# one harness over.
test_claim_handoff() {
    section "Part 3 — the claim handoff carries the same discrimination"

    local src
    src=$(cat internal/hookcmd/claim_handoff.go)
    assert_text_contains "claim handoff ANDs the harness with the config key" \
        'supportedAgent() && monitor.ReportGateEnabledForCwd(' "${src}"

    # And that the Python spawn handoff was deliberately left alone: `task
    # spawn` opens a tmux window, so the session it describes is a terminal
    # Claude Code one by construction, no matter which harness ran the command.
    # Gating it on the CALLER's environment would strip the contract from
    # terminal sessions spawned from Desktop.
    local py
    py=$(cat src/endless/task_cmd.py)
    if [[ "${py}" == *"agent_env"* || "${py}" == *"CLAUDE_CODE_ENTRYPOINT"* ]]; then
        report_fail "python spawn handoff stays harness-agnostic" \
            "no harness detection in task_cmd.py" "found some"
    else
        report_pass "python spawn handoff stays harness-agnostic"
    fi
}

# ─── Part 4: the CLI refusal ────────────────────────────────────────────────

# The one place Endless SPEAKS to an unsupported harness rather than staying
# silent. Silence is right for the hooks — there is nothing to enforce — but a
# Desktop session running `endless` has asked a direct question, and the honest
# answer is that this tool does not support it.
#
# Why it matters at all: CLAUDE.md files say "First: run `endless guide`". An
# agent on Desktop reads that, runs it, and without this lands in a workflow it
# cannot complete.
test_cli_refusal() {
    section "Part 4 — the CLI refuses an unsupported harness"

    local out rc

    out=$(cli desktop guide); rc=$?
    assert_text_contains "desktop: names the harness" \
        'does not support Claude Code Desktop' "${out}"
    assert_text_contains "desktop: says the command did not run" \
        'This command did not run' "${out}"
    assert_text_contains "desktop: disarms the CLAUDE.md instruction" \
        'does not apply here' "${out}"
    assert_eq "desktop: exits non-zero" "1" "${rc}"

    # No task id. "Do not use Endless here" plus "look up E-NNNN" is a
    # contradiction — resolving the second requires the first.
    if [[ "${out}" =~ E-[0-9]+ ]]; then
        report_fail "desktop: cites no Endless task id" \
            "no E-NNNN in the banner" "found one"
    else
        report_pass "desktop: cites no Endless task id"
    fi

    # The banner and the guide are contradictory instructions; only one ships.
    if [[ "${out}" == *"The happy path"* ]]; then
        report_fail "desktop: the output itself is withheld" \
            "no guide body after the banner" "guide body present"
    else
        report_pass "desktop: the output itself is withheld"
    fi

    # Every command, not just guide — the refusal is in the group callback.
    local cmd
    for cmd in "task list" "session status" "project list"; do
        out=$(cli desktop ${cmd})
        assert_text_contains "desktop: \`endless ${cmd}\` refuses too" \
            'does not support Claude Code Desktop' "${out}"
    done

    out=$(cli terminal guide)
    assert_text_contains "terminal: the guide prints normally" \
        'Using Endless in a Claude Code Session' "${out}"

    # Fails OPEN where the hooks fail closed, and on purpose: UNKNOWN is
    # overwhelmingly a person at a prompt, and locking them out of their own
    # tool to defend against a harness that may not exist is the worse trade.
    out=$(cli human guide)
    assert_text_contains "bare shell: a human still gets the guide" \
        'Using Endless in a Claude Code Session' "${out}"
}

# ─── main ───────────────────────────────────────────────────────────────────

cleanup() {
    [[ -n "${GATE_ON_DIR}"  && -d "${GATE_ON_DIR}"  ]] && rm -rf "${GATE_ON_DIR}"
    [[ -n "${GATE_OFF_DIR}" && -d "${GATE_OFF_DIR}" ]] && rm -rf "${GATE_OFF_DIR}"
    return 0
}

main() {
    local repo_root
    repo_root=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${repo_root}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${repo_root}" || exit 2

    # Deliberately NOT exported here: this suite's whole
    # subject is the difference between the two environments, so each hook call
    # sets its own.
    unset CLAUDE_CODE_ENTRYPOINT

    if ! command -v uv >/dev/null 2>&1; then
        printf 'ERROR: uv not on PATH\n' >&2
        exit 2
    fi

    SANDBOX_CFG="$(uv run endless db path --db sandbox 2>/dev/null | xargs dirname)"
    if [[ -z "${SANDBOX_CFG}" || ! -d "${SANDBOX_CFG}" ]]; then
        printf 'ERROR: cannot resolve the sandbox config dir; run `just dev-sandbox-init`\n' >&2
        exit 2
    fi

    GATE_ON_DIR="${repo_root}/.endless/tmp/e-1962-gate-on"
    GATE_OFF_DIR="${repo_root}/.endless/tmp/e-1962-gate-off"
    trap cleanup EXIT
    mkdir -p "${GATE_ON_DIR}/.endless" "${GATE_OFF_DIR}/.endless"
    printf '{"report_gate": true}\n'  > "${GATE_ON_DIR}/.endless/config.json"
    printf '{"report_gate": false}\n' > "${GATE_OFF_DIR}/.endless/config.json"

    printf '%sE-1962 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:      %s\n' "${repo_root}"
    printf '  db:       sandbox (%s)\n' "${SANDBOX_CFG}"
    printf '  gate on:  %s\n' "${GATE_ON_DIR}"
    printf '  gate off: %s\n' "${GATE_OFF_DIR}"

    test_build_and_suites

    SESSION_EID=$(./bin/endless-go --config-dir "${SANDBOX_CFG}" session-query ensure-claude-id \
        --session-id "${TEST_UUID}" --project-root "${repo_root}" 2>/dev/null)
    if [[ ! "${SESSION_EID}" =~ ^[0-9]+$ ]]; then
        printf '\nERROR: could not create a sandbox session row (got %q)\n' "${SESSION_EID}" >&2
        exit 2
    fi

    test_harness_discrimination
    test_harness_does_not_override_config
    test_claim_handoff
    test_cli_refusal

    summary
}

main "$@"
