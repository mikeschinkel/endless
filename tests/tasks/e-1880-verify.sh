#!/usr/bin/env bash
#
# E-1880 verification — `endless task report` no longer contradicts the handoff
# contract it serves.
#
# The bug, observed live at the E-1870 handoff, was a command that could not be
# obeyed. `_close.tmpl` tells every spawned session two things at once: "run
# `endless task report <id>` and relay its output verbatim", and "do NOT recap
# task status, phase, or relationships". The report then emitted `Task:`,
# `Status:`, `Landed:` and a parent/child recap under a steer that said "add
# nothing else" — and printed the same ids twice, once under "Follow-ups you
# filed" and again under "Children", because `--parent E-N --cleans-up E-N` (the
# filing pattern every handoff prescribes) creates both relations.
#
# The fix holds the command to the bar it already enforces on the agent: its own
# `note-check` DROPs any note that "restates something already visible in
# git/task state", so the command emits a line only when the user could not
# already know it.
#
# Run from anywhere inside the worktree:
#   esu
#   ./tests/tasks/e-1880-verify.sh
#
# Strategy (shape per tests/tasks/e-1771-verify.sh, the command's own script): a
# FULLY ISOLATED throwaway env (temp XDG_CONFIG_HOME + fresh DB, a temp git
# project) driving the CANDIDATE Python CLI (`.venv/bin/endless`) and the
# worktree-built `bin/endless-go`. Nothing touches the real endless repo/ledger.
#
# What it checks:
#   0. Fail-fast unit front: Python tests/test_task_report.py + the Go
#      TaskReportFacts / session-query task-report tests (the `type` field the
#      epic carve-out rides on is part of that wire contract).
#   1. No status recap: a task at `unverified` with a follow-up emits neither
#      `Status:` nor `Landed:` nor `Task: E-`.
#   2. Dedupe / the E-1870 case: a NON-epic with two `--parent N --cleans-up N`
#      follow-ups prints each id exactly once and renders no `Children:` line.
#   3. Epic carve-out: the same shape under `--type epic` DOES render
#      `Children:`, and still prints no id twice.
#   4. Empty block: a task with nothing to report renders the fixed
#      `nothing-to-report` line inside the relay markers (E-1901 replaced the
#      old separate steer-empty prompt; the no-invented-summary point stands).
#   5. Contract consistency: `_close.tmpl` still forbids recapping status and
#      relationships, and no line the renderer can emit violates that — the
#      regression guard that stops the two surfaces drifting apart again.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 setup error.

set -u

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'
    BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

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
    local t
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# assert_contains DESC HAYSTACK NEEDLE
assert_contains() {
    local desc="$1" hay="$2" needle="$3"
    if [[ "${hay}" == *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "contains: ${needle}" "${hay}"
}

# assert_not_contains DESC HAYSTACK NEEDLE
assert_not_contains() {
    local desc="$1" hay="$2" needle="$3"
    if [[ "${hay}" != *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "does NOT contain: ${needle}" "${hay}"
}

# assert_count_once DESC HAYSTACK TASK_ID — the whole point of check 2/3.
# The trailing [^0-9] alternative keeps E-4 from also matching E-40.
assert_count_once() {
    local desc="$1" hay="$2" needle="$3" n
    n=$(grep -oE -- "${needle}([^0-9]|$)" <<<"${hay}" | wc -l | tr -d ' ')
    if [[ "${n}" == "1" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${needle} appears exactly once" "appears ${n}x in:
${hay}"
}

REPO_ROOT=""
WORK=""
PROJ=""
EN=""
GO=""

cleanup() { [[ -n "${WORK}" && -d "${WORK}" ]] && rm -rf "${WORK}"; }
en() { ( cd "${PROJ}" && "${EN}" "$@" ); }

add_task() {
    local out; out=$(en task add "$@" 2>&1) || { printf '%s' "${out}"; return 1; }
    printf '%s\n' "${out}" | grep -oE 'E-[0-9]+' | head -1
}

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    EN="${REPO_ROOT}/.venv/bin/endless"
    GO="${REPO_ROOT}/bin/endless-go"
    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    [[ -x "${GO}" ]] || { printf 'ERROR: %s missing — run `just build`\n' "${GO}" >&2; exit 2; }
    if [[ ! -x "${EN}" ]]; then
        ( cd "${REPO_ROOT}" && uv run endless --version >/dev/null 2>&1 ) || {
            printf 'ERROR: could not materialize .venv (uv run endless failed)\n' >&2; exit 2; }
    fi

    WORK=$(mktemp -d)
    trap cleanup EXIT

    export XDG_CONFIG_HOME="${WORK}/config"
    export XDG_CACHE_HOME="${WORK}/cache"
    export ENDLESS_AUTO_MIGRATE=1
    export PATH="${REPO_ROOT}/bin:${PATH}"
    unset ENDLESS_SESSION_ID CLAUDECODE CLAUDE_CODE_SESSION_ID 2>/dev/null || true
    mkdir -p "${XDG_CONFIG_HOME}" "${XDG_CACHE_HOME}"

    PROJ="${WORK}/proj"
    mkdir -p "${PROJ}"
    git -C "${PROJ}" init -q -b main
    git -C "${PROJ}" config user.email "verify@example.com"
    git -C "${PROJ}" config user.name "Verify"
    : > "${PROJ}/README.md"
    git -C "${PROJ}" add README.md
    git -C "${PROJ}" commit -q -m "init"

    en project register "${PROJ}" --infer --name verify1880 --status active >/dev/null 2>&1 || {
        printf 'ERROR: registering temp project failed\n' >&2; exit 2; }
}

# seed_e1870_shape TYPE — a focal task of TYPE with two follow-ups filed exactly
# the way every handoff instructs (`--parent focal --cleans-up focal`), so each
# lands in BOTH the successor and the child list. Echoes "FOCAL F1 F2".
seed_e1870_shape() {
    local ttype="$1" focal f1 f2
    focal=$(add_task "Implement the ${ttype} thing" --type "${ttype}") || return 1
    f1=$(add_task "Follow up on the first gap" --parent "${focal}" --cleans-up "${focal}") || return 1
    f2=$(add_task "Follow up on the second gap" --parent "${focal}" --cleans-up "${focal}") || return 1
    printf '%s %s %s\n' "${focal}" "${f1}" "${f2}"
}

# ─── checks ─────────────────────────────────────────────────────────────────

check_unit_front() {
    section "0 — fail-fast unit front (Python + Go)"
    local out rc
    out=$(cd "${REPO_ROOT}" && uv run pytest tests/test_task_report.py -q 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "Python tests/test_task_report.py pass"
    else report_fail "Python tests/test_task_report.py pass" "pytest exit 0" "exit ${rc}
${out}"; fi

    out=$(cd "${REPO_ROOT}" && go test ./internal/monitor/ -run TaskReportFacts 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "Go TaskReportFacts tests pass (incl. the type slug)"
    else report_fail "Go TaskReportFacts tests pass (incl. the type slug)" "go test exit 0" "exit ${rc}
${out}"; fi

    out=$(cd "${REPO_ROOT}" && go test ./internal/sessionquerycmd/ -run TaskReport 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "Go session-query task-report wire contract passes"
    else report_fail "Go session-query task-report wire contract passes" "go test exit 0" "exit ${rc}
${out}"; fi
}

check_no_status_recap() {
    section "1 — no status recap (the instruction the handoff forbids)"
    local focal follow out
    focal=$(add_task "Ship the recap-free thing") || {
        report_fail "create focal task" "task id" "${focal}"; return; }
    follow=$(add_task "Follow up on the leftover" --cleans-up "${focal}") || {
        report_fail "create follow-up" "task id" "${follow}"; return; }
    en task update "${focal}" --status unverified >/dev/null 2>&1 || {
        report_fail "set focal to unverified" "exit 0" "update failed"; return; }

    out=$(en task report "${focal}" 2>&1)
    # Sanity: the report is non-empty, so absence below is a real omission and
    # not just an empty report trivially passing every assertion.
    assert_contains "the report does have something to say" "${out}" "Follow-ups you filed:"
    assert_not_contains "no 'Status:' line" "${out}" "Status:"
    assert_not_contains "no 'Landed' line" "${out}" "Landed"
    assert_not_contains "no 'Task: E-' echo of the id the user typed" "${out}" "Task: E-"
    assert_not_contains "no phase recap" "${out}" "Phase:"
}

check_dedupe_non_epic() {
    section "2 — E-1870's shape: each id printed exactly once, no Children line"
    local ids focal f1 f2 out
    ids=$(seed_e1870_shape todo) || { report_fail "seed E-1870 shape" "3 task ids" "${ids}"; return; }
    read -r focal f1 f2 <<<"${ids}"

    out=$(en task report "${focal}" 2>&1)
    assert_contains "the follow-ups are reported" "${out}" "Follow-ups you filed:"
    assert_count_once "${f1} printed exactly once" "${out}" "${f1}"
    assert_count_once "${f2} printed exactly once" "${out}" "${f2}"
    assert_not_contains "no 'Children:' recap for a non-epic" "${out}" "Children:"
}

check_epic_carveout() {
    section "3 — epic carve-out: Children IS rendered, still nothing twice"
    local ids focal f1 f2 child out
    ids=$(seed_e1870_shape epic) || { report_fail "seed epic shape" "3 task ids" "${ids}"; return; }
    read -r focal f1 f2 <<<"${ids}"
    # A plain child (parent only, no cleans_up) — the id that SHOULD surface
    # under Children for an epic, and that nothing else can report.
    child=$(add_task "Build the epic's child piece" --parent "${focal}") || {
        report_fail "create plain child" "task id" "${child}"; return; }

    out=$(en task report "${focal}" 2>&1)
    assert_contains "epic renders a Children line" "${out}" "Children:"
    assert_contains "the plain child is listed" "${out}" "Children: ${child}"
    assert_count_once "${child} printed exactly once" "${out}" "${child}"
    assert_count_once "${f1} printed exactly once (not in both lists)" "${out}" "${f1}"
    assert_count_once "${f2} printed exactly once (not in both lists)" "${out}" "${f2}"
}

check_empty_block() {
    # E-1901 superseded the old shape here. E-1880's point stands unchanged — a
    # clean session must not manufacture a summary — but the mechanism moved: the
    # empty case no longer gets a separate steer telling the agent to compose its
    # own one-liner (freehand prose the Stop gate cannot match), it gets a fixed
    # sanctioned string inside the same markers as any other report.
    section "4 — nothing to report → the fixed 'Nothing to report.' block"
    local focal out rc
    focal=$(add_task "Do the quiet thing") || {
        report_fail "create quiet task" "task id" "${focal}"; return; }
    out=$(en task report "${focal}" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "exit 0 with an empty fact block"
    else report_fail "exit 0 with an empty fact block" "exit 0" "exit ${rc} | ${out}"; fi
    assert_contains "emits the fixed nothing-to-report line" "${out}" "Nothing to report."
    assert_contains "still delimited for verbatim relay" "${out}" "----- BEGIN REPORT -----"
    # The original prohibition, restated against the current wording: no invented
    # summary, and none of the computable recap lines.
    assert_not_contains "no status recap in the empty case" "${out}" "Status:"
    assert_not_contains "no landing recap in the empty case" "${out}" "Landed"

    # It stays a REGISTERED prompt name, so the wording is user-editable like the
    # other three (ED-1531 Req 5) rather than being hardcoded product wording.
    printf '{"name":"nothing-to-report","text":"EMPTY-MARKER"}\n' \
        > "${PROJ}/.endless/report-prompts.jsonl"
    out=$(en task report "${focal}" 2>&1)
    assert_contains "project nothing-to-report override wins" "${out}" "EMPTY-MARKER"
    rm -f "${PROJ}/.endless/report-prompts.jsonl"
}

check_contract_consistency() {
    section "5 — the two surfaces still agree (drift guard)"
    local tmpl render
    tmpl="${REPO_ROOT}/internal/templatecmd/templates/handoff/_close.tmpl"
    [[ -f "${tmpl}" ]] || { report_fail "_close.tmpl exists" "${tmpl}" "missing"; return; }

    # The handoff side of the contract must still say it — if this prohibition is
    # ever dropped the renderer's omissions become unmotivated, not consistent.
    if grep -q 'Do NOT recap task status, phase, or relationships' "${tmpl}"; then
        report_pass "_close.tmpl still forbids recapping status/phase/relationships"
    else
        report_fail "_close.tmpl still forbids recapping status/phase/relationships" \
            "the prohibition is present" "not found in ${tmpl}"
    fi
    if grep -q 'relaying its output verbatim' "${tmpl}"; then
        report_pass "_close.tmpl still asks for the report to be relayed verbatim"
    else
        report_fail "_close.tmpl still asks for the report to be relayed verbatim" \
            "'relaying its output verbatim' present" "not found in ${tmpl}"
    fi

    # …and the command side must emit no line that violates it. Static, because
    # the runtime checks above can only prove it for the states they exercise.
    render="${REPO_ROOT}/src/endless/report_cmd.py"
    local body
    # E-1901 renamed the renderer to _render_sanctioned (and split the
    # agent-facing anomaly addendum out into _render_agent_notes). The static
    # check follows the rename: the sanctioned block is now the exact text the
    # user receives, so a forbidden line appearing in it is MORE serious than
    # before, not less.
    body=$(awk '/^def _render_sanctioned\(/,/^def _render_agent_notes\(/' "${render}" | grep -E '^\s+lines\.append')
    if [[ -z "${body}" ]]; then
        report_fail "_render_sanctioned still appends lines" "at least one lines.append" "none found"
        return
    fi
    local forbidden ok=1
    for forbidden in 'Task: E-' 'Status:' 'Landed' 'Phase:'; do
        if grep -qF -- "${forbidden}" <<<"${body}"; then
            report_fail "_render_sanctioned emits no forbidden line (${forbidden})" \
                "no lines.append with ${forbidden}" "${body}"
            ok=0
        fi
    done
    [[ "${ok}" -eq 1 ]] && report_pass "_render_sanctioned appends no status/phase/id recap line"

    # The epic gate is what keeps `Children:` from being a relationship recap.
    if grep -q 'facts.get("type") == "epic"' "${render}"; then
        report_pass "Children is gated on the epic type"
    else
        report_fail "Children is gated on the epic type" \
            'facts.get("type") == "epic" in _render_sanctioned' "not found in ${render}"
    fi
}

main() {
    setup

    printf '%sE-1880 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cli:     %s\n' "${EN}"
    printf '  go:      %s\n' "${GO}"
    printf '  env:     isolated (%s)\n' "${WORK}"

    check_unit_front
    check_no_status_recap
    check_dedupe_non_epic
    check_epic_carveout
    check_empty_block
    check_contract_consistency

    summary
}

main "$@"
