#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1759 and records what was true when E-1759
# landed. Edit it only if you ARE E-1759. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1759 verification — the handoff spawn-prompt templates close with a single
# exception rule (invoke `endless worktree check`) instead of enumerating report
# categories, and the generic tail is factored into one shared partial
# (handoff/_close.tmpl). Naming empty categories invited "none" ceremony; the
# rewrite replaces that with: relay whatever `worktree check` prints, never
# confirm the negative.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-1759
#
# Strategy: render each handoff type in bg and non-bg mode through the
# worktree-built `bin/endless-go template render` (self-dev → renders from the
# embedded candidate templates, no materialization) against a throwaway self_dev
# project fixture, and assert on the rendered text. Nothing touches the real
# endless repo, ledger, or this worktree's branch — teardown is `rm -rf`.
#
# What it checks:
#   0. The templatecmd render unit tests (partial resolves; exception-rule
#      wording; bg vs non-bg return line). Self-contained; project-wide
#      `just test` / `go test ./...` regression is a separate pre-land concern.
#   1. task/bug/epic/research/brainstorm — every rendered handoff invokes
#      `endless worktree check` and forbids confirming the negative.
#   2. The retired enumeration phrases ("dangling tags", "landed-vs-worktree
#      delta") are gone from every rendered handoff.
#   3. The tmux return line appears in non-bg output and is absent in bg output.
#   4. The type-specific deliverable prefix survives the refactor.
#   5. respawn's inline bullet carries the same exception rule (and keeps its own
#      shape — no partial, status wording untouched).
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

# check_go_unit — run the templatecmd render unit tests so this script is the
# single self-contained verification of the task.
check_go_unit() {
    section "0 — Go render unit tests (partial + exception rule)"
    local out rc
    out=$(cd "${REPO_ROOT}" && go test ./internal/templatecmd/ \
        -run 'TestRender_HandoffClose|TestRender_FullVars|TestRender_MissingVar' 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "templatecmd render unit tests pass"
    else report_fail "templatecmd render unit tests pass" "go test exit 0" "exit ${rc}
${out}"; fi
}

# Deliverable prefix each type must keep inline before the shared tail.
prefix_for() {
    case "$1" in
        task|bug)  printf 'lead with the how-to-test' ;;
        epic)      printf 'lead with the state of the children' ;;
        research)  printf 'say where the findings live' ;;
        brainstorm) printf 'say where the synthesis lives' ;;
    esac
}

check_types() {
    local typ bg out label
    for typ in task bug epic research brainstorm; do
        section "handoff/${typ} — exception rule, no enumeration, return-line split"
        for bg in 0 1; do
            label="bg"; [[ "${bg}" == "0" ]] && label="non-bg"
            out=$(render "${typ}" "${bg}")
            if [[ -z "${out}" ]]; then
                report_fail "${typ} (${label}) renders" "non-empty output" "(empty)"
                continue
            fi
            assert_contains "${typ} (${label}) invokes worktree check" \
                "${out}" "endless worktree check"
            assert_contains "${typ} (${label}) forbids confirming the negative" \
                "${out}" "do NOT confirm the negative"
            assert_contains "${typ} (${label}) keeps deliverable prefix" \
                "${out}" "$(prefix_for "${typ}")"
            assert_not_contains "${typ} (${label}) drops 'dangling tags'" \
                "${out}" "dangling tags"
            assert_not_contains "${typ} (${label}) drops 'landed-vs-worktree delta'" \
                "${out}" "landed-vs-worktree delta"
            if [[ "${bg}" == "0" ]]; then
                assert_contains "${typ} (non-bg) shows the tmux return line" \
                    "${out}" "tmux move-window -t archive:"
            else
                assert_not_contains "${typ} (bg) omits the tmux return line" \
                    "${out}" "tmux move-window -t archive:"
                assert_contains "${typ} (bg) ends with background-agent note" \
                    "${out}" "You're a background agent"
            fi
        done
    done
}

check_respawn() {
    section "handoff/respawn — inline exception rule (own shape, no partial)"
    local vars out
    vars='{"spawned_id":9999,"label_prefix":"E-9999","title":"T","worktree_path":"/tmp/wt","branch":"b","restore_case":"reused","child_count":0}'
    out=$( cd "${PROJ}" && printf '%s' "${vars}" | "${GO}" template render handoff/respawn 2>/dev/null )
    if [[ -z "${out}" ]]; then
        report_fail "respawn renders" "non-empty output" "(empty)"; return
    fi
    assert_contains "respawn invokes worktree check" "${out}" "endless worktree check"
    assert_contains "respawn forbids confirming the negative" "${out}" "do NOT confirm the negative"
    assert_not_contains "respawn drops 'dangling tags'" "${out}" "dangling tags"
    assert_not_contains "respawn drops 'landed-vs-worktree delta'" "${out}" "landed-vs-worktree delta"
    # respawn keeps its own shape: no shared partial reference leaks through.
    assert_not_contains "respawn does not leak an unresolved partial ref" "${out}" "handoff_close"
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup

    printf '%sE-1759 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  go:      %s\n' "${GO}"
    printf '  env:     isolated self_dev fixture (%s)\n' "${WORK}"

    check_go_unit
    check_types
    check_respawn

    summary
}

main "$@"
