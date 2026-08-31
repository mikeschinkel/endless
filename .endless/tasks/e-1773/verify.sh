#!/usr/bin/env bash
#
# E-1773 verification — the handoff spawn-prompt templates now carry two
# additions to the reporting discipline E-1759 established:
#   1. Reporting is routed through the `endless task report` command (run it and
#      relay its output verbatim), not composed as freeform prose. The anomaly
#      `--json` shape is discovered via `endless task report --help`, not
#      inlined here.
#   2. A `FULL STATUS` bypass keyword: when the user types it, the agent answers
#      unconstrained for that one response (per-response, not a sticky mode).
#
# Both live in the shared partial handoff/_close.tmpl (task/bug/epic/research/
# brainstorm pull it in); respawn keeps its own shape and inlines the same two
# sentences. This script asserts the additions render for every handoff type, in
# foreground and .bg variants, WITHOUT displacing the E-1759 discipline.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-1773
#
# Strategy (mirrors .endless/tasks/e-1759/verify.sh): render each handoff type
# through the worktree-built `bin/endless-go template render` against a
# throwaway self_dev project fixture, and assert on the rendered text. Nothing
# touches the real endless repo, ledger, or this worktree's branch — teardown is
# `rm -rf`.
#
# What it checks:
#   0. The extended templatecmd render unit test (TestRender_HandoffClose_
#      ExceptionRule now pins the two new lines). Self-contained; the
#      project-wide `just test` / `go test ./...` regression is a separate
#      pre-land concern.
#   1. task/bug/epic/research/brainstorm — every rendered handoff routes
#      reporting through `endless task report` and carries `FULL STATUS`.
#   2. respawn — same two additions, via its own inline shape (no partial).
#   3. Regression: the E-1759 discipline is intact — `endless worktree check`
#      and "do NOT confirm the negative" survive; bg carries the background-agent
#      note; the retired enumeration phrasing stays gone.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 setup error.

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
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'
    BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

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

# ─── assertions ─────────────────────────────────────────────────────────────

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

# ─── setup ──────────────────────────────────────────────────────────────────

REPO_ROOT=""
WORK=""
PROJ=""
GO=""

cleanup() { [[ -n "${WORK}" && -d "${WORK}" ]] && rm -rf "${WORK}"; }

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    GO="${REPO_ROOT}/bin/endless-go"
    [[ -x "${GO}" ]] || {
        printf 'ERROR: %s missing — run `just build`\n' "${GO}" >&2; exit 2; }

    WORK=$(mktemp -d)
    trap cleanup EXIT

    # A throwaway self_dev project: render reads the embedded (candidate)
    # templates and skips materialization, so no files are written and no git
    # repo is required.
    PROJ="${WORK}/proj"
    mkdir -p "${PROJ}/.endless"
    printf '{"self_dev": true}\n' > "${PROJ}/.endless/config.json"
}

# render TYPE BG(0|1) — render handoff/<TYPE> to stdout with a full var map.
render() {
    local typ="$1" bg="$2" bgval="false"
    [[ "${bg}" == "1" ]] && bgval="true"
    local vars='{"spawned_id":9999,"label_prefix":"E-8888/E-9999","title":"Test task","spawner_task":7777,"return_anchor":"%9","worktree_path":"/tmp/wt/e-9999","branch":"task/9999-test","child_count":0,"children_state":"2 ready","bg":BGVAL}'
    vars="${vars/BGVAL/${bgval}}"
    ( cd "${PROJ}" && printf '%s' "${vars}" | "${GO}" template render "handoff/${typ}" 2>/dev/null )
}

# ─── checks ─────────────────────────────────────────────────────────────────

# check_go_unit — run the templatecmd render unit test so this script is the
# single self-contained verification of the task.
check_go_unit() {
    section "0 — Go render unit test (FULL STATUS + task report pinned)"
    local out rc
    out=$(cd "${REPO_ROOT}" && go test ./internal/templatecmd/ \
        -run 'TestRender_HandoffClose_ExceptionRule' 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "templatecmd render unit test passes"
    else report_fail "templatecmd render unit test passes" "go test exit 0" "exit ${rc}
${out}"; fi
}

# Assert the two E-1773 additions plus the retained E-1759 discipline on one
# rendered handoff. LABEL describes the type/variant.
assert_additions() {
    local label="$1" out="$2" bg="$3"
    assert_contains "${label} routes reporting through 'endless task report'" \
        "${out}" "endless task report"
    assert_contains "${label} carries the FULL STATUS bypass keyword" \
        "${out}" "FULL STATUS"
    # E-1759 discipline must survive the additions.
    assert_contains "${label} keeps the worktree-check line" \
        "${out}" "endless worktree check"
    assert_contains "${label} keeps 'do NOT confirm the negative'" \
        "${out}" "do NOT confirm the negative"
    if [[ "${bg}" == "1" ]]; then
        assert_contains "${label} keeps the background-agent note" \
            "${out}" "You're a background agent"
    fi
}

check_partial_types() {
    local typ bg out label
    for typ in task bug epic research brainstorm; do
        section "handoff/${typ} — FULL STATUS + report routing (via shared partial)"
        for bg in 0 1; do
            label="${typ} (non-bg)"; [[ "${bg}" == "1" ]] && label="${typ} (bg)"
            out=$(render "${typ}" "${bg}")
            if [[ -z "${out}" ]]; then
                report_fail "${label} renders" "non-empty output" "(empty)"
                continue
            fi
            assert_additions "${label}" "${out}" "${bg}"
        done
    done
}

check_respawn() {
    section "handoff/respawn — FULL STATUS + report routing (own inline shape)"
    local vars out
    vars='{"spawned_id":9999,"label_prefix":"E-9999","title":"T","worktree_path":"/tmp/wt","branch":"b","restore_case":"reused","child_count":0}'
    out=$( cd "${PROJ}" && printf '%s' "${vars}" | "${GO}" template render handoff/respawn 2>/dev/null )
    if [[ -z "${out}" ]]; then
        report_fail "respawn renders" "non-empty output" "(empty)"; return
    fi
    assert_additions "respawn" "${out}" "0"
    # respawn keeps its own shape: no shared partial reference leaks through.
    assert_not_contains "respawn does not leak an unresolved partial ref" \
        "${out}" "handoff_close"
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup

    printf '%sE-1773 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  go:      %s\n' "${GO}"
    printf '  env:     isolated self_dev fixture (%s)\n' "${WORK}"

    check_go_unit
    check_partial_types
    check_respawn

    summary
}

main "$@"
