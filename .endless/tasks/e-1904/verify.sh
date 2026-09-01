#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1904 and records what was true when E-1904
# landed. Edit it only if you ARE E-1904. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1904 verification — a worktree's dependents are cleaned up when the
# worktree is reaped, and NOTHING still referenced is ever destroyed.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-1904
#
# WHAT LANDED
#   Three defects, one root cause: worktree teardown never cleaned up its
#   dependents, and the one predicate that could have caught it was blind.
#
#   1. sandboxcmd/list.go classify() returned stateInUse UNCONDITIONALLY for
#      keep/persistent sandboxes. prune only removes stateOrphaned, so
#      `endless-sandbox prune` could never reclaim a worktree sandbox no matter
#      how long its worktree had been gone. It now consults a ReapGuard.
#   2. monitor/reap_worktrees.go had zero references to sandboxes — Destroy was
#      unreachable from the drop/land/reap path. ReapStaleWorktrees now calls
#      monitor.ReapSandbox, a seam wired in cmd/endless-go to
#      sandboxcmd.ReapSandboxForWorktree (sandboxcmd imports monitor, so the
#      dependency cannot run the other way).
#   3. channelcmd only exited on a signal or MCP session end, neither of which
#      fires when its worktree is reaped. Four channel processes were found
#      alive 18-24 days after their worktrees were removed, each pinning a
#      sandbox open. A watchdog exited the process when its worktree went.
#      E-2029 deleted the whole channel surface, watchdog included, so this
#      suite no longer asserts defect 3 — there is no process left to strand.
#      Defects 1 and 2 are unaffected and still covered below.
#
# THE REAP SAFETY PREDICATE (the load-bearing part)
#   A sandbox OUTLIVES its worktree directory, so directory existence alone is
#   not a sufficient check. During the manual cleanup that motivated this task,
#   a dir-existence rule destroyed 10 sandboxes it should not have (all were
#   restored from a pre-delete backup). A sandbox is reapable only when ALL of
#   these are false:
#     1. the worktree directory exists
#     2. git still tracks a worktree of that name
#     3. a live tmux window references the task ID    <- survives dir removal
#     4. the task branch is not merged to main        <- survives dir removal
#     5. a live process holds files open in it        (enforced by destroy)
#   Conditions 3 and 4 are the whole point: they are the two that outlive the
#   directory, and they are exactly the two the naive rule missed.
#
# Layers:
#   A. FAIL-FAST unit tests — the guard's five protection conditions, its
#      fail-closed behavior, classify()'s orphan transition, and the reaper
#      seam. If these break, stop.
#   B. Guard semantics — a nil guard must preserve the conservative in-use
#      fallback, and an ephemeral (random-hex) name must not be worktree-bound.
#   C. Wiring — monitor.ReapSandbox is actually assigned in cmd/endless-go, and
#      prune refuses to run without a guard.
#   D. Project-wide regression — `go test ./...` and the full Python suite.
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

REPO_ROOT=""

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'
    BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
note()    { printf '  %s%s%s\n' "${DIM}" "$1" "${RESET}"; }

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

# assert_go_test DESC PKG RUN_PATTERN
assert_go_test() {
    local desc="$1" pkg="$2" pattern="$3"
    local output rc
    output=$(cd "${REPO_ROOT}" && go test "${pkg}" -run "${pattern}" -count=1 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return 0; fi
    report_fail "${desc}" "go test ${pkg} -run ${pattern} exits 0" "$(printf '%s' "${output}" | tail -5)"
    return 1
}

# assert_grep DESC PATTERN FILE
assert_grep() {
    local desc="$1" pattern="$2" file="$3"
    if grep -qE "${pattern}" "${REPO_ROOT}/${file}" 2>/dev/null; then
        report_pass "${desc}"; return 0
    fi
    report_fail "${desc}" "${pattern} present in ${file}" "not found"
    return 1
}

# ─── setup ──────────────────────────────────────────────────────────────────

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || {
        printf 'setup error: not inside a git repository\n' >&2
        exit 2
    }
    if [[ ! -f "${REPO_ROOT}/internal/sandboxcmd/reapguard.go" ]]; then
        printf 'setup error: reapguard.go not found — is this the E-1904 worktree?\n' >&2
        exit 2
    fi
}

# ─── layer A: fail-fast unit tests ──────────────────────────────────────────

layer_a() {
    section "A. Fail-fast unit tests"
    note "the reap safety predicate and the two surviving defect fixes"

    assert_go_test "guard: all five protection conditions" \
        ./internal/sandboxcmd/ 'TestReapGuardProtectionConditions' || return 1

    assert_go_test "guard: a failed probe errors rather than unprotecting everything" \
        ./internal/sandboxcmd/ 'TestReapGuardFailsClosed' || return 1

    assert_go_test "guard: no tmux server is an empty set, not an error" \
        ./internal/sandboxcmd/ 'TestReapGuardNoTmuxServerIsEmptyNotError' || return 1

    assert_go_test "classify: persistent+unprotected now reports orphaned" \
        ./internal/sandboxcmd/ 'TestClassify' || return 1

    assert_go_test "reaper: a reaped worktree invokes the sandbox seam" \
        ./internal/monitor/ 'TestReapBoundSandbox' || return 1

    return 0
}

# ─── layer B: guard semantics ───────────────────────────────────────────────

layer_b() {
    section "B. Guard semantics"
    note "the conservative fallbacks that keep a broken probe from destroying work"

    assert_grep "classify falls back to in-use when the guard is nil" \
        'guard == nil' internal/sandboxcmd/list.go

    assert_grep "guard treats a non-e-NNNN name as not worktree-bound" \
        'sandboxTaskRe.MatchString' internal/sandboxcmd/reapguard.go

    assert_grep "guard checks the tmux-window condition" \
        'reasonTmuxWindow' internal/sandboxcmd/reapguard.go

    assert_grep "guard checks the unmerged-branch condition" \
        'reasonUnmergedBranch' internal/sandboxcmd/reapguard.go
}

# ─── layer C: wiring ────────────────────────────────────────────────────────

layer_c() {
    section "C. Wiring"
    note "a seam that is never assigned is a no-op, so assert the assignment"

    assert_grep "cmd/endless-go wires monitor.ReapSandbox" \
        'monitor\.ReapSandbox = sandboxcmd\.ReapSandboxForWorktree' cmd/endless-go/main.go

    assert_grep "the reaper calls the seam after a successful reap" \
        'reapBoundSandbox\(e\.Name\(\)\)' internal/monitor/reap_worktrees.go

    assert_grep "prune refuses to run without a guard" \
        'refusing to prune without a reap guard' internal/sandboxcmd/prune.go

    assert_grep "ReapSandboxForWorktree re-checks the guard at deletion time" \
        'guard.Protected\(worktreeName\)' internal/sandboxcmd/reap.go
}

# ─── layer D: project-wide regression ───────────────────────────────────────

layer_d() {
    section "D. Project-wide regression"

    local output rc

    output=$(cd "${REPO_ROOT}" && go build ./... 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "go build ./..."
    else
        report_fail "go build ./..." "exit 0" "$(printf '%s' "${output}" | tail -5)"
    fi

    # Explicit -timeout because of a PRE-EXISTING flaw tracked as E-1908, not
    # anything this task introduced: TestDestroyRefusesWithLiveWriter and
    # TestDestroyForceOverridesLiveWriterCheck call t.Setenv("HOME", tmp) before
    # shelling out to `go build`, which redirects GOCACHE into an empty temp dir
    # and cold-rebuilds the whole dependency graph (sqlite included). Those three
    # destroy tests measure ~205s of the sandboxcmd package's ~215s and can cross
    # Go's default 10m package timeout on a loaded machine. Every test E-1904
    # added runs in 0.00s. Drop this flag once E-1908 lands.
    output=$(cd "${REPO_ROOT}" && go test -timeout 20m ./... 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "go test ./... (-timeout 20m; see E-1908)"
    else
        report_fail "go test ./..." "exit 0" "$(printf '%s' "${output}" | grep -E '^(FAIL|---)' | head -5)"
    fi

    output=$(cd "${REPO_ROOT}" && just test 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "just test (Python suite)"
    else
        report_fail "just test (Python suite)" "exit 0" "$(printf '%s' "${output}" | tail -5)"
    fi
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup
    printf '%sE-1904 — worktree dependent cleanup + reap safety predicate%s\n' "${BOLD}" "${RESET}"

    if ! layer_a; then
        note "fail-fast: a core contract broke; skipping later layers"
        summary
        return 1
    fi
    layer_b
    layer_c
    layer_d

    summary
}

main "$@"
