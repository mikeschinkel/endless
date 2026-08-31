#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1690 and records what was true when E-1690
# landed. Edit it only if you ARE E-1690. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1690 verification suite — the SINGLE entry point for verifying E-1690.
#
#   esu
#   endless task verify E-1690
#
# Self-contained. Verifies Part A of E-1690: the handoff templates'
# end-of-session `Final message` line no longer instructs a task-status recap
# and DOES instruct "report only what `endless session status` can't already
# show". Asserts the new wording is present and the old wording is gone across
# the whole internal/templatecmd/templates/handoff/*.md.tmpl set, confirms the
# orchestration guide carries the companion note, and runs the templatecmd Go
# tests to prove the edited templates still parse and render.
#
# Exit 0 on all-passed, 1 on any failure (with detail to diagnose).
#
# Part B (the `session status` dirty `◆` indicator) is an exploration deferred
# to its own task per the plan; when it lands, its rendering assertions get
# their own verify script.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ─── locate worktree ────────────────────────────────────────────────────────

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
WT_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
cd "${WT_ROOT}" || { printf 'ERROR: cannot cd to %s\n' "${WT_ROOT}" >&2; exit 1; }

HANDOFF_DIR="${WT_ROOT}/internal/templatecmd/templates/handoff"
GUIDE="${WT_ROOT}/docs/guide/orchestration.md"

# The full set of handoff templates the plan scopes the change to.
TEMPLATES=(task epic bug research brainstorm respawn)

# Wording the tightened templates MUST carry.
NEW_DEFER="report ONLY what \`endless session status\` can't already show"
NEW_NORECAP="Do NOT recap task status, phase, or relationships"
# Wording the old (over-expanding) `Final message` line carried — must be gone.
OLD_SUMMARY="1–2 sentence summary"

# ─── output ─────────────────────────────────────────────────────────────────

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
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}

# assert_file_contains DESC FILE PATTERN — pass if FILE exists and contains PATTERN.
assert_file_contains() {
    local desc="$1" file="$2" pattern="$3"
    if [[ ! -f "${file}" ]]; then report_fail "${desc}" "file exists: ${file}" "missing"; return; fi
    if grep -qF -- "${pattern}" "${file}"; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "contains: ${pattern}" "absent in ${file##*/}"
    fi
}

# assert_file_lacks DESC FILE PATTERN — pass if FILE exists and does NOT contain PATTERN.
assert_file_lacks() {
    local desc="$1" file="$2" pattern="$3"
    if [[ ! -f "${file}" ]]; then report_fail "${desc}" "file exists: ${file}" "missing"; return; fi
    if grep -qF -- "${pattern}" "${file}"; then
        report_fail "${desc}" "absent: ${pattern}" "still present in ${file##*/}"
    else
        report_pass "${desc}"
    fi
}

# assert_cmd DESC CMD [ARGS...] — pass if CMD exits 0.
assert_cmd() {
    local desc="$1"; shift
    local out rc
    out=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "exit 0" "exit=${rc} | $(printf '%s\n' "${out}" | tail -8 | tr '\n' '⏎')"
    fi
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── checks ─────────────────────────────────────────────────────────────────

section "Handoff templates — new 'defer to session status' wording present"
for t in "${TEMPLATES[@]}"; do
    f="${HANDOFF_DIR}/${t}.md.tmpl"
    assert_file_contains "${t}: instructs 'report only what session status can't show'" "${f}" "${NEW_DEFER}"
    assert_file_contains "${t}: instructs 'do NOT recap task status/phase/relationships'" "${f}" "${NEW_NORECAP}"
done

section "Handoff templates — old 'summary' recap wording gone"
for t in "${TEMPLATES[@]}"; do
    f="${HANDOFF_DIR}/${t}.md.tmpl"
    assert_file_lacks "${t}: no longer asks for a '1–2 sentence summary'" "${f}" "${OLD_SUMMARY}"
done

section "Orchestration guide — companion note present"
assert_file_contains "guide states the 'report only what session status can't show' discipline" "${GUIDE}" "what \`endless session status\` can't already show"

section "Go tests — templatecmd (edited templates still parse + render)"
assert_cmd "go test ./internal/templatecmd/" go test ./internal/templatecmd/ -count=1

summary
