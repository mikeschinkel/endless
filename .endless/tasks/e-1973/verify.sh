#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1973 and records what was true when E-1973
# landed. Edit it only if you ARE E-1973. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1973 verification — the appeal budget is enforcement state, so it must not
# refuse where nothing enforces.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1973
#
# The bug: `report_item` refused a third run with "You have already used this
# turn's one appeal" regardless of `report_gate`. Where the gate is off no Stop
# gate holds the turn and nothing reads the counter, so the refusal denied a
# command no one was enforcing on the basis of a number no one consulted — and
# it stranded sessions that reached for the minimizer voluntarily.
#
# Third of a family, all the same principle: off means the channel is not
# there, not that nobody is watching. E-1953 fixed the PostToolUse
# reinforcement and the spawn/claim handoffs; E-1966 fixed the Python wind-down
# nudge; this fixes the command's own budget. Part 3 below re-checks the whole
# family from one place, because the failure mode is "another emitter was
# missed" and a per-task test cannot see that.
#
# Exit 0 on all-passed, 1 on any failure.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

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

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT+1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT+1)); FAILED_TESTS+=("$1")
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
    printf '\n'; return 1
}
assert_succeeds() {
    local desc="$1"; shift
    local output rc; output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | ${output}"
}
assert_file_contains() {
    local desc="$1" pattern="$2" file="$3"
    if [[ -f "${file}" ]] && grep -qF -- "${pattern}" "${file}"; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "${file} contains: ${pattern}" "absent"
}

test_units() {
    section "Unit contract (fail-fast)"
    assert_succeeds "pytest: the budget is skipped when the gate is off, enforced when on" \
        uv run pytest tests/test_task_report.py -q \
        -k "appeal or second_run"
    assert_succeeds "pytest: the report command as a whole" \
        uv run pytest tests/test_task_report.py -q
}

test_the_fix() {
    section "The fix — the budget consults the gate"

    # The guard must ask the gate, not just the counter. Asserted at the call
    # site because the branch needs a resolvable session and a DB.
    assert_file_contains "the appeal bound is gated" \
        "if session_id is not None and _report_gate_on():" src/endless/report_cmd.py

    # And it must ask the SAME resolver the other emitters use. Three emitters
    # disagreeing about whether the channel is live is how this family of bugs
    # keeps recurring.
    assert_file_contains "via the shared resolver, not a second implementation" \
        "from endless.task_cmd import _report_gate_on as impl" src/endless/report_cmd.py
}

test_no_emitter_left_behind() {
    section "The family — every emitter consults the gate"

    # Go side (E-1953): SessionStart rule, PostToolUse reinforcement, Stop gate,
    # and the claim handoff all route through reportChannelOn / the config read.
    assert_file_contains "Go: PostToolUse reinforcement" \
        'payload.ToolName == "Bash" && reportChannelOn(projectID, isRegistered, payload.CWD)' \
        internal/hookcmd/claude.go
    assert_file_contains "Go: SessionStart rule" \
        "composeSessionStartContext(ctx, reportChannelOn(projectID, isRegistered, payload.CWD))" \
        internal/hookcmd/claude.go
    assert_file_contains "Go: Stop gate" \
        "if !reportChannelOn(projectID, isRegistered, payload.CWD) {" \
        internal/hookcmd/relay_gate.go
    assert_file_contains "Go: claim handoff" \
        "monitor.ReportGateEnabledForCwd(worktreePath, projectRoot)" \
        internal/hookcmd/claim_handoff.go

    # Python side: spawn handoff (E-1953), wind-down nudge (E-1966), and the
    # appeal budget (this task) all call the one resolver.
    assert_file_contains "Python: spawn handoff" \
        '"report_gate": _report_gate_on(),' src/endless/task_cmd.py
    assert_file_contains "Python: wind-down nudge" \
        "if not _report_gate_on():" src/endless/task_cmd.py

    # The resolver itself must default ON. An unresolvable project root is
    # ignorance, not an opt-out — defaulting off would silently ungate every
    # project whose path lookup ever failed.
    assert_succeeds "the shared resolver defaults ON" \
        uv run python -c "
from endless import config
from pathlib import Path
import tempfile
d = Path(tempfile.mkdtemp())
assert config.project_report_gate(d) is True, 'missing config should default ON'
(d / '.endless').mkdir()
(d / '.endless' / 'config.json').write_text('{}')
assert config.project_report_gate(d) is True, 'absent key should default ON'
(d / '.endless' / 'config.json').write_text('{\"report_gate\": false}')
assert config.project_report_gate(d) is False, 'explicit false must turn it off'
"
}

test_this_repo_is_off() {
    section "This repo ships the gate OFF"
    assert_file_contains "endless opts out" '"report_gate": false' .endless/config.json
}

main() {
    local repo_root
    repo_root=$(git rev-parse --show-toplevel 2>/dev/null) || { echo "not in a git worktree" >&2; exit 2; }
    cd "${repo_root}" || exit 2
    command -v uv >/dev/null 2>&1 || { echo "uv not on PATH" >&2; exit 2; }

    printf '%sE-1973 verification%s\n%s\n  cwd: %s\n' \
        "${BOLD}" "${RESET}" "${UNDERLINE}" "${repo_root}"

    test_units
    test_the_fix
    test_no_emitter_left_behind
    test_this_repo_is_off
    summary
}

main "$@"
