#!/usr/bin/env bash
#
# E-1768 verification — the focal task's OWN uncommitted work is suppressed from
# the `session status` anomaly expansion, while every genuine kind still surfaces
# and the handoff-time `worktree check` surface is left unchanged.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-1768
#
# Background (see the task text): E-1758 unified two surfaces onto one
# monitor.WorktreeAnomalies set. `worktree check` runs at HANDOFF (a dirty tree
# IS an anomaly there — correct). `session status` renders CONTINUOUSLY, on the
# focal task you are mid-implementation on, where uncommitted user files are the
# EXPECTED work-in-progress state — a false positive. This task suppresses ONLY
# AnomalyUncommitted, ONLY in the session-status focal render.
#
# Strategy (same shape as .endless/tasks/e-1758/verify.sh): build a FULLY ISOLATED
# throwaway environment (temp XDG_CONFIG_HOME + fresh DB, a temp git project, a
# real linked worktree) and drive the worktree-built `bin/endless-go` +
# candidate `.venv/bin/endless` against it. Nothing touches the real endless
# repo, ledger, or this worktree's branch — teardown is `rm -rf`.
#
# What it checks:
#   0. E-1768's own Go unit tests (the focal-expansion filter) + the monitor
#      anomaly core tests (proving the shared core is unchanged).
#   (1-4 below run against a task WITH a real worktree, focal via --task.)
#   1. dirty USER file on the focal task -> `session status` expansion omits the
#      "uncommitted:" detail line (the bug fix)...
#   2. ...but the row itself STILL bears the inline ◆ dirty marker (E-1701 —
#      only the detail line is suppressed, not the coarse marker).
#   3. detached HEAD on the focal task -> a GENUINE kind still surfaces in the
#      expansion ("detached").
#   4. `worktree check` on the SAME dirty tree STILL flags "uncommitted" and
#      exits 1 — proving the suppression is session-status-only, not the shared
#      core / handoff surface.
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

# assert_contains DESC HAYSTACK NEEDLE
assert_contains() {
    local desc="$1" hay="$2" needle="$3"
    if [[ "${hay}" == *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "contains: ${needle}" "${hay}"
}

# assert_not_contains DESC HAYSTACK NEEDLE
assert_not_contains() {
    local desc="$1" hay="$2" needle="$3"
    if [[ "${hay}" != *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "does NOT contain: ${needle}" "${hay}"
}

# ─── setup ──────────────────────────────────────────────────────────────────

REPO_ROOT=""
WORK=""
PROJ=""
EN=""
GO=""
WT=""
TID=""

cleanup() { [[ -n "${WORK}" && -d "${WORK}" ]] && rm -rf "${WORK}"; }

en() { ( cd "${PROJ}" && "${EN}" "$@" ); }

add_task() {
    local title="$1" out
    out=$(en task add "${title}" 2>&1) || { printf '%s' "${out}"; return 1; }
    printf '%s\n' "${out}" | grep -oE 'E-[0-9]+' | head -1
}

# ss — run the worktree-built endless-go session-status from inside WT with the
# focal task named explicitly (headless focal resolution, E-1685). This is the
# render path under test; the named task renders as the ● focal row.
ss() { ( cd "${WT}" && "${GO}" session-status --task "${TID#E-}" 2>/dev/null || true ); }

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    EN="${REPO_ROOT}/.venv/bin/endless"
    GO="${REPO_ROOT}/bin/endless-go"
    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    [[ -x "${GO}" ]] || {
        printf 'ERROR: %s missing — run `just build`\n' "${GO}" >&2; exit 2; }
    if [[ ! -x "${EN}" ]]; then
        ( cd "${REPO_ROOT}" && uv run endless --version >/dev/null 2>&1 ) || {
            printf 'ERROR: could not materialize .venv (uv run endless failed)\n' >&2; exit 2; }
    fi

    WORK=$(mktemp -d)
    trap cleanup EXIT

    export XDG_CONFIG_HOME="${WORK}/config"
    export XDG_CACHE_HOME="${WORK}/cache"
    export ENDLESS_AUTO_MIGRATE=1
    export PATH="${REPO_ROOT}/bin:${PATH}"
    unset ENDLESS_SESSION_ID CLAUDECODE CLAUDE_CODE_SESSION_ID 2>/dev/null || true
    mkdir -p "${XDG_CONFIG_HOME}" "${XDG_CACHE_HOME}"

    PROJ="${WORK}/proj"
    mkdir -p "${PROJ}"
    git -C "${PROJ}" init -q -b main
    git -C "${PROJ}" config user.email "verify@example.com"
    git -C "${PROJ}" config user.name "Verify"
    : > "${PROJ}/README.md"
    # Mirror the real checkout's gitignore for the per-worktree companion + lock
    # (E-1218): untracked, must never surface as a user file in `git status`.
    printf '.endless/worktree.json\n.endless/worktree.lock\n' > "${PROJ}/.gitignore"
    git -C "${PROJ}" add README.md .gitignore
    git -C "${PROJ}" commit -q -m "init"

    en register "${PROJ}" --infer --name verify1768 --status active >/dev/null 2>&1 || {
        printf 'ERROR: registering temp project failed\n' >&2; exit 2; }

    TID=$(add_task "Verify focal-task worktree") || {
        printf 'ERROR: creating worktree task failed: %s\n' "${TID}" >&2; exit 2; }
    local num="${TID#E-}"
    WT="${PROJ}/.endless/worktrees/e-${num}"
    git -C "${PROJ}" worktree add -q -b "task/${num}" "${WT}" main 2>/dev/null || {
        printf 'ERROR: creating worktree for %s failed\n' "${TID}" >&2; exit 2; }
    ensure_companion
}

# ensure_companion — (re)write the worktree.json marker. In this throwaway repo
# the companion is gitignored (see setup) so it stays untracked; any tree-
# restoring step must not rely on git to preserve it.
ensure_companion() {
    mkdir -p "${WT}/.endless"
    printf '{"kind":"task","base_branch":"main","branch":"task/%s"}\n' "${TID#E-}" \
        > "${WT}/.endless/worktree.json"
}

# reset_clean — return the worktree to a pristine on-branch state between checks.
# Deliberately NOT `git clean -fd` (would delete the untracked companion marker).
reset_clean() {
    git -C "${WT}" checkout -q "task/${TID#E-}" 2>/dev/null
    rm -f "${WT}/scratch.go" 2>/dev/null
    git -C "${WT}" checkout -q -- . 2>/dev/null
    ensure_companion
}

# ─── checks ─────────────────────────────────────────────────────────────────

# check_go_unit — E-1768's own Go unit tests (the focal-expansion filter) plus
# the monitor anomaly core tests, so this script is self-contained. The project-
# wide `just test` / `go test ./...` regression is a separate pre-land concern.
check_go_unit() {
    section "0 — Go unit tests (focal-expansion filter + unchanged core)"
    local out rc
    out=$(cd "${REPO_ROOT}" && go test -count=1 ./internal/sessionstatuscmd/ \
        -run 'FocalExpansion' 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "session-status focal-expansion tests pass"
    else report_fail "session-status focal-expansion tests pass" "go test exit 0" "exit ${rc}
${out}"; fi

    out=$(cd "${REPO_ROOT}" && go test -count=1 ./internal/monitor/ \
        -run 'WorktreeAnomaliesAt|UserStatusPaths|WorktreeAnomalyLine' 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "monitor anomaly core tests still pass (core unchanged)"
    else report_fail "monitor anomaly core tests still pass (core unchanged)" "go test exit 0" "exit ${rc}
${out}"; fi
}

check_uncommitted_suppressed() {
    section "1 — dirty focal task → session status omits the uncommitted detail"
    reset_clean
    : > "${WT}/scratch.go"
    local out
    out=$(ss)
    # The false-positive detail line must be gone (the fix).
    assert_not_contains "focal 'uncommitted:' detail line suppressed" "${out}" "uncommitted:"
    # And nothing named the dirty file in an expansion line either.
    assert_not_contains "focal expansion does not list the dirty user file" "${out}" "scratch.go"
}

check_dirty_marker_survives() {
    section "2 — dirty focal row still bears the inline ◆ marker (E-1701)"
    reset_clean
    : > "${WT}/scratch.go"
    local out
    out=$(ss)
    # The coarse ◆ marker is width-neutral and sits in the row prefix ("T◆E-…");
    # only the EXPANDED detail line is suppressed, not the marker itself.
    assert_contains "focal row keeps the ◆ dirty marker" "${out}" "◆"
    rm -f "${WT}/scratch.go"
}

check_genuine_kind_surfaces() {
    section "3 — genuine anomaly (detached HEAD) still expands on the focal row"
    reset_clean
    git -C "${WT}" checkout -q --detach HEAD 2>/dev/null
    local out
    out=$(ss)
    assert_contains "focal expansion still shows the detached-head anomaly" "${out}" "detached"
    git -C "${WT}" checkout -q "task/${TID#E-}" 2>/dev/null
}

check_worktree_check_unchanged() {
    section "4 — handoff surface unchanged: worktree check STILL flags dirty"
    reset_clean
    : > "${WT}/scratch.go"
    local out rc
    out=$(cd "${WT}" && "${EN}" worktree check 2>/dev/null); rc=$?
    if [[ "${rc}" -eq 1 ]]; then report_pass "worktree check exits 1 on a dirty tree (unchanged)"
    else report_fail "worktree check exits 1 on a dirty tree (unchanged)" "exit 1" "exit ${rc} | '${out}'"; fi
    assert_contains "worktree check STILL reports the uncommitted anomaly" "${out}" "uncommitted"
    assert_contains "worktree check STILL names the user file" "${out}" "scratch.go"
    rm -f "${WT}/scratch.go"
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup

    printf '%sE-1768 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cli:     %s\n' "${EN}"
    printf '  go:      %s\n' "${GO}"
    printf '  env:     isolated (%s)\n' "${WORK}"
    printf '  task:    %s (worktree %s)\n' "${TID}" "${WT}"

    check_go_unit
    check_uncommitted_suppressed
    check_dirty_marker_survives
    check_genuine_kind_surfaces
    check_worktree_check_unchanged

    summary
}

main "$@"
