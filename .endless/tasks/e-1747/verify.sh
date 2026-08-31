#!/usr/bin/env bash
#
# E-1747 verification — every multiline document field is mirrored to a
# committed `.endless/<subdir>/E-NNN.md` (or ED-NNN.md) file, not just the plan.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-1747
#
# Strategy: build a FULLY ISOLATED throwaway environment (temp XDG_CONFIG_HOME +
# fresh DB, temp git project, a real linked worktree) and drive the CANDIDATE
# Python CLI (`.venv/bin/endless`, the worktree's editable install) + the
# worktree-built `bin/endless-go` against it. Nothing touches the real endless
# repo, the real ledger, or this worktree's own branch — teardown is `rm -rf`.
#
# What it checks (each against a task WITH a real worktree unless noted):
#   1. `task update --outcome`      -> .endless/outcomes/E-NNN.md written+committed
#   2. `task update --outcome-file` -> same mirror content
#   3. `task update --analysis`     -> .endless/analyses/E-NNN.md written+committed
#   4. `task assume --outcome`      -> outcome from a status verb also mirrors
#   5. regression: `task update --text` -> .endless/plans/E-NNN.md still works
#   6. decision body, from MAIN cwd     -> .endless/decisions/ED-NNN.md on main
#   7. decision body, from WORKTREE cwd -> ED-NNN.md committed on the wt branch
#   8. no-worktree task -> DB-only, NO file written, command still succeeds
#
# Birth-time seeding of all mirrors at worktree creation is covered by the Go
# and Python unit tests (test_plan_file_to_worktree.py::
# test_materialize_task_docs_seeds_all_fields); this script is the end-to-end
# CLI check of the write-time paths.
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

# assert_file_has DESC FILE PATTERN — file exists and contains PATTERN.
assert_file_has() {
    local desc="$1" file="$2" pat="$3"
    if [[ ! -f "${file}" ]]; then
        report_fail "${desc}" "file exists: ${file}" "absent"; return
    fi
    if grep -q -- "${pat}" "${file}"; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "contains: ${pat}" "$(cat "${file}")"
}

# assert_head_subject DESC GITDIR SUBJECT — HEAD commit subject equals SUBJECT.
assert_head_subject() {
    local desc="$1" gitdir="$2" want="$3" got
    got=$(git -C "${gitdir}" log --format=%s -n 1 2>&1)
    if [[ "${got}" == "${want}" ]]; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "HEAD subject == ${want}" "${got}"
}

# assert_absent DESC PATH
assert_absent() {
    local desc="$1" path="$2"
    if [[ ! -e "${path}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "path does not exist: ${path}" "present"
}

# assert_succeeds DESC CMD...
assert_succeeds() {
    local desc="$1"; shift
    local out rc
    out=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | ${out}"
}

# ─── setup ──────────────────────────────────────────────────────────────────

REPO_ROOT=""
WORK=""
PROJ=""
EN=""

cleanup() { [[ -n "${WORK}" && -d "${WORK}" ]] && rm -rf "${WORK}"; }

# en — the CANDIDATE Python CLI, run with cwd = the temp project so
# project-from-cwd resolution lands on our isolated project.
en() { ( cd "${PROJ}" && "${EN}" "$@" ); }
# en_in DIR ... — same CLI, run from a specific cwd (e.g. the worktree).
en_in() { local d="$1"; shift; ( cd "${d}" && "${EN}" "$@" ); }

add_task() {
    local title="$1" out
    out=$(en task add "${title}" 2>&1) || { printf '%s' "${out}"; return 1; }
    printf '%s\n' "${out}" | grep -oE 'E-[0-9]+' | head -1
}

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    EN="${REPO_ROOT}/.venv/bin/endless"
    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    [[ -x "${REPO_ROOT}/bin/endless-go" ]] || {
        printf 'ERROR: %s/bin/endless-go missing — run `just build`\n' "${REPO_ROOT}" >&2; exit 2; }
    # Materialize the editable venv if needed, then use its console script.
    if [[ ! -x "${EN}" ]]; then
        ( cd "${REPO_ROOT}" && uv run endless --version >/dev/null 2>&1 ) || {
            printf 'ERROR: could not materialize .venv (uv run endless failed)\n' >&2; exit 2; }
    fi

    WORK=$(mktemp -d)
    trap cleanup EXIT

    # Fully isolated endless environment — fresh config dir + DB, no sandbox,
    # candidate endless-go first on PATH.
    export XDG_CONFIG_HOME="${WORK}/config"
    export XDG_CACHE_HOME="${WORK}/cache"
    export ENDLESS_AUTO_MIGRATE=1
    export PATH="${REPO_ROOT}/bin:${PATH}"
    unset ENDLESS_SESSION_ID CLAUDECODE CLAUDE_CODE_SESSION_ID 2>/dev/null || true
    mkdir -p "${XDG_CONFIG_HOME}" "${XDG_CACHE_HOME}"

    # Temp project = a real git repo (the mirror's `_main_root_for_task`).
    PROJ="${WORK}/proj"
    mkdir -p "${PROJ}"
    git -C "${PROJ}" init -q -b main
    git -C "${PROJ}" config user.email "verify@example.com"
    git -C "${PROJ}" config user.name "Verify"
    : > "${PROJ}/README.md"
    git -C "${PROJ}" add README.md
    git -C "${PROJ}" commit -q -m "init"

    en register "${PROJ}" --infer --name verify1747 --status active >/dev/null 2>&1 || {
        printf 'ERROR: registering temp project failed\n' >&2; exit 2; }
}

# make_worktree TASK_ID — construct the linked worktree the mirror path looks
# for at <proj>/.endless/worktrees/e-<id>, with the companion marker.
make_worktree() {
    local id="$1" wt="${PROJ}/.endless/worktrees/e-$1"
    git -C "${PROJ}" worktree add -q -b "task/${id}" "${wt}" main 2>/dev/null || return 1
    mkdir -p "${wt}/.endless"
    printf '{"kind":"task","base_branch":"main","branch":"task/%s"}\n' "${id}" \
        > "${wt}/.endless/worktree.json"
    printf '%s' "${wt}"
}

# ─── checks ─────────────────────────────────────────────────────────────────

TID=""      # task WITH a worktree
WT=""       # its worktree path
NID=""      # task WITHOUT a worktree

check_task_field_mirrors() {
    section "1–5 — task field mirrors (outcome / analysis / text) on a worktree task"

    assert_succeeds "task update --outcome succeeds" \
        en task update "${TID}" --outcome "OUTCOME-INLINE-MARKER"
    assert_file_has "outcome mirrored to .endless/outcomes/E-NNN.md" \
        "${WT}/.endless/outcomes/${TID}.md" "OUTCOME-INLINE-MARKER"
    assert_head_subject "outcome mirror committed on the worktree branch" \
        "${WT}" "Endless: update outcome for ${TID}"

    local ofile="${WORK}/outcome.txt"
    printf 'OUTCOME-FROM-FILE-MARKER\n' > "${ofile}"
    assert_succeeds "task update --outcome-file succeeds" \
        en task update "${TID}" --outcome-file "${ofile}"
    assert_file_has "outcome-file mirror content" \
        "${WT}/.endless/outcomes/${TID}.md" "OUTCOME-FROM-FILE-MARKER"

    assert_succeeds "task update --analysis succeeds" \
        en task update "${TID}" --analysis "ANALYSIS-MARKER"
    assert_file_has "analysis mirrored to .endless/analyses/E-NNN.md" \
        "${WT}/.endless/analyses/${TID}.md" "ANALYSIS-MARKER"
    assert_head_subject "analysis mirror committed on the worktree branch" \
        "${WT}" "Endless: update analysis for ${TID}"

    assert_succeeds "task assume --outcome succeeds" \
        en task assume "${TID}" --outcome "ASSUME-OUTCOME-MARKER"
    assert_file_has "outcome from a status verb also mirrors" \
        "${WT}/.endless/outcomes/${TID}.md" "ASSUME-OUTCOME-MARKER"

    # Regression: the original plan (text) mirror still works.
    assert_succeeds "task update --text succeeds" \
        en task update "${TID}" --text "PLAN-MARKER"
    assert_file_has "text still mirrored to .endless/plans/E-NNN.md" \
        "${WT}/.endless/plans/${TID}.md" "PLAN-MARKER"
    assert_head_subject "plan mirror committed on the worktree branch" \
        "${WT}" "Endless: update plan for ${TID}"
}

check_decision_mirrors() {
    section "6–7 — decision body mirrors (main cwd, then worktree cwd)"

    # From the project (main) cwd: decision has no worktree → lands on main.
    local out did
    out=$(en decision add "Prefer explicit flags" \
        --description "DECISION-ON-MAIN-MARKER" --project verify1747 2>&1)
    did=$(printf '%s\n' "${out}" | grep -oE 'ED-[0-9]+' | head -1)
    if [[ -z "${did}" ]]; then
        report_fail "decision add (main cwd) returns an ED- id" "ED-NNN" "${out}"
    else
        report_pass "decision add (main cwd) returns an ED- id"
        assert_file_has "decision mirrored to .endless/decisions/ED-NNN.md on main" \
            "${PROJ}/.endless/decisions/${did}.md" "DECISION-ON-MAIN-MARKER"
        assert_head_subject "decision mirror committed on main" \
            "${PROJ}" "Endless: add decision ${did}"
    fi

    # From inside the worktree cwd: decision rides the worktree branch.
    local out2 did2
    out2=$(en_in "${WT}" decision add "Worktree-authored decision" \
        --description "DECISION-IN-WT-MARKER" --project verify1747 2>&1)
    did2=$(printf '%s\n' "${out2}" | grep -oE 'ED-[0-9]+' | head -1)
    if [[ -z "${did2}" ]]; then
        report_fail "decision add (worktree cwd) returns an ED- id" "ED-NNN" "${out2}"
    else
        report_pass "decision add (worktree cwd) returns an ED- id"
        assert_file_has "decision mirrored into the worktree" \
            "${WT}/.endless/decisions/${did2}.md" "DECISION-IN-WT-MARKER"
        assert_head_subject "decision mirror committed on the worktree branch" \
            "${WT}" "Endless: add decision ${did2}"
    fi
}

check_no_worktree() {
    section "8 — a task with no worktree: DB-only, no file, no crash"

    assert_succeeds "task update --outcome on a worktree-less task succeeds" \
        en task update "${NID}" --outcome "NO-WT-OUTCOME"
    assert_absent "no outcome file written for a worktree-less task" \
        "${PROJ}/.endless/outcomes/${NID}.md"
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup

    printf '%sE-1747 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cli:     %s\n' "${EN}"
    printf '  go:      %s/bin/endless-go\n' "${REPO_ROOT}"
    printf '  env:     isolated (%s)\n' "${WORK}"

    TID=$(add_task "Task with a worktree") || {
        printf 'ERROR: creating worktree task failed: %s\n' "${TID}" >&2; exit 2; }
    WT=$(make_worktree "${TID#E-}") || {
        printf 'ERROR: creating worktree for %s failed\n' "${TID}" >&2; exit 2; }
    NID=$(add_task "Task without a worktree") || {
        printf 'ERROR: creating no-worktree task failed: %s\n' "${NID}" >&2; exit 2; }
    printf '  tasks:   %s (worktree), %s (no worktree)\n' "${TID}" "${NID}"

    check_task_field_mirrors
    check_decision_mirrors
    check_no_worktree

    summary
}

main "$@"
