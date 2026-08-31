#!/usr/bin/env bash
#
# E-1856 verification script — proves the SessionStart cwd auto-bind no longer
# silently repoints a session's active_task_id, closing the two defects that
# made a task's Claude session unreachable via `session goto`/`session resume`.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1856
#
# The two behaviors under test (internal/hookcmd/claude.go, autoBindFromCwd):
#   1. A worktree already owned by a LIVE sibling session (non-stale worktree
#      lock held by a different session) is never bound into — no phantom
#      co-owner. Proven by TestSessionStart_LiveOwnedWorktreeRefusesAndDoesNotBind.
#   2. A resume (/clear, /compact) must not repoint active_task_id to a task
#      different from the one the session already carries — the cwd auto-bind is
#      a fallback for UNBOUND sessions, never a re-pointer. Proven by
#      TestAutoBindFromCwd_ResumeDoesNotRebindDifferentTask.
#
# Fail-fast: the E-1856 unit tests (and the E-1700 spawn-race fallback they must
# not regress) run first; the broader package suites follow.
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on an environment problem.
#
# REQUIRES the worktree's go.work (absolute-path replaces). If builds fail with
# "replacement directory ../go-pkgs/... does not exist", run `just go-work-init`.
#
# Per-task verify script (convention from E-1596 / E-1602).

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
    GREEN=$'\033[32m'
    RED=$'\033[31m'
    DIM=$'\033[2m'
    BOLD=$'\033[1m'
    RESET=$'\033[0m'
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
    local desc="$1"
    local expected="$2"
    local actual="$3"
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "${desc}"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "${expected}"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "${actual}"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("${desc}")
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
        "${GREEN}" "${PASS_COUNT}" "${RESET}" \
        "${RED}" "${FAIL_COUNT}" "${RESET}"
    printf '\n  %sFAILED:%s\n' "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do
        printf '    - %s\n' "${t}"
    done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_succeeds DESC CMD [ARGS...]  — pass if CMD exits 0.
assert_succeeds() {
    local desc="$1"
    shift
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
        return 0
    fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | $(printf '%s' "${output}" | tail -8)"
    return 1
}

# assert_no_output DESC CMD [ARGS...]  — pass if CMD produces no stdout/stderr.
assert_no_output() {
    local desc="$1"
    shift
    local output
    output=$("$@" 2>&1)
    if [[ -z "${output}" ]]; then
        report_pass "${desc}"
        return 0
    fi
    report_fail "${desc}" "no output" "${output}"
    return 1
}

# ─── 1: E-1856 behaviors (fail-fast) ─────────────────────────────────────────

test_behaviors() {
    section "1 — E-1856 behaviors (fail-fast)"

    assert_succeeds "resume/clear/compact does not repoint active_task_id from cwd (behavior 2)" \
        go test -run '^TestAutoBindFromCwd_ResumeDoesNotRebindDifferentTask$' -count=1 ./internal/hookcmd/ \
        || return 1
    assert_succeeds "live-owned worktree refuses and is never bound into (behavior 1)" \
        go test -run '^TestSessionStart_LiveOwnedWorktreeRefusesAndDoesNotBind$' -count=1 ./internal/hookcmd/ \
        || return 1
    assert_succeeds "spawn-race cwd fallback still binds an unbound session (E-1700 non-regression)" \
        go test -run '^TestSessionStartBind_CwdFallbackOnSpawnMarkerRace$' -count=1 ./internal/hookcmd/ \
        || return 1
    return 0
}

# ─── 2: static analysis ──────────────────────────────────────────────────────

test_static() {
    section "2 — Static analysis (build, vet, gofmt)"

    assert_succeeds "internal/hookcmd builds" \
        go build ./internal/hookcmd/
    assert_succeeds "internal/hookcmd is vet-clean" \
        go vet ./internal/hookcmd/
    assert_no_output "internal/hookcmd is gofmt-clean" \
        gofmt -l internal/hookcmd
    assert_succeeds "whole module still compiles (go build ./...)" \
        go build ./...
}

# ─── 3: full affected package suites ─────────────────────────────────────────

test_suites() {
    section "3 — Affected package suites are green"

    assert_succeeds "internal/hookcmd suite" \
        go test -count=1 ./internal/hookcmd/
    assert_succeeds "internal/monitor suite" \
        go test -count=1 ./internal/monitor/
}

# ─── main ────────────────────────────────────────────────────────────────────

main() {
    local repo_root
    repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd) || {
        printf 'cannot resolve repo root\n' >&2
        exit 2
    }
    cd "${repo_root}" || exit 2

    printf '%sE-1856 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  scope:   internal/hookcmd cwd auto-bind (SessionStart)\n'
    printf '  go:      %s\n' "$(go version 2>&1 | awk '{print $3}')"

    # Fail-fast: bail before the slower static/suite checks if the core
    # behaviors regress.
    if ! test_behaviors; then
        summary
        exit 1
    fi
    test_static
    test_suites

    summary
}

main "$@"
