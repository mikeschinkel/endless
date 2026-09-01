#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1966 and records what was true when E-1966
# landed. Edit it only if you ARE E-1966. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1966 verification — the Python wind-down report nudge obeys `report_gate`,
# and harness detection has exactly one implementation.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1966
#
# Single entry point (per E-1596). Fail-fast on the unit contracts, then drive
# the REAL `endless` CLI and watch what it prints.
#
#   0. The automated suites: the nudge's transition + gate tests, the detector
#      and its consumers, the --help augmentation.
#   1. The bug itself, end to end: `task update --status unverified` prints the
#      nudge where the channel is on and stays silent where it is off.
#   2. It is the GATE that went quiet, not the nudge. The same command in the
#      same directory still does everything else it did.
#   3. Harness detection follows `agent_env` (E-1962), not CLAUDECODE=1.
#   4. The third spelling is gone and the rename is complete.
#
# Exit 0 on all-passed, 1 on any failure.
#
# ─── why the directories, and why not inside the worktree ───────────────────
#
# The gate resolves from CWD through `config.enclosing_project_root()`, so
# proving both directions needs two real directories carrying two real
# configs. This repo IS the gate-off case — it ships `"report_gate": false` —
# so it serves as one of them, unmocked.
#
# The gate-on directory must live OUTSIDE the worktree. `enclosing_project_root`
# special-cases any path containing `.endless/worktrees/e-NNN` and returns the
# MAIN checkout above it, ignoring any nearer config; a gate-on dir under
# `.endless/tmp/` here would silently resolve to this repo's `false` and the
# test would prove the opposite of what it claims. (E-1962's suite puts its
# fixture dirs under `.endless/tmp/` because the Go hook takes cwd from its
# JSON payload and resolves nearest-config — a different resolver.)
#
# Which is also why the CLI is routed by XDG_CONFIG_HOME rather than `--db
# sandbox`: from a directory outside the worktree `--db` is refused (it is
# gated on the enclosing project being self-dev), and the sandbox is the point
# — none of this touches the real ledger.
#
# What this suite does NOT do: run any other task's verify script. Those are
# pre-land gates for their own task in their own worktree, not a regression
# suite. Project-wide regression here is `go build/vet/test ./...` + `just test`.
#
# Model: .endless/tasks/e-1962/verify.sh.

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

REPO_ROOT=""
SANDBOX_XDG=""
GATE_ON_DIR=""
GATE_UNSET_DIR=""
PROBE_ID=""

# The stable fragment of the nudge — the pointer at the report command. Present
# means it fired, whatever the surrounding prose says.
MARKER="endless task report"

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

# cli DIR HARNESS ARGS... -> the real `endless ARGS...`, run from DIR under the
# given harness environment, always against the sandbox DB.
#
# HARNESS is `terminal` (Claude Code CLI), `claudecode` (CLAUDECODE=1 and
# nothing else — what the retired detector keyed on), or `human` (bare shell).
cli() {
    local dir="$1" harness="$2"; shift 2
    local -a env_args=(XDG_CONFIG_HOME="${SANDBOX_XDG}")
    case "${harness}" in
        terminal)   env_args+=(CLAUDE_CODE_ENTRYPOINT=cli CLAUDECODE=1) ;;
        claudecode) env_args+=(CLAUDECODE=1) ;;
        human)      : ;;
        *)          printf 'BAD HARNESS %s' "${harness}"; return 1 ;;
    esac
    # Two flags this script must always carry, both for the same reason: an
    # ERROR prints no nudge either, so anything that stops the command from
    # running reads as a passing "silent" case unless assert_silent catches it
    # (it does now — both of these were found that way).
    #
    #   --db sandbox   inside the worktree the self-dev gate demands an
    #                  explicit --db. Refused from the tmp dirs, hence the
    #                  conditional; those route by XDG_CONFIG_HOME instead.
    #   --no-session   every call here is from a bare shell, and the session
    #                  resolver refuses a pane it cannot map to a live Claude
    #                  session. Without it the suite only passes when whoever
    #                  runs it happens to sit in a bound pane.
    local -a extra_args=(--no-session)
    [[ "${dir}" == "${REPO_ROOT}" ]] && extra_args+=(--db sandbox)
    (cd "${dir}" && env -u CLAUDE_CODE_ENTRYPOINT -u CLAUDECODE \
        -u __CFBundleIdentifier -u CLAUDE_AGENT_SDK_VERSION \
        -u ENDLESS_SESSION_ID -u TMUX_PANE \
        "${env_args[@]}" uv run --project "${REPO_ROOT}" endless "$@" \
        "${extra_args[@]}" 2>&1)
}

# wind_down DIR HARNESS -> the output of one underway -> unverified transition
# on the probe task. Re-arms the task first so it can be run repeatedly.
wind_down() {
    local dir="$1" harness="$2"
    cli "${dir}" human task update "${PROBE_ID}" --status underway >/dev/null 2>&1
    cli "${dir}" "${harness}" task update "${PROBE_ID}" --status unverified
}

# ─── assertions ─────────────────────────────────────────────────────────────

assert_succeeds() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=$(printf '%s' "${output}" | tail -20)"
}

assert_fired() {
    local desc="$1" out="$2"
    if [[ "${out}" == *"${MARKER}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "the nudge (contains '${MARKER}')" "${out:0:220}"
}

assert_silent() {
    local desc="$1" out="$2"
    # A command that failed prints no nudge either. Silence only counts when the
    # command RAN — otherwise every one of these passes the day the invocation
    # breaks, which is precisely how this suite once went green.
    if [[ "${out}" == *"Error:"* || "${out}" == *"Traceback"* ]]; then
        report_fail "${desc}" "the command to run and print no nudge" \
            "it failed: ${out:0:220}"
        return
    fi
    if [[ "${out}" != *"${MARKER}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "no nudge" "${out:0:220}"
}

assert_contains() {
    local desc="$1" needle="$2" haystack="$3"
    if [[ "${haystack}" == *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output contains '${needle}'" "${haystack:0:220}"
}

assert_absent() {
    local desc="$1" needle="$2" haystack="$3"
    if [[ "${haystack}" != *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output does NOT contain '${needle}'" "${haystack:0:220}"
}

# ─── Part 0: the automated suites ───────────────────────────────────────────

test_suites() {
    section "Part 0 — automated suites (fail-fast)"

    assert_succeeds "pytest tests/test_report_reminder.py (transitions + the gate)" \
        uv run pytest tests/test_report_reminder.py -q
    assert_succeeds "pytest tests/test_agent_env.py (detector + its consumers)" \
        uv run pytest tests/test_agent_env.py -q
    assert_succeeds "pytest tests/test_agent_help.py (--help augmentation)" \
        uv run pytest tests/test_agent_help.py -q
    assert_succeeds "pytest tests/test_handoff.py (the renamed gate reader's other caller)" \
        uv run pytest tests/test_handoff.py -q

    if [[ "${FAIL_COUNT}" -gt 0 ]]; then
        printf '\n  %sFail-fast: the unit contracts are broken; skipping the live parts.%s\n' \
            "${RED}${BOLD}" "${RESET}"
        summary
        exit 1
    fi
}

# ─── Part 1: the bug, end to end ────────────────────────────────────────────

# The load-bearing part, and the one that reproduces the original report: the
# nudge fired during E-1962's own `task update --status unverified`, inside a
# checkout that ships `"report_gate": false`. It asserts that all further
# reporting for the session routes through `task report` — where the gate is
# off, nothing routes it and nothing enforces it, so the claim is simply false.
test_the_nudge_obeys_the_gate() {
    section "Part 1 — the nudge obeys report_gate (real CLI)"

    local out

    out=$(wind_down "${REPO_ROOT}" terminal)
    assert_silent "report_gate:false (this repo): silent" "${out}"

    out=$(wind_down "${GATE_ON_DIR}" terminal)
    assert_fired "report_gate:true: still fires" "${out}"

    out=$(wind_down "${GATE_UNSET_DIR}" terminal)
    assert_fired "key absent: still fires (only an explicit false opts out)" "${out}"

    note "this repo's own .endless/config.json ships report_gate:false"
}

# ─── Part 2: the gate went quiet, not the command ───────────────────────────

# A nudge deleted outright would pass Part 1 too. These pin down that only the
# gated sentence moved: the transition still happens and still reports itself,
# and the wind-down predicate still discriminates where the channel is live.
test_only_the_nudge_moved() {
    section "Part 2 — the gate went quiet, not the command"

    local out

    out=$(wind_down "${REPO_ROOT}" terminal)
    assert_contains "gate off: the status change still happens and still prints" \
        "underway -> unverified" "${out}"

    # Not a wind-down, in a project whose channel IS on: still silent, so
    # Part 1's firing is the transition logic and not a blanket "always on".
    cli "${GATE_ON_DIR}" human task update "${PROBE_ID}" --status ready >/dev/null 2>&1
    out=$(cli "${GATE_ON_DIR}" terminal task update "${PROBE_ID}" --status underway)
    assert_silent "gate on: ready -> underway is not a wind-down" "${out}"

    # A human at the same prompt, in the same gate-on project, is not the
    # audience — the pre-existing agent gate still stands alongside the new one.
    out=$(wind_down "${GATE_ON_DIR}" human)
    assert_silent "gate on: a human invocation is still not nudged" "${out}"
}

# ─── Part 3: detection comes from agent_env ─────────────────────────────────

# `_running_under_agent()` used to be its own CLAUDECODE=1 test — "some Claude
# Code", not which, and blind to every non-Claude harness. It now delegates to
# agent_env (E-1962), which is the discriminating case below: CLAUDECODE=1 with
# no entrypoint is no longer an agent.
test_detection_delegates() {
    section "Part 3 — harness detection comes from agent_env"

    local out

    out=$(wind_down "${GATE_ON_DIR}" terminal)
    assert_fired "CLAUDE_CODE_ENTRYPOINT=cli: recognized as an agent" "${out}"

    out=$(wind_down "${GATE_ON_DIR}" claudecode)
    assert_silent "CLAUDECODE=1 alone: not a recognized harness" "${out}"

    out=$(wind_down "${GATE_ON_DIR}" human)
    assert_silent "bare shell: not an agent" "${out}"

    # The same detector now drives the --help directive block, which carried the
    # third copy of the CLAUDECODE test until this task folded it in.
    out=$(cli "${REPO_ROOT}" terminal task spawn --help)
    assert_contains "--help under the CLI harness: the agent block is prepended" \
        "AGENT" "${out}"
    out=$(cli "${REPO_ROOT}" claudecode task spawn --help)
    assert_absent "--help under CLAUDECODE=1 alone: no agent block" \
        "AGENT — read this" "${out}"
    out=$(cli "${REPO_ROOT}" human task spawn --help)
    assert_absent "--help for a human: no agent block" "AGENT — read this" "${out}"
    # ...but the human opt-in is a separate term, not folded into detection.
    out=$(cli "${REPO_ROOT}" human task spawn --help --agent-view)
    assert_contains "--agent-view still lets a human preview it" "AGENT" "${out}"
}

# ─── Part 4: one spelling, one reader ───────────────────────────────────────

test_no_duplicate_spellings() {
    section "Part 4 — one detector, one gate reader"

    local py help_py
    py=$(cat "${REPO_ROOT}/src/endless/task_cmd.py")
    help_py=$(cat "${REPO_ROOT}/src/endless/agent_help.py")

    assert_absent "agent_help no longer answers the harness question itself" \
        "def is_claude_code_agent" "${help_py}"
    assert_contains "agent_help asks the detector" "agent_env.detect()" "${help_py}"
    assert_contains "_running_under_agent asks the detector" \
        "agent_env.detect()" "${py}"

    # The gate reader serves two emitters now, so its handoff-specific name went
    # with the rename. A surviving old name means a second copy got written.
    assert_absent "_handoff_report_gate is gone (renamed, not duplicated)" \
        "_handoff_report_gate" "${py}"
    assert_contains "the spawn handoff reads through the renamed helper" \
        '"report_gate": _report_gate_on()' "${py}"

    # And the handoff still renders with the gate honored, through the real CLI.
    local out
    out=$(cli "${REPO_ROOT}" human task handoff "${PROBE_ID}")
    assert_absent "handoff in a report_gate:false project omits the instructions" \
        "${MARKER}" "${out}"
}

# ─── main ───────────────────────────────────────────────────────────────────

cleanup() {
    if [[ -n "${PROBE_ID}" ]]; then
        cli "${REPO_ROOT}" human task update "${PROBE_ID}" --status obsolete \
            >/dev/null 2>&1 || true
    fi
    [[ -n "${GATE_ON_DIR}"    && -d "${GATE_ON_DIR}"    ]] && rm -rf "${GATE_ON_DIR}"
    [[ -n "${GATE_UNSET_DIR}" && -d "${GATE_UNSET_DIR}" ]] && rm -rf "${GATE_UNSET_DIR}"
    return 0
}

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${REPO_ROOT}" || exit 2

    if ! command -v uv >/dev/null 2>&1; then
        printf 'ERROR: uv not on PATH\n' >&2
        exit 2
    fi

    # Deliberately NOT exported: every `cli` call sets its own harness, because
    # the difference between those environments is half this suite's subject.
    unset CLAUDE_CODE_ENTRYPOINT CLAUDECODE

    local sandbox_db
    sandbox_db=$(uv run endless db path --db sandbox 2>/dev/null)
    if [[ -z "${sandbox_db}" || ! -f "${sandbox_db}" ]]; then
        printf 'ERROR: cannot resolve the sandbox DB; run `just dev-sandbox-init`\n' >&2
        exit 2
    fi
    # <sandbox>/endless/endless.db -> XDG_CONFIG_HOME is <sandbox>.
    SANDBOX_XDG=$(dirname "$(dirname "${sandbox_db}")")

    # Outside the worktree on purpose — see the header.
    GATE_ON_DIR=$(mktemp -d "${TMPDIR:-/tmp}/e-1966-gate-on-XXXXXX")
    GATE_UNSET_DIR=$(mktemp -d "${TMPDIR:-/tmp}/e-1966-gate-unset-XXXXXX")
    trap cleanup EXIT
    mkdir -p "${GATE_ON_DIR}/.endless" "${GATE_UNSET_DIR}/.endless"
    # The project name matches this repo's so event emission resolves the same
    # registered project from either directory; only report_gate differs.
    printf '{"name": "endless", "report_gate": true}\n' \
        > "${GATE_ON_DIR}/.endless/config.json"
    printf '{"name": "endless"}\n' \
        > "${GATE_UNSET_DIR}/.endless/config.json"

    printf '%sE-1966 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:         %s\n' "${REPO_ROOT}"
    printf '  db:          sandbox (%s)\n' "${sandbox_db}"
    printf '  gate off:    %s %s(this repo)%s\n' "${REPO_ROOT}" "${DIM}" "${RESET}"
    printf '  gate on:     %s\n' "${GATE_ON_DIR}"
    printf '  gate unset:  %s\n' "${GATE_UNSET_DIR}"

    test_suites

    PROBE_ID=$(cli "${REPO_ROOT}" human task add "Probe the wind-down nudge" \
        --tier 1 | sed -n 's/.*Added \(E-[0-9]*\).*/\1/p' | head -1)
    if [[ ! "${PROBE_ID}" =~ ^E-[0-9]+$ ]]; then
        printf '\nERROR: could not create a sandbox probe task (got %q)\n' \
            "${PROBE_ID}" >&2
        exit 2
    fi
    printf '  probe task:  %s %s(sandbox)%s\n' "${PROBE_ID}" "${DIM}" "${RESET}"

    test_the_nudge_obeys_the_gate
    test_only_the_nudge_moved
    test_detection_delegates
    test_no_duplicate_spellings

    summary
}

main "$@"
