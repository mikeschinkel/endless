#!/usr/bin/env bash
#
# E-1700 verification script — confirms a spawned worker session's
# active_task_id no longer ends up NULL (which made its tmux status line show
# "claim a task").
#
# Two layers were fixed:
#
#   1. internal/hookcmd/claude.go — the SessionStart spawn-marker bind is now a
#      bool-returning trySpawnBind, and the cwd-derived fallback (maybeCwdBind)
#      is gated on !spawnBound rather than "no @endless_spawned_by marker". So a
#      spawn-marker read race still binds the session via its worktree cwd.
#
#   2. internal/monitor/db.go — the ROOT cause for self-dev spawns: a
#      cwd-self-detected sandbox (SelfDetectWorktreeSandbox) no longer
#      masquerades as an explicit --config-dir, so the hook/channel/tmux
#      PinMainDB override still routes session/pane-state writes to MAIN (where
#      the spawned task exists) while config/logs follow the sandbox. Without
#      this the bind hit the sandbox, where the task is absent, and FK-failed to
#      NULL. An explicit --config-dir (tests exercising endless) still wins and
#      routes to the sandbox.
#
# The behavior is exercised against the REAL code paths by the Go tests in
# internal/hookcmd/spawn_bind_test.go and internal/monitor/db_gate_test.go — this
# script compiles the affected packages and runs each behavior as a named check,
# so a single command tells Mike pass/fail per case. It also re-runs the pinned
# chat-takeover test to show the intended active_task_id-clearing semantics were
# preserved.
#
# Run from anywhere inside the worktree (esu cd's here and exports the session):
#   ./tests/tasks/e-1700-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on environment/setup error.

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

HOOKCMD_PKG="./internal/hookcmd/"
MONITOR_PKG="./internal/monitor/"

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
    local detail="$2"
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "${desc}"
    printf '      %sdetail:%s %s\n' "${DIM}" "${RESET}" "${detail}"
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

# assert_cmd DESC CMD [ARGS...]
#   Pass if CMD exits 0. On failure, report the tail of its combined output.
assert_cmd() {
    local desc="$1"
    shift
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "exit=${rc} | $(printf '%s' "${output}" | tail -3 | tr '\n' '⏎')"
}

# assert_go_test DESC PKG TESTNAME
#   Run exactly one Go test in PKG (fresh, uncached) and pass iff it succeeds.
assert_go_test() {
    local desc="$1"
    local pkg="$2"
    local name="$3"
    assert_cmd "${desc}" \
        go test -count=1 -run "^${name}$" "${pkg}"
}

# ─── checks ─────────────────────────────────────────────────────────────────

test_compiles() {
    section "Build — affected packages compile with the bind-race fix"
    assert_cmd "internal/hookcmd compiles" go build "${HOOKCMD_PKG}"
    assert_cmd "internal/monitor compiles" go build "${MONITOR_PKG}"
}

test_fix_behavior() {
    section "Fix — spawn-marker race falls back to a cwd bind (no NULL)"

    assert_go_test "taskID-read race reports 'not bound' so the fallback runs" \
        "${HOOKCMD_PKG}" "TestTrySpawnBind_RaceReturnsFalse"
    assert_go_test "spawned session with a raced @endless_task_id still binds via cwd" \
        "${HOOKCMD_PKG}" "TestSessionStartBind_CwdFallbackOnSpawnMarkerRace"
}

test_routing() {
    section "Root cause — self-dev session state routes to main, not sandbox"

    assert_go_test "cwd self-detect does NOT suppress the main pin; explicit --config-dir does" \
        "${MONITOR_PKG}" "TestSelfDetectVsExplicit_MainPinRouting"
    assert_go_test "PinMainDB still moves the DB to main while config/logs stay on the sandbox" \
        "${MONITOR_PKG}" "TestPinMainDB"
    assert_go_test "the self-dev worktree gate stays satisfied by both flag and pin contexts" \
        "${MONITOR_PKG}" "TestGuardWorktreeDBContext"
}

test_preserved_semantics() {
    section "Regression — intended chat-takeover clearing is preserved"

    assert_go_test "explicit chat takeover still clears active_task_id to NULL" \
        "${MONITOR_PKG}" "TestStartChatSession_UpsertClearsActiveTask"
}

test_no_regression() {
    section "Regression — hookcmd + monitor suites stay green"

    assert_cmd "internal/hookcmd full suite passes" \
        go test -count=1 "${HOOKCMD_PKG}"
    assert_cmd "internal/monitor full suite passes" \
        go test -count=1 "${MONITOR_PKG}"
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

    if ! command -v go >/dev/null 2>&1; then
        printf 'ERROR: go not on PATH\n' >&2
        exit 2
    fi

    # Worktrees need a go.work pointing at the local go-pkgs/ modules; without it
    # the replace directives resolve at the wrong depth and the build fails.
    # Generate it on demand so the script is self-contained.
    if [[ ! -f "${repo_root}/go.work" ]]; then
        if command -v just >/dev/null 2>&1; then
            just go-work-init >/dev/null 2>&1
        fi
        if [[ ! -f "${repo_root}/go.work" ]]; then
            printf 'ERROR: go.work missing and could not be generated (run: just go-work-init)\n' >&2
            exit 2
        fi
    fi

    printf '%sE-1700 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  go:      %s\n' "$(go version 2>&1 | awk '{print $3}')"

    test_compiles
    test_fix_behavior
    test_routing
    test_preserved_semantics
    test_no_regression

    summary
}

main "$@"
