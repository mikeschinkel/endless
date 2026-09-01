#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1764 and records what was true when E-1764
# landed. Edit it only if you ARE E-1764. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1764 verification — two shipped, user-facing docs no longer hardcode a
# personal name where they should generically reference "the user".
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-1764
#
# This is a deterministic, content-only fix, so verification is a pure static
# check of the checked-out docs — no DB, no sandbox, no CLI. It asserts against
# the working-tree files at the repo root (git rev-parse), so it reflects
# exactly what would be committed/landed.
#
# What it checks:
#   1. Neither target doc contains the personal name "Mike" anymore.
#   2. The three reworded lines now read "the user", verbatim:
#        docs/guide/sessions.md          — "waiting on the user's review"
#        docs/TASK_RECORDING_PROMPT.md   — "until the user notices and asks"
#        docs/TASK_RECORDING_PROMPT.md   — "Do this without the user having to
#                                           remind, prompt, or catch omissions"
#   3. Guard against over-reach: intentional "Mike" references in OTHER docs
#      (the talk / lightning-talk brief / design brief, where Mike is named as
#      speaker/author) are left untouched.
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

# assert_absent DESC FILE NEEDLE
#   Pass if FILE does NOT contain the fixed string NEEDLE.
assert_absent() {
    local desc="$1" file="$2" needle="$3"
    if [[ ! -f "${file}" ]]; then
        report_fail "${desc}" "file exists: ${file}" "MISSING"; return
    fi
    local hits
    hits=$(grep -Fn -- "${needle}" "${file}" || true)
    if [[ -z "${hits}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "no line contains: ${needle}" "${hits}"
}

# assert_present DESC FILE NEEDLE
#   Pass if FILE contains the fixed string NEEDLE at least once.
assert_present() {
    local desc="$1" file="$2" needle="$3"
    if [[ ! -f "${file}" ]]; then
        report_fail "${desc}" "file exists: ${file}" "MISSING"; return
    fi
    if grep -Fq -- "${needle}" "${file}"; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "a line contains: ${needle}" "(not found in ${file})"
}

# ─── checks ─────────────────────────────────────────────────────────────────

SESSIONS="docs/guide/sessions.md"
PROMPT="docs/TASK_RECORDING_PROMPT.md"

check_name_removed() {
    section "1 — personal name removed from the two target docs"
    assert_absent "sessions.md no longer names 'Mike'" "${SESSIONS}" "Mike"
    assert_absent "TASK_RECORDING_PROMPT.md no longer names 'Mike'" "${PROMPT}" "Mike"
}

check_reworded() {
    section "2 — reworded lines reference 'the user' verbatim"
    assert_present "sessions.md example task waits on the user's review" \
        "${SESSIONS}" "waiting on the user's review"
    assert_present "PROMPT: '...until the user notices and asks'" \
        "${PROMPT}" "until the user notices and asks"
    assert_present "PROMPT: '...without the user having to remind...'" \
        "${PROMPT}" "Do this without the user having to remind, prompt, or catch omissions"
}

check_no_overreach() {
    section "3 — intentional 'Mike' references in OTHER docs left untouched"
    # These name Mike as speaker/author, not as a stand-in for "the user";
    # they must survive the fix. If any is gone, the change over-reached.
    assert_present "talk still lists speaker 'Mike Schinkel'" \
        "docs/talk-2026-04-21-manage-10+-projects-without-getting-overwhelmed.md" \
        "Mike Schinkel"
    assert_present "lightning-talk brief still has 'Working preferences (Mike)'" \
        "docs/brief-2026-04-19-ai-tinkerers-lightning-talk-brief.md" \
        "Working preferences (Mike)"
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

    printf '%sE-1764 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  mode:    static content check (no DB / no CLI)\n'

    check_name_removed
    check_reworded
    check_no_overreach

    summary
}

main "$@"
