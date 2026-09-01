#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1713 and records what was true when E-1713
# landed. Edit it only if you ARE E-1713. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1713 verification script — canAmend must not amend a ledger commit that is
# reachable from any ref besides the current branch.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1713
#
# The bug: after `worktree land` rebases a task branch onto main's ledger tip,
# the next ledger event on main used to `git commit --amend` that tip in place,
# orphaning the SHA the worktree branch still points at. taskWorktreeDirty
# (internal/monitor/reap_worktrees.go) then counts `main..HEAD > 0` and falsely
# reports the worktree dirty; re-landing never clears it.
#
# The fix broadens canAmend's published-history guard from "not reachable from
# origin/*" to "not reachable from any ref besides the current branch", so main
# appends instead of amending whenever another ref (a landed worktree branch, a
# remote, or a tag) shares its ledger tip.
#
# This script is the entire verify handoff (E-1596 result contract): it ensures
# binaries are current, then runs the Go unit + reproduction suite in
# internal/events, which exercises the real production CommitLedgerSegment path
# against fixture git repos that reproduce the exact orphaning topology. The
# reproduction test asserts the same invariant taskWorktreeDirty inverts
# (main..HEAD == 0 for the sibling/worktree branch), so a git-level fixture is a
# faithful, deterministic stand-in for a full `endless worktree land` E2E
# without touching any real DB/sandbox.
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed (prints
# ALL PASSED), 1 on any failure (prints a numbered failure list).
#
# Model: .endless/tasks/e-1577/verify.sh / e-1542-verify.sh (the E-1596 prototypes).

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# Run from the worktree root regardless of caller cwd.
ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || {
    printf 'ERROR: not inside a git work tree\n' >&2
    exit 1
}
cd "${ROOT}" || exit 1

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() {
    printf '\n%s%s%s\n' "${BOLD}" "$1" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
}

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
    printf '\n%sSummary%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n' "${GREEN}" "${PASS_COUNT}" "${RESET}"
        printf '\n  %sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}"
    printf '\n  %sFAILED:%s\n' "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do
        printf '    - %s\n' "${t}"
    done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_succeeds DESC CMD [ARGS...] — pass iff CMD exits 0.
assert_succeeds() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

# ─── build ────────────────────────────────────────────────────────────────

test_build() {
    section "Build"
    if [[ ! -f bin/endless-go ]] || [[ -n "$(find . -name '*.go' -not -path './vendor/*' -newer bin/endless-go 2>/dev/null | head -1)" ]]; then
        assert_succeeds "just build (binaries stale)" just build
    else
        report_pass "binaries up to date (skipping build)"
    fi
}

# ─── Go unit + reproduction suite ─────────────────────────────────────────

test_go_suite() {
    section "canAmend unit + reproduction tests (internal/events)"

    # Whole package (regression-safe: the origin/* refusal + amend-hygiene tests
    # must still pass under the broadened guard).
    assert_succeeds "go test ./internal/events/..." \
        go test ./internal/events/...

    # Named reproduction tests, run individually so a regression names itself.
    # These fail on baseline (amend orphans the sibling/worktree branch) and
    # pass with the fix.
    assert_succeeds "sibling branch on ledger tip → append (not amend)" \
        go test ./internal/events/ -run '^TestCommitLedgerSegment_SiblingBranchStartsNewCommit$'
    assert_succeeds "landed worktree tip stays ancestor of main (no orphan)" \
        go test ./internal/events/ -run '^TestCommitLedgerSegment_LandedWorktreeTipStaysAncestor$'
    assert_succeeds "sole-ref tip still amends (optimization intact)" \
        go test ./internal/events/ -run '^TestCommitLedgerSegment_SoleRefAmends$'
    assert_succeeds "pushed tip still refuses amend (origin/* guard intact)" \
        go test ./internal/events/ -run '^TestCommitLedgerSegment_PushedHeadStartsNewCommit$'
}

# ─── main ─────────────────────────────────────────────────────────────────

test_build
test_go_suite
summary
