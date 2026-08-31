#!/usr/bin/env bash
#
# E-1765 verification script — the `⚑ review` action for `submitted` tasks.
#
# E-1648 added `submitted` (spec-complete, awaiting approval) but `session
# status` folded it into actDo (▶ do), so a submitted task LOOKED spawnable
# even though the claim gate refuses it. E-1765 gives `submitted` its own
# action — actReview (⚑ review) — so the render matches the gate:
#   classify(submitted) → actReview (was actDo)
# The ⚑ glyph is single-width so the width-aware table stays aligned, and
# submitted stays out of the --tree do/plan backlog (not spawnable).
#
# Run from anywhere inside the worktree:
#   endless task verify E-1765
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Isolation: this script exercises the classifier via `go test` against the
# worktree source plus greps of the worktree source, so no DB/ledger/cache is
# touched. It mirrors the e-1648-verify.sh shape (section/pass/fail/summary
# helpers) minus the DB fixture, which the classifier change does not need.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

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

section()     { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}
summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'; return 1
}

# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output contains: $2" "$3"; fi
}
# assert_not_contains DESC NEEDLE HAYSTACK
assert_not_contains() {
    if [[ "$3" != *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output must NOT contain: $2" "$3"; fi
}

WT=""

# ─── section A: classifier unit tests ────────────────────────────────────────

test_unit() {
    section "A. go test ./internal/sessionstatuscmd/ (submitted → actReview, ⚑ width)"
    local out rc
    out=$(cd "$WT" && go test ./internal/sessionstatuscmd/ 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go test ./internal/sessionstatuscmd/ passes"
    else report_fail "go test ./internal/sessionstatuscmd/" "exit 0" "exit=$rc"$'\n'"$out"; fi
}

# ─── section B: classify maps submitted to actReview, not actDo ──────────────

test_classifier() {
    section "B. classify: submitted → actReview (not actDo)"
    local src="$WT/internal/sessionstatuscmd/session_status.go"

    # The `submitted` case must return actReview, and `ready` alone must be actDo
    # (the old shared `"ready", "submitted"` arm is gone).
    assert_not_contains "classify no longer shares ready+submitted in one arm" \
        '"ready", "submitted"' "$(cat "$src")"
    assert_contains "classify has a dedicated submitted case" \
        'case "submitted":' "$(cat "$src")"
    # In the submitted case block, actReview is returned (and actDo is not the
    # submitted result). Grep the case body up to the next case.
    local submitted_arm
    submitted_arm=$(awk '/case "submitted":/{f=1} f{print} /return actReview/{if(f)exit}' "$src")
    assert_contains "submitted case returns actReview" "return actReview" "$submitted_arm"
}

# ─── section C: glyph + label pinned in source ───────────────────────────────

test_glyph() {
    section "C. actReview glyph/label pinned (⚑ review)"
    local src="$WT/internal/sessionstatuscmd/session_status.go"
    assert_contains "actionMeta pins actReview to ⚑ review" \
        'actReview:  {"⚑", "review"}' "$(cat "$src")"
    # The ⚑ single-width guard is asserted by the Go test in section A; grep that
    # the assertion exists so the guarantee is not silently dropped.
    assert_contains "TestActionIcons asserts ⚑ display width == 1" \
        'displayWidth("⚑")' "$(cat "$WT/internal/sessionstatuscmd/session_status_test.go")"
}

# ─── section D: submitted excluded from the --tree do/plan backlog ───────────

test_tree_excludes() {
    section "D. --tree backlog (doPlanIDs) gathers only actDo/actPlan"
    local src="$WT/internal/sessionstatuscmd/tree.go"
    # doPlanIDs must gather only actDo/actPlan; actReview must NOT be added there,
    # so a submitted task never enters the implementation-order backlog.
    local body
    body=$(awk '/func doPlanIDs/{f=1} f{print} /^}/{if(f)exit}' "$src")
    assert_contains "doPlanIDs switches on actDo, actPlan" "case actDo, actPlan:" "$body"
    assert_not_contains "doPlanIDs does not include actReview" "actReview" "$body"
}

# ─── section E: regression — events + monitor ────────────────────────────────

test_regression() {
    section "E. Regression — go test ./internal/events/ ./internal/monitor/"
    local out rc
    out=$(cd "$WT" && go test ./internal/events/ ./internal/monitor/ 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go test ./internal/events/ ./internal/monitor/ passes"
    else report_fail "go test ./internal/events/ ./internal/monitor/" "exit 0" "exit=$rc"$'\n'"$out"; fi
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    WT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -n "${WT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }

    command -v go >/dev/null || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }

    printf '%sE-1765 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"

    test_unit
    test_classifier
    test_glyph
    test_tree_excludes
    test_regression

    summary
}

main "$@"
