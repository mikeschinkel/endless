#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1832 and records what was true when E-1832
# landed. Edit it only if you ARE E-1832. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1832 verification — README.md is refocused on onboarding a new human:
# what Endless is, how to install it, and how to start using it. The exhaustive
# CLI reference and how-to bulk are gone (moved to the guide), the guide/roadmap/
# vision are linked, and the canonical status-lifecycle mermaid invariant holds.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-1832
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

    section "B1 — README leads with the current tagline + elevator"
    assert_contains "uses the new tagline" "Manage 50+ AI tasks without becoming overwhelmed" "${body}"
    assert_not_contains "drops the old tagline" "Many projects, all at once." "${body}"
    assert_contains "elevator: many Claude Code sessions at once" "many Claude Code sessions at once" "${body}"
    assert_contains "elevator: per-task worktree + sandbox" "config/DB sandbox" "${body}"
    assert_contains "elevator: verification-script handoff" "verification script" "${body}"
    assert_contains "elevator: session monitor at a glance" "at a glance" "${body}"
    assert_contains "elevator: built on git + append-only ledger" "append-only ledger" "${body}"

    section "B2 — deprecated/de-emphasized features are gone"
    assert_not_contains "no 'task tree' framing" "task tree" "${body}"
    assert_not_contains "no 'first-class artifacts' decisions pitch" "first-class artifacts" "${body}"
    assert_not_contains "no deprecated 'endless serve'" "endless serve" "${body}"
    assert_not_contains "no web-dashboard pitch" "web dashboard" "${body}"

    section "B3 — Prerequisites present and each tool hyperlinked"
    assert_contains "Prerequisites section" "## Prerequisites" "${body}"
    assert_contains "prereq git hyperlinked" "[git](https://git-scm.com/)" "${body}"
    assert_contains "prereq tmux hyperlinked" "[tmux](https://github.com/tmux/tmux)" "${body}"
    assert_contains "prereq just hyperlinked" "[just](https://github.com/casey/just)" "${body}"
    assert_contains "prereq Go hyperlinked" "[Go](https://go.dev/)" "${body}"
    assert_contains "prereq Python hyperlinked" "[Python](https://www.python.org/)" "${body}"
    assert_contains "prereq uv hyperlinked" "[uv](https://github.com/astral-sh/uv)" "${body}"
    assert_contains "prereq sqlite3 hyperlinked" "[sqlite3](https://www.sqlite.org/)" "${body}"
    assert_contains "prereq jq hyperlinked" "[jq](https://jqlang.org/)" "${body}"

    section "B4 — Install + Getting started; CLI-reference bulk moved out"
    assert_contains "Install section" "## Install" "${body}"
    assert_contains "install command" "just install" "${body}"
    assert_contains "Getting started section" "## Getting started" "${body}"
    assert_contains "first-use: register a project" "project register" "${body}"
    assert_contains "first-use: add a task" "task add" "${body}"
    assert_contains "first-use: task list" "endless task list" "${body}"
    assert_contains "first-use: task recent" "endless task recent" "${body}"
    assert_contains "first-use: task show <id> (not the broken --all form)" "endless task show E-123" "${body}"
    assert_not_contains "no broken 'task show --all' example" "task show --all" "${body}"
    assert_contains "first-use: spawn a task" "task spawn" "${body}"
    assert_not_contains "no '## CLI Reference' heading" "## CLI Reference" "${body}"
    assert_not_contains "no '### Project Management' heading" "### Project Management" "${body}"
    assert_not_contains "no '### Task Management' heading" "### Task Management" "${body}"
    assert_not_contains "no '## Database' schema dump" "## Database" "${body}"

    section "B5 — sessions + tmux layout + CLI self-help documented"
    assert_contains "documents session status (one-shot)" "endless session status" "${body}"
    assert_contains "documents session monitor (live)" "endless session monitor" "${body}"
    assert_contains "documents the working layout" "Working layout" "${body}"
    assert_contains "documents the --help discovery pattern" "endless task --help" "${body}"

    section "B6 — guide is NOT routed to humans; roadmap/vision linked"
    assert_not_contains "no human-facing 'endless guide' routing" "endless guide" "${body}"
    assert_contains "links to ROADMAP.md" "ROADMAP.md" "${body}"
    assert_contains "links to VISION.md" "VISION.md" "${body}"

    section "B7 — task lifecycle documents the new 'untriaged' entry status"
    assert_contains "keeps a task-lifecycle section" "## Task lifecycle" "${body}"
    assert_contains "prose introduces 'untriaged'" "untriaged" "${body}"
    assert_contains "prose explains the approve gate" "approved to implement" "${body}"
    # The canonical mermaid must carry the new upstream status (layer A already
    # asserts byte-identity; this pins the actual content the invariant guards).
    assert_contains "canonical block: [*] enters at untriaged" "[*] --> untriaged" "${body}"
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
