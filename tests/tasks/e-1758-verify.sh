#!/usr/bin/env bash
#
# E-1758 verification — `endless worktree check` (and its Go core
# `session-query worktree-anomalies`) reports ONLY genuine git/worktree handoff
# anomalies; a clean worktree prints nothing and exits 0. Empty output IS the
# representation of clean.
#
# Run from anywhere inside the worktree:
#   esu
#   ./tests/tasks/e-1758-verify.sh
#
# Strategy (identical shape to tests/tasks/e-1747-verify.sh): build a FULLY
# ISOLATED throwaway environment (temp XDG_CONFIG_HOME + fresh DB, a temp git
# project, a real linked worktree) and drive the CANDIDATE Python CLI
# (`.venv/bin/endless`) + the worktree-built `bin/endless-go` against it. Nothing
# touches the real endless repo, ledger, or this worktree's branch — teardown is
# `rm -rf`.
#
# What it checks (each against a task WITH a real worktree):
#   1. clean worktree            -> `worktree check` prints nothing, exit 0
#   2. untracked USER file       -> printed, exit 1
#   3. endless auto-managed file alone (.endless/verbs.jsonl) -> still clean, exit 0
#   4. commits ahead of main, otherwise clean -> still clean, exit 0 (silence rule)
#   5. detached HEAD             -> flagged, exit 1
#   6. `session status` shows the ◆ detail when a user file is dirty,
#      and nothing extra when clean
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

# check_run DESC EXPECT_RC EXPECT_EMPTY(0|1) -- CMD...
#   Runs CMD from inside the worktree, asserts exit code and (optionally) that
#   stdout is empty / non-empty.
check_run() {
    local desc="$1" want_rc="$2" want_empty="$3"; shift 3
    [[ "$1" == "--" ]] && shift
    local out rc
    out=$(cd "${WT}" && "$@" 2>/dev/null); rc=$?
    if [[ "${rc}" -ne "${want_rc}" ]]; then
        report_fail "${desc}" "exit ${want_rc}" "exit ${rc} | out='${out}'"; return
    fi
    if [[ "${want_empty}" == "1" && -n "${out}" ]]; then
        report_fail "${desc}" "empty stdout" "'${out}'"; return
    fi
    if [[ "${want_empty}" == "0" && -z "${out}" ]]; then
        report_fail "${desc}" "non-empty stdout" "(empty)"; return
    fi
    report_pass "${desc}"
}

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

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    EN="${REPO_ROOT}/.venv/bin/endless"
    GO="${REPO_ROOT}/bin/endless-go"
    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    [[ -x "${GO}" ]] || {
        printf 'ERROR: %s missing — run `just go`\n' "${GO}" >&2; exit 2; }
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
    # Mirror the real endless checkout's gitignore for the per-worktree companion
    # + lock (E-1218): they are untracked and must never surface in `git status`.
    # Without this the companion would be flagged as a user file in every check.
    printf '.endless/worktree.json\n.endless/worktree.lock\n' > "${PROJ}/.gitignore"
    git -C "${PROJ}" add README.md .gitignore
    git -C "${PROJ}" commit -q -m "init"

    en register "${PROJ}" --infer --name verify1758 --status active >/dev/null 2>&1 || {
        printf 'ERROR: registering temp project failed\n' >&2; exit 2; }

    TID=$(add_task "Task with a worktree") || {
        printf 'ERROR: creating worktree task failed: %s\n' "${TID}" >&2; exit 2; }
    local num="${TID#E-}"
    WT="${PROJ}/.endless/worktrees/e-${num}"
    git -C "${PROJ}" worktree add -q -b "task/${num}" "${WT}" main 2>/dev/null || {
        printf 'ERROR: creating worktree for %s failed\n' "${TID}" >&2; exit 2; }
    mkdir -p "${WT}/.endless"
    printf '{"kind":"task","base_branch":"main","branch":"task/%s"}\n' "${num}" \
        > "${WT}/.endless/worktree.json"
}

# ensure_companion — (re)write the worktree.json marker. In this throwaway repo
# the companion is untracked (no `.gitignore` rule for it, unlike a real endless
# checkout), so any tree-restoring step must not rely on git to preserve it.
ensure_companion() {
    mkdir -p "${WT}/.endless"
    printf '{"kind":"task","base_branch":"main","branch":"task/%s"}\n' "${TID#E-}" \
        > "${WT}/.endless/worktree.json"
}

# reset_clean — return the worktree to a pristine on-branch state between checks.
# Deliberately does NOT use `git clean -fd`: that would delete the untracked
# companion marker (see ensure_companion) and make every subsequent check look
# like it is running outside a worktree. We reattach the branch, discard tracked
# edits, and remove only the specific scratch artifacts the checks create.
reset_clean() {
    git -C "${WT}" checkout -q "task/${TID#E-}" 2>/dev/null
    rm -f "${WT}/scratch.go" "${WT}/.endless/verbs.jsonl" 2>/dev/null
    git -C "${WT}" checkout -q -- . 2>/dev/null   # restore any tracked edits/deletions
    ensure_companion
}

# ─── checks ─────────────────────────────────────────────────────────────────

check_clean() {
    section "1 — clean worktree prints nothing, exit 0"
    reset_clean
    check_run "worktree check (clean) → empty, exit 0" 0 1 -- "${EN}" worktree check
}

check_user_file() {
    section "2 — untracked USER file → flagged, exit 1"
    reset_clean
    : > "${WT}/scratch.go"
    local out rc
    out=$(cd "${WT}" && "${EN}" worktree check 2>/dev/null); rc=$?
    if [[ "${rc}" -eq 1 ]]; then report_pass "exit 1 when a user file is dirty"
    else report_fail "exit 1 when a user file is dirty" "exit 1" "exit ${rc}"; fi
    assert_contains "names the uncommitted anomaly" "${out}" "uncommitted"
    assert_contains "names the user file" "${out}" "scratch.go"
    rm -f "${WT}/scratch.go"
}

check_auto_managed() {
    section "3 — endless auto-managed file alone → still clean, exit 0"
    reset_clean
    printf '{}\n' > "${WT}/.endless/verbs.jsonl"
    check_run "auto-managed dirt only → empty, exit 0" 0 1 -- "${EN}" worktree check
    rm -f "${WT}/.endless/verbs.jsonl"
}

check_ahead_of_main() {
    section "4 — commits ahead of main, otherwise clean → still clean, exit 0"
    reset_clean
    echo "work" > "${WT}/feature.txt"
    git -C "${WT}" add feature.txt
    git -C "${WT}" commit -q -m "feature commit"
    check_run "unlanded commits alone → empty, exit 0 (silence rule)" 0 1 -- "${EN}" worktree check
    # leave the branch ahead; reset_clean in the next check restores tree cleanliness
}

check_detached() {
    section "5 — detached HEAD → flagged, exit 1"
    reset_clean
    git -C "${WT}" checkout -q --detach HEAD 2>/dev/null
    local out rc
    out=$(cd "${WT}" && "${EN}" worktree check 2>/dev/null); rc=$?
    if [[ "${rc}" -eq 1 ]]; then report_pass "exit 1 on detached HEAD"
    else report_fail "exit 1 on detached HEAD" "exit 1" "exit ${rc} | '${out}'"; fi
    assert_contains "names the detached-head anomaly" "${out}" "detached"
    git -C "${WT}" checkout -q "task/${TID#E-}" 2>/dev/null
}

check_session_status_detail() {
    section "6 — session status shows ◆ detail when dirty, nothing extra when clean"
    reset_clean
    # Clean: the focal row's detail lines must be absent.
    local clean_out
    clean_out=$(cd "${WT}" && "${GO}" session-status --task "${TID#E-}" 2>/dev/null || true)
    assert_not_contains "clean focal → no anomaly detail line" "${clean_out}" "uncommitted:"

    # Dirty user file: the ◆ detail line must appear for the focal row.
    : > "${WT}/scratch.go"
    local dirty_out
    dirty_out=$(cd "${WT}" && "${GO}" session-status --task "${TID#E-}" 2>/dev/null || true)
    assert_contains "dirty focal → ◆ anomaly detail line" "${dirty_out}" "uncommitted:"
    rm -f "${WT}/scratch.go"
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup

    printf '%sE-1758 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cli:     %s\n' "${EN}"
    printf '  go:      %s\n' "${GO}"
    printf '  env:     isolated (%s)\n' "${WORK}"
    printf '  task:    %s (worktree %s)\n' "${TID}" "${WT}"

    check_clean
    check_user_file
    check_auto_managed
    check_ahead_of_main
    check_detached
    check_session_status_detail

    summary
}

main "$@"
