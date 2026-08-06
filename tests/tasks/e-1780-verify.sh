#!/usr/bin/env bash
#
# E-1780 verification script — the single verification command for this task.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1780-verify.sh
#
# Asserts the deliverables of E-1780 — documenting the session command family:
#   1. docs/guide/sessions.md gains agent-facing entries for `status`, `show`,
#      and `list`; carries the retitled `## Reading snapshots` heading; carries
#      a single `appendix-a` pointer; and does NOT inline the user-facing
#      commands (no `session goto`/`monitor`/... how-to in sessions.md).
#   2. docs/guide/appendix-a.md exists, frames itself as "commands a human
#      runs," and documents each of the eight user-facing session commands
#      (nine until E-1906 retired `session recap`).
#   3. docs/guide/index.md's `## Sections` list names `appendix-a`.
#   4. docs/guide/ yields exactly the 5 pillar sections + the appendix (no
#      accidental extra section), and `just guide-check` is green.
#
# Guide content is asserted against the worktree's own files (the global
# `endless guide` reads the main checkout's docs, not this branch's). The
# guide-check gate is run as a fold-in fail-fast regression.
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure.
#
# Interim ad-hoc location tests/tasks/ (matches the prototype convention);
# migrates to .endless/tasks/<id>/ once the manifest runner lands.

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

REPO_ROOT=""

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

# assert_succeeds DESC CMD [ARGS...]  — pass if CMD exits 0.
assert_succeeds() {
    local desc="$1"; shift
    local out; out=$("$@" 2>&1); local rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | ${out}"
}

# assert_file_has DESC FILE PATTERN  — FILE contains fixed-string PATTERN.
assert_file_has() {
    local desc="$1"; local file="$2"; local pat="$3"
    if grep -qF -- "${pat}" "${file}" 2>/dev/null; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${file} contains: ${pat}" "absent"
}

# assert_file_lacks DESC FILE PATTERN  — FILE does NOT contain fixed-string PATTERN.
assert_file_lacks() {
    local desc="$1"; local file="$2"; local pat="$3"
    if ! grep -qF -- "${pat}" "${file}" 2>/dev/null; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${file} lacks: ${pat}" "present"
}

# ─── checks ─────────────────────────────────────────────────────────────────

check_sessions_agent_facing() {
    section "sessions.md — agent-facing entries + retitle + single pointer"
    local f="${REPO_ROOT}/docs/guide/sessions.md"

    assert_file_has "documents \`session status\`" "${f}" "endless session status"
    assert_file_has "documents \`session show\`" "${f}" "endless session show"
    assert_file_has "documents \`session list\`" "${f}" "endless session list"

    assert_file_has "carries the retitled \`## Reading snapshots\` heading" \
        "${f}" "## Reading snapshots"
    assert_file_lacks "drops the old \`Reading status\` heading" \
        "${f}" "## Reading status"

    assert_file_has "carries the single appendix-a pointer" "${f}" "endless guide appendix-a"

    # Must NOT inline the user-facing commands — those live in the appendix.
    local cmd
    # `recap` was in this list until E-1906 retired the session-recap machinery.
    for cmd in goto back trail monitor history search hide unhide; do
        assert_file_lacks "does not inline user-facing \`session ${cmd}\`" \
            "${f}" "session ${cmd}"
    done
}

check_appendix() {
    section "appendix-a.md — user-facing framing + eight commands"
    local f="${REPO_ROOT}/docs/guide/appendix-a.md"

    if [[ -f "${f}" ]]; then
        report_pass "appendix-a.md exists"
    else
        report_fail "appendix-a.md exists" "present" "absent"
        return
    fi

    assert_file_has "titled as Appendix A — User-focused commands" \
        "${f}" "Appendix A — User-focused commands"
    assert_file_has "frames these as commands a human runs" "${f}" "a **human** runs"

    local cmd
    # `recap` was in this list until E-1906 retired the session-recap machinery.
    for cmd in goto back trail monitor history search hide unhide; do
        assert_file_has "documents \`session ${cmd}\`" "${f}" "session ${cmd}"
    done
}

check_index() {
    section "index.md — Sections list names the appendix"
    local f="${REPO_ROOT}/docs/guide/index.md"
    assert_file_has "\`## Sections\` list names appendix-a" "${f}" "**appendix-a**"
    assert_file_has "labels appendix-a as a user-facing appendix" \
        "${f}" "user-facing appendix"
}

check_section_inventory() {
    section "Guide inventory — 5 pillars + 1 appendix, no accidental section"
    local count
    count=$(cd "${REPO_ROOT}/docs/guide" && ls *.md | grep -v '^index\.md$' | wc -l | tr -d ' ')
    if [[ "${count}" -eq 6 ]]; then
        report_pass "docs/guide has exactly 6 section files (5 pillars + appendix-a)"
    else
        report_fail "docs/guide has exactly 6 section files" "6" "${count}"
    fi
}

check_guide_check_gate() {
    section "Fold-in regression — just guide-check is green"
    assert_succeeds "just guide-check passes" just guide-check
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2; exit 2
    fi
    cd "${REPO_ROOT}" || exit 2

    printf '%sE-1780 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  repo:   %s\n' "${REPO_ROOT}"

    check_sessions_agent_facing
    check_appendix
    check_index
    check_section_inventory
    check_guide_check_gate

    summary
}

main "$@"
