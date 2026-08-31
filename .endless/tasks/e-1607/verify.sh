#!/usr/bin/env bash
#
# E-1607 verification script — the single verification command for this task.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1607
#
# Asserts the two deliverables of E-1607:
#   1. A "Verification suites & the one-command handoff" section in the
#      orchestration guide (folded in, NOT a new top-level guide slug), plus a
#      cross-reference topic row pointing at orchestration.
#   2. Final-message discipline in all six spawn handoff templates: the three
#      verify-handoffs (task/bug/respawn) carry the one-command contract; the
#      three information-deliverable templates (research/brainstorm/epic) carry
#      the anti-checklist prohibition; none use the stale `--status verify`.
#
# Guide content is asserted against the worktree's own files (the global
# `endless guide` reads the main checkout's docs, not this branch's). Templates
# are rendered via the worktree-built endless-go so the embedded copy under test
# is exactly this branch's source. Also runs the relevant Go unit tests as a
# fold-in fail-fast regression.
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure.
#
# Interim ad-hoc location .endless/tasks/ (matches the prototype convention);
# migrates to .endless/tasks/<id>/ once the manifest runner lands.

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

REPO_ROOT=""
BIN=""
RENDER_VARS='{"spawned_id":"1607","spawner_task":"441","title":"Demo task","label_prefix":"E-1607","return_anchor":"%441","worktree_path":"/tmp/wt","branch":"task/1607-x","bg":false,"child_count":0,"restore_case":"reused","children_state":"2 unplanned"}'

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

# assert_stdin_has DESC PATTERN  — the piped stdin contains fixed-string PATTERN.
assert_stdin_has() {
    local desc="$1"; local pat="$2"
    local out; out=$(cat)
    if [[ "${out}" == *"${pat}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output contains: ${pat}" "${out}"
}

# assert_stdin_lacks DESC PATTERN  — the piped stdin does NOT contain PATTERN.
assert_stdin_lacks() {
    local desc="$1"; local pat="$2"
    local out; out=$(cat)
    if [[ "${out}" != *"${pat}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output lacks: ${pat}" "present"
}

# render TYPE  — render a handoff template via the worktree binary.
render() { echo "${RENDER_VARS}" | "${BIN}" template render "handoff/$1" 2>&1; }

# ─── checks ─────────────────────────────────────────────────────────────────

check_guide_section() {
    section "Guide — Verification section folded into orchestration (no new slug)"
    local guide="${REPO_ROOT}/docs/guide/orchestration.md"
    assert_file_has "orchestration.md has the Verification section heading" \
        "${guide}" "## Verification suites & the one-command handoff"
    assert_file_has "documents the verify.toml manifest" "${guide}" "verify.toml"
    assert_file_has "shows the [[check]] list" "${guide}" "[[check]]"
    assert_file_has "names the settled runner command" "${guide}" "endless task verify"
    assert_file_has "states the one-command contract" "${guide}" "exactly ONE"
    assert_file_has "describes the per-task suite" "${guide}" "One suite per task"

    # No new top-level slug: exactly the pre-existing 5 sections, no verification.md.
    local slugs
    slugs=$(cd "${REPO_ROOT}/docs/guide" && ls *.md | grep -v '^index\.md$' | wc -l | tr -d ' ')
    if [[ "${slugs}" -eq 5 ]]; then
        report_pass "guide still has exactly 5 section slugs (no reflexive new section)"
    else
        report_fail "guide still has exactly 5 section slugs" "5" "${slugs}"
    fi
    if [[ ! -e "${REPO_ROOT}/docs/guide/verification.md" ]]; then
        report_pass "no standalone verification.md slug was minted"
    else
        report_fail "no standalone verification.md slug was minted" "absent" "present"
    fi
}

check_cross_reference() {
    section "Cross-reference — verification topic maps to orchestration"
    local topics="${REPO_ROOT}/docs/guide/help/_topics.md"
    local index="${REPO_ROOT}/docs/guide/index.md"
    assert_file_has "_topics.md declares the verification topic" \
        "${topics}" "topic: per-task verification suite"
    assert_file_has "generated index block carries the topic row -> orchestration" \
        "${index}" "| per-task verification suite | orchestration |"
}

check_verify_templates() {
    section "Verify handoffs (task/bug/respawn) — one-command contract present"
    local t
    for t in task bug respawn; do
        render "${t}" | assert_stdin_has "${t}: hands over exactly ONE command" "exactly ONE"
        render "${t}" | assert_stdin_has "${t}: points at the guide section" "endless guide orchestration"
        render "${t}" | assert_stdin_has "${t}: forbids a manual checklist" "Do NOT enumerate a manual checklist"
    done
}

check_nonverify_templates() {
    section "Info deliverables (research/brainstorm/epic) — anti-variorum present"
    local t
    for t in research brainstorm epic; do
        render "${t}" | assert_stdin_has "${t}: states there is nothing to verify" "nothing to verify"
        render "${t}" | assert_stdin_has "${t}: forbids inventing a how-to-test" "Do NOT invent"
        render "${t}" | assert_stdin_lacks "${t}: does not leak the one-command contract" "exactly ONE"
    done
}

check_no_stale_status() {
    section "Regression — no template uses the stale '--status verify'"
    local hits
    hits=$(grep -rl -- '--status verify' "${REPO_ROOT}/internal/templatecmd/templates/handoff/" 2>/dev/null)
    if [[ -z "${hits}" ]]; then
        report_pass "no handoff template contains '--status verify' (canonical is 'unverified')"
    else
        report_fail "no handoff template contains '--status verify'" "none" "${hits}"
    fi
}

check_go_unit_tests() {
    section "Fold-in regression — templatecmd + verify Go unit tests"
    assert_succeeds "go test ./internal/templatecmd/... ./internal/verify/..." \
        go test ./internal/templatecmd/... ./internal/verify/...
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2; exit 2
    fi
    cd "${REPO_ROOT}" || exit 2

    printf '%sE-1607 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  repo:   %s\n' "${REPO_ROOT}"

    section "Build — worktree endless-go (candidate templates under test)"
    assert_succeeds "go build -o bin/endless-go ./cmd/endless-go" \
        go build -o bin/endless-go ./cmd/endless-go
    BIN="${REPO_ROOT}/bin/endless-go"
    if [[ ! -x "${BIN}" ]]; then
        printf 'ERROR: %s not built\n' "${BIN}" >&2; exit 2
    fi

    check_guide_section
    check_cross_reference
    check_verify_templates
    check_nonverify_templates
    check_no_stale_status
    check_go_unit_tests

    summary
}

main "$@"
