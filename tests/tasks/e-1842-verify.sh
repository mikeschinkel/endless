#!/usr/bin/env bash
#
# E-1842 verification — VISION.md is authored and published at the repo root.
#
# Run from inside the worktree (esu puts you there):
#   esu && ./tests/tasks/e-1842-verify.sh
#
# What it proves (VISION.md is a documentation deliverable, so the checks are
# on the published file's content):
#   1. VISION.md exists at the repo root.
#   2. It carries the agreed tagline "What we envision Endless will be".
#   3. It is prose, NOT a table or exists/planned split (that job belongs to
#      ROADMAP.md) — no markdown table rows, no "Exists today"/"Planned" columns.
#   4. It tells the arc: single-developer origin -> multi-developer substrate.
#   5. It develops the core tenets (steering fleet of sessions, the task loop
#      with worktree/sandbox/verification, collaboration over git, the ledger).
#   6. "Lowering the review burden" gets headline billing (its own section).
#   7. It cites no internal task IDs (E-NNNN / ED-NNNN) — public doc.
#   8. It contains no em-dash characters — public doc (AI-tell convention).
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

set -u

WT="$(git rev-parse --show-toplevel)"
VISION="${WT}/VISION.md"

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

die() { printf '%sSETUP ERROR:%s %s\n' "${RED}${BOLD}" "${RESET}" "$1" >&2; exit 2; }

# ─── setup: read the published file ─────────────────────────────────────────

section "Setup — locate VISION.md at the repo root"
[[ -f "${VISION}" ]] || die "VISION.md not found at repo root (${VISION})"
BODY="$(cat "${VISION}")" || die "could not read VISION.md"
printf '  found VISION.md (%d lines)\n' "$(wc -l < "${VISION}" | tr -d ' ')"

# case-insensitive substring test against the file body
has() { grep -qi -- "$1" "${VISION}"; }

# ─── existence + identity ───────────────────────────────────────────────────

section "Identity — heading and tagline"

if [[ "${BODY}" == *"# Vision"* ]]; then
    report_pass "top-level '# Vision' heading present"
else
    report_fail "H1 heading" "'# Vision' present" "not found"
fi

if has "What we envision Endless will be"; then
    report_pass "tagline 'What we envision Endless will be' present"
else
    report_fail "tagline" "'What we envision Endless will be'" "not found"
fi

# ─── prose, not a table (that is ROADMAP.md's job) ──────────────────────────

section "Form — prose narrative, not a table or exists/planned split"

if grep -qE '^\|' "${VISION}"; then
    report_fail "no markdown table" "no lines beginning with '|'" "found a table row"
else
    report_pass "no markdown table rows (prose, not a table)"
fi

if has "Exists today" || has "| Planned"; then
    report_fail "no exists/planned split" "no roadmap-style columns" "found column text"
else
    report_pass "no exists-today / planned split (roadmap's job, not vision's)"
fi

# A vision narrative should be substantial prose, not a stub.
WORDS="$(wc -w < "${VISION}" | tr -d ' ')"
if [[ "${WORDS}" -ge 400 ]]; then
    report_pass "substantial narrative (${WORDS} words)"
else
    report_fail "narrative length" ">= 400 words" "${WORDS} words"
fi

# ─── the arc: single-developer origin -> multi-developer future ─────────────

section "Substance — the single-dev to multi-dev arc"

if has "tmux" || has "concurrent"; then
    report_pass "names the single-developer / many-concurrent-sessions origin"
else
    report_fail "origin" "mentions tmux or concurrent sessions" "not found"
fi

if has "many developers" || has "multi-developer"; then
    report_pass "names the many-developers future"
else
    report_fail "future" "mentions many/multi developers" "not found"
fi

# ─── the tenets ─────────────────────────────────────────────────────────────

section "Substance — core tenets developed"

if has "orders of magnitude" || has "fleet of sessions" || has "steering"; then
    report_pass "tenet: human steers vastly more concurrent work"
else
    report_fail "steering tenet" "orders-of-magnitude / steering language" "not found"
fi

if has "worktree" && has "sandbox" && has "verification"; then
    report_pass "tenet: task loop (worktree + sandbox + verification)"
else
    report_fail "task-loop tenet" "worktree AND sandbox AND verification" "one or more missing"
fi

if has "git" && { has "no central" || has "not a server" || has "push"; }; then
    report_pass "tenet: collaboration via git, no central server"
else
    report_fail "git-collaboration tenet" "git + no-central-server language" "not found"
fi

if has "ledger" && { has "write-ahead" || has "JSONL" || has "append-only"; }; then
    report_pass "tenet: the ledger foundation (append-only WAL)"
else
    report_fail "ledger tenet" "ledger + WAL/JSONL/append-only" "not found"
fi

# ─── headline billing for lowering the review burden ────────────────────────

section "Emphasis — review-burden philosophy gets headline billing"

# It must be a dedicated section heading, not one clause buried in prose.
if grep -qiE '^##+ .*review burden' "${VISION}"; then
    report_pass "review burden has its own section heading"
else
    report_fail "review-burden section" "a '## ... review burden' heading" "not found"
fi

# ─── public-doc hygiene ─────────────────────────────────────────────────────

section "Hygiene — public-doc conventions"

if grep -qE '\b(E|ED)-[0-9]+\b' "${VISION}"; then
    report_fail "no internal task IDs" "no E-NNNN / ED-NNNN" "found an internal ID"
else
    report_pass "no internal task IDs (E-NNNN / ED-NNNN)"
fi

if grep -q '—' "${VISION}"; then
    report_fail "no em-dashes" "no '—' characters (public-doc AI-tell rule)" "found an em-dash"
else
    report_pass "no em-dash characters (public-doc convention)"
fi

# ─── summary ────────────────────────────────────────────────────────────────

printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
if [[ "${FAIL_COUNT}" -eq 0 ]]; then
    printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
    exit 0
fi
printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
    "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
printf '\n'
exit 1
