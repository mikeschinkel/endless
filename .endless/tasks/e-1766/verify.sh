#!/usr/bin/env bash
#
# E-1766 verification — `endless worktree check` no longer errors in a self-dev
# worktree via a needless sandbox-DB task lookup.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-1766
#
# The bug: `worktree check` resolved the worktree from cwd (path + companion,
# both DB-free) but then handed the Go core only `--task-id`, which round-tripped
# the DB purely to rediscover the worktree path. In a self-dev worktree the DB
# context self-routes to the per-worktree sandbox (E-1281/E-1368), where the task
# row does NOT live, so the lookup failed with
#   resolve project for E-NNNN: sql: no rows in result set   (exit 2)
# even though everything the probe needs (path from cwd, branch from the
# companion, repo root from the path convention) was already in hand. The fix
# hands the Go core `--worktree-path` + `--project-root` and drops the DB touch.
#
# Strategy (same shape as .endless/tasks/e-1758/verify.sh): build a FULLY ISOLATED
# throwaway environment and — crucially — reproduce the self-dev split that
# e-1758's verify did NOT: the task lives in the "main" DB (XDG_CONFIG_HOME) while
# cwd inside the worktree routes the CANDIDATE `bin/endless-go` to a per-worktree
# SANDBOX DB (empty, task-less) via the real E-1368 self-detection. That split is
# exactly what made the old `--task-id` path error. Nothing touches the real
# endless repo, ledger, or this worktree's branch — teardown is `rm -rf`.
#
# What it checks:
#   0. The Go anomaly core's unit tests (self-contained; the project-wide
#      `just test` / `go test ./...` regression is a separate pre-land concern).
#   1. FIXTURE SANITY: cwd inside the worktree routes endless-go to the empty
#      sandbox DB (task-text empty there, non-empty from the main checkout) —
#      proving the self-dev split that broke the old DB-based lookup is present.
#   2. clean worktree       -> `worktree check` prints nothing, exit 0, and NO
#                              "resolve project"/"no rows" error (the bug).
#   3. untracked USER file  -> flagged, exit 1.
#   4. auto-managed file alone (.endless/verbs.jsonl) -> still clean, exit 0.
#   5. detached HEAD        -> flagged, exit 1.
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

# assert_eq DESC WANT GOT
assert_eq() {
    local desc="$1" want="$2" got="$3"
    if [[ "${want}" == "${got}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${want}" "${got}"
}

# ─── setup ──────────────────────────────────────────────────────────────────

REPO_ROOT=""
WORK=""
PROJ=""
EN=""
GO=""
WT=""
TID=""
NUM=""

cleanup() { [[ -n "${WORK}" && -d "${WORK}" ]] && rm -rf "${WORK}"; }

en() { ( cd "${PROJ}" && "${EN}" "$@" ); }

add_task() {
    local title="$1" out
    out=$(en task add "${title}" 2>&1) || { printf '%s' "${out}"; return 1; }
    printf '%s\n' "${out}" | grep -oE 'E-[0-9]+' | head -1
}

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    EN="${REPO_ROOT}/.venv/bin/endless"
    GO="${REPO_ROOT}/bin/endless-go"
    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    command -v python3 >/dev/null 2>&1 || { printf 'ERROR: python3 not on PATH\n' >&2; exit 2; }
    [[ -x "${GO}" ]] || {
        printf 'ERROR: %s missing — run `just build`\n' "${GO}" >&2; exit 2; }
    if [[ ! -x "${EN}" ]]; then
        ( cd "${REPO_ROOT}" && uv run endless --version >/dev/null 2>&1 ) || {
            printf 'ERROR: could not materialize .venv (uv run endless failed)\n' >&2; exit 2; }
    fi

    # Resolve to the real path so `git rev-parse --show-toplevel` (which the
    # candidate resolves) and `git worktree list` agree on the worktree path
    # (macOS /var -> /private/var symlink otherwise splits them).
    WORK=$(cd "$(mktemp -d)" && pwd -P)
    trap cleanup EXIT

    export XDG_CONFIG_HOME="${WORK}/config"   # the "main" DB: the task lives here
    export XDG_CACHE_HOME="${WORK}/cache"     # CacheDir(): sandboxes live under here
    export ENDLESS_AUTO_MIGRATE=1
    export PATH="${REPO_ROOT}/bin:${PATH}"    # candidate endless-go wins shutil.which
    unset ENDLESS_SESSION_ID CLAUDECODE CLAUDE_CODE_SESSION_ID 2>/dev/null || true
    mkdir -p "${XDG_CONFIG_HOME}" "${XDG_CACHE_HOME}"

    PROJ="${WORK}/proj"
    mkdir -p "${PROJ}"
    git -C "${PROJ}" init -q -b main
    git -C "${PROJ}" config user.email "verify@example.com"
    git -C "${PROJ}" config user.name "Verify"
    : > "${PROJ}/README.md"
    # Mirror the real endless checkout's gitignore for the per-worktree companion
    # + lock (E-1218): untracked, must never surface in `git status`.
    printf '.endless/worktree.json\n.endless/worktree.lock\n' > "${PROJ}/.gitignore"
    git -C "${PROJ}" add README.md .gitignore
    git -C "${PROJ}" commit -q -m "init"

    en register "${PROJ}" --infer --name verify1766 --status active >/dev/null 2>&1 || {
        printf 'ERROR: registering temp project failed\n' >&2; exit 2; }

    # Mark the project self_dev so the candidate endless-go self-routes to the
    # per-worktree sandbox from cwd (E-1368). register wrote config.json; merge
    # the flag in without disturbing its other fields.
    python3 - "${PROJ}/.endless/config.json" <<'PY' || { printf 'ERROR: could not set self_dev\n' >&2; exit 2; }
import json, sys
p = sys.argv[1]
with open(p) as f:
    d = json.load(f)
d["self_dev"] = True
with open(p, "w") as f:
    json.dump(d, f, indent=2)
PY

    TID=$(add_task "Task with a self-dev worktree") || {
        printf 'ERROR: creating worktree task failed: %s\n' "${TID}" >&2; exit 2; }
    NUM="${TID#E-}"
    # Give the task a non-trivial plan so the fixture-sanity check can prove the
    # main DB has it and the sandbox DB does not.
    en task update "${TID}" --text \
        "Plan for ${TID}: exercise the self-dev worktree check path." >/dev/null 2>&1

    WT="${PROJ}/.endless/worktrees/e-${NUM}"
    git -C "${PROJ}" worktree add -q -b "task/${NUM}" "${WT}" main 2>/dev/null || {
        printf 'ERROR: creating worktree for %s failed\n' "${TID}" >&2; exit 2; }
    ensure_companion

    # The self-dev sandbox config dir. SelfDetectWorktreeSandbox requires it to
    # exist on disk (else it no-ops); AUTO_MIGRATE creates the empty DB on first
    # open. This is the task-less DB the cwd routes to.
    mkdir -p "${XDG_CACHE_HOME}/endless/sandboxes/e-${NUM}/endless"
}

# ensure_companion — (re)write the worktree.json marker. Untracked in this
# throwaway repo, so any tree-restoring step must not rely on git to keep it.
ensure_companion() {
    mkdir -p "${WT}/.endless"
    printf '{"kind":"task","base_branch":"main","branch":"task/%s"}\n' "${NUM}" \
        > "${WT}/.endless/worktree.json"
}

# reset_clean — return the worktree to a pristine on-branch state between checks.
# Avoids `git clean -fd` (would delete the untracked companion marker).
reset_clean() {
    git -C "${WT}" checkout -q "task/${NUM}" 2>/dev/null
    rm -f "${WT}/scratch.go" "${WT}/.endless/verbs.jsonl" 2>/dev/null
    git -C "${WT}" checkout -q -- . 2>/dev/null
    ensure_companion
}

# ─── checks ─────────────────────────────────────────────────────────────────

check_go_unit() {
    section "0 — Go unit tests for the worktree-anomaly core"
    local out rc
    out=$(cd "${REPO_ROOT}" && go test ./internal/monitor/ \
        -run 'WorktreeAnomaliesAt|UserStatusPaths|IsAutoManagedPath|WorktreeAnomalyLine' 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "monitor anomaly unit tests pass"
    else report_fail "monitor anomaly unit tests pass" "go test exit 0" "exit ${rc}
${out}"; fi
}

check_fixture_split() {
    section "1 — fixture sanity: cwd routes endless-go to the empty sandbox DB"
    # From the main checkout (no worktree): opens the main DB → task present.
    local from_main
    from_main=$(cd "${PROJ}" && "${GO}" session-query task-text --id "${NUM}" 2>/dev/null)
    assert_contains "task present in the main DB" "${from_main}" "Plan for ${TID}"
    # From inside the worktree: self-routes to the sandbox DB → task absent.
    local from_wt
    from_wt=$(cd "${WT}" && "${GO}" session-query task-text --id "${NUM}" 2>/dev/null)
    assert_eq "task absent in the cwd-routed sandbox DB (self-dev split present)" "" "${from_wt}"
}

check_clean() {
    section "2 — clean worktree: prints nothing, exit 0, no DB-lookup error"
    reset_clean
    local out err rc
    out=$(cd "${WT}" && "${EN}" worktree check 2>/tmp/e1766_err); rc=$?
    err=$(cat /tmp/e1766_err 2>/dev/null); rm -f /tmp/e1766_err
    assert_eq "clean check exits 0" "0" "${rc}"
    assert_eq "clean check prints nothing" "" "${out}"
    # The bug: the sandbox-DB lookup errored here. Guard against its return.
    assert_not_contains "no 'resolve project' DB error" "${err}" "resolve project"
    assert_not_contains "no 'no rows' DB error" "${err}" "no rows"
}

check_user_file() {
    section "3 — untracked USER file → flagged, exit 1"
    reset_clean
    : > "${WT}/scratch.go"
    local out rc
    out=$(cd "${WT}" && "${EN}" worktree check 2>/dev/null); rc=$?
    assert_eq "exit 1 when a user file is dirty" "1" "${rc}"
    assert_contains "names the uncommitted anomaly" "${out}" "uncommitted"
    assert_contains "names the user file" "${out}" "scratch.go"
    rm -f "${WT}/scratch.go"
}

check_auto_managed() {
    section "4 — endless auto-managed file alone → still clean, exit 0"
    reset_clean
    printf '{}\n' > "${WT}/.endless/verbs.jsonl"
    local out rc
    out=$(cd "${WT}" && "${EN}" worktree check 2>/dev/null); rc=$?
    assert_eq "auto-managed dirt only → exit 0" "0" "${rc}"
    assert_eq "auto-managed dirt only → empty" "" "${out}"
    rm -f "${WT}/.endless/verbs.jsonl"
}

check_detached() {
    section "5 — detached HEAD → flagged, exit 1"
    reset_clean
    git -C "${WT}" checkout -q --detach HEAD 2>/dev/null
    local out rc
    out=$(cd "${WT}" && "${EN}" worktree check 2>/dev/null); rc=$?
    assert_eq "exit 1 on detached HEAD" "1" "${rc}"
    assert_contains "names the detached-head anomaly" "${out}" "detached"
    git -C "${WT}" checkout -q "task/${NUM}" 2>/dev/null
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup

    printf '%sE-1766 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cli:     %s\n' "${EN}"
    printf '  go:      %s\n' "${GO}"
    printf '  env:     isolated self-dev (%s)\n' "${WORK}"
    printf '  task:    %s (main DB)\n' "${TID}"
    printf '  wt:      %s\n' "${WT}"
    printf '  sandbox: %s\n' "${XDG_CACHE_HOME}/endless/sandboxes/e-${NUM}"

    check_go_unit
    check_fixture_split
    check_clean
    check_user_file
    check_auto_managed
    check_detached

    summary
}

main "$@"
