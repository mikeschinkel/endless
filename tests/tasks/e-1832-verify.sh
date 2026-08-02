#!/usr/bin/env bash
#
# E-1832 verification — README.md is refocused on onboarding a new human:
# what Endless is, how to install it, and how to start using it. The exhaustive
# CLI reference and how-to bulk are gone (moved to the guide), the guide/roadmap/
# vision are linked, and the canonical status-lifecycle mermaid invariant holds.
#
# Run from anywhere inside the worktree:
#   esu
#   ./tests/tasks/e-1832-verify.sh
#
# This is a documentation-content task, so the checks read the worktree's own
# tracked files (README.md, CLAUDE.md, docs/) directly — no DB, ledger, or
# isolated env is involved. Nothing is mutated; there is nothing to tear down.
#
# Layers:
#   A. FAIL-FAST invariant: the canonical status-lifecycle mermaid block is
#      byte-identical in README.md, CLAUDE.md, and docs/guide/index.md (the same
#      assertion e-1648-verify.sh guards). If README's rewrite broke it, stop
#      before the content checks.
#   B. Content: README leads with what/install/getting-started, drops the CLI
#      reference bulk, and links out to the guide, ROADMAP, and VISION.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 setup error.

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

# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "content contains: $2" "$3"; fi
}
# assert_not_contains DESC NEEDLE HAYSTACK
assert_not_contains() {
    if [[ "$3" != *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "content must NOT contain: $2" "(present)"; fi
}

# ─── setup ──────────────────────────────────────────────────────────────────

WT=""
README=""

setup() {
    WT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${WT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    README="${WT}/README.md"
    [[ -f "${README}" ]] || { printf 'ERROR: README.md missing at %s\n' "${README}" >&2; exit 2; }
    for f in docs/status-lifecycle.mmd CLAUDE.md docs/guide/index.md; do
        [[ -f "${WT}/${f}" ]] || { printf 'ERROR: %s missing\n' "${f}" >&2; exit 2; }
    done
}

# ─── layer A — canonical mermaid invariant (FAIL-FAST) ───────────────────────

# Extract the canonical mermaid body from a file — identical logic to
# e-1648-verify.sh's test_docs_sync, kept local so this script is standalone.
extract_canon() {
    awk '
        /^<!-- BEGIN canonical:docs\/status-lifecycle.mmd/ {inblk=1; next}
        /^<!-- END canonical:docs\/status-lifecycle.mmd/   {inblk=0}
        inblk && /^```mermaid$/ {infence=1; next}
        inblk && infence && /^```$/ {infence=0; next}
        inblk && infence {print}
    ' "$1"
}

run_invariant_layer() {
    section "A — canonical status-lifecycle mermaid is byte-identical (FAIL-FAST)"
    local canon="${WT}/docs/status-lifecycle.mmd" d ok=1
    for d in README.md CLAUDE.md docs/guide/index.md; do
        if diff -q <(extract_canon "${WT}/${d}") "${canon}" >/dev/null 2>&1; then
            report_pass "${d} embeds the canonical block byte-for-byte"
        else
            report_fail "${d} embeds the canonical block byte-for-byte" \
                "identical to docs/status-lifecycle.mmd" \
                "$(diff <(extract_canon "${WT}/${d}") "${canon}" | head -5)"
            ok=0
        fi
    done
    if [[ "${ok}" -ne 1 ]]; then
        summary
        exit 1
    fi
}

# ─── layer B — README content ────────────────────────────────────────────────

check_readme_content() {
    local body
    body=$(cat "${README}")

    section "B1 — README leads with what Endless is"
    assert_contains "keeps the project tagline" "Many projects, all at once." "${body}"
    assert_contains "elevator: task tree" "task tree" "${body}"
    assert_contains "elevator: decisions as first-class artifacts" "first-class artifacts" "${body}"
    assert_contains "elevator: per-task worktrees" "Per-task git worktrees" "${body}"
    assert_contains "elevator: session tracking" "Session tracking" "${body}"
    assert_contains "elevator: web dashboard" "web dashboard" "${body}"

    section "B2 — Install + Getting started sections present"
    assert_contains "Install section" "## Install" "${body}"
    assert_contains "install command" "just install" "${body}"
    assert_contains "Getting started section" "## Getting started" "${body}"
    assert_contains "first-use: register a project" "project register" "${body}"
    assert_contains "first-use: add a task" "task add" "${body}"

    section "B3 — CLI-reference bulk moved out"
    assert_not_contains "no '## CLI Reference' heading" "## CLI Reference" "${body}"
    assert_not_contains "no '### Project Management' heading" "### Project Management" "${body}"
    assert_not_contains "no '### Task Management' heading" "### Task Management" "${body}"
    assert_not_contains "no '### Documents & Notes' heading" "### Documents & Notes" "${body}"
    assert_not_contains "no '### Web Dashboard' heading" "### Web Dashboard" "${body}"
    assert_not_contains "no '### Hooks & Setup' heading" "### Hooks & Setup" "${body}"
    assert_not_contains "no '## Database' schema dump" "## Database" "${body}"

    section "B4 — links out to guide, roadmap, vision"
    assert_contains "points to the guide" "endless guide" "${body}"
    assert_contains "notes the guide targets an AI/Claude session" "AI coding session" "${body}"
    assert_contains "links to ROADMAP.md" "ROADMAP.md" "${body}"
    assert_contains "links to VISION.md" "VISION.md" "${body}"

    section "B5 — task lifecycle stays discoverable in README"
    assert_contains "keeps a task-lifecycle section" "## Task lifecycle" "${body}"
    assert_contains "lifecycle prose explains the approve gate" "approved to implement" "${body}"
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup

    printf '%sE-1832 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"
    printf '  target:   README.md (documentation-content task)\n'

    run_invariant_layer
    check_readme_content

    summary
}

main "$@"
