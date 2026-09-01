#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1799 and records what was true when E-1799
# landed. Edit it only if you ARE E-1799. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1799 verification — per-task post-land script run by `endless worktree land`.
#
# A task can commit `.endless/hooks/post-land/e-<id>.sh` on its branch; after the
# ff-merge, land execs it once with cwd = the main checkout, `$1` = main root, and
# ENDLESS_TASK_ID / ENDLESS_MERGE_SHA / ENDLESS_WORKTREE_PATH / ENDLESS_BASE_BRANCH
# in the environment. Absent → silent no-op. Non-executable → loud warning, land
# still succeeds, script skipped. Non-zero exit → land still succeeds; loud error
# names the script, exit code, cwd, and the re-run command.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1799
#
# This is the single fail-fast gate. It folds:
#   1. The task's pytest suite (tests/test_worktree_land_post_land_script.py) —
#      the four land cases each drive a REAL land_worktree() (real git rebase +
#      ff-merge; only the DB/registry emit is mocked), plus the isolated
#      _run_post_land_script contract (cwd/argv/env/failure) and the call-site
#      ordering. The post-worktree-create-hook suite rides along as a regression.
#   2. Doc + wiring smoke checks (guide section present, call site present).
#
# Exit 0 all-passed, 1 any failure, 2 setup error. Structure per the house
# per-task verify convention (shape borrowed from .endless/tasks/e-1577/verify.sh).

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
    local desc="$1" expected="$2" actual="$3"
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

# assert_file_has DESC FILE PATTERN — pass when FILE exists and contains PATTERN.
assert_file_has() {
    local desc="$1" file="$2" pattern="$3"
    if [[ -f "${file}" ]] && grep -qF -- "${pattern}" "${file}"; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "${file} contains: ${pattern}" \
        "$( [[ -f ${file} ]] && echo '(pattern not found)' || echo 'file missing' )"
}

# ─── 1: pytest suite (fail-fast) ────────────────────────────────────────────

test_pytest_suite() {
    section "1 — pytest: post-land script contract + 4 real lands + regression"

    local out rc
    out=$(uv run pytest \
        tests/test_worktree_land_post_land_script.py \
        tests/test_worktree_create.py \
        -x -q 2>&1)
    rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "post-land-script + create-hook suites pass ($(printf '%s' "${out}" | tail -1))"
    else
        report_fail "post-land-script + create-hook suites pass" \
            "pytest exits 0" "$(printf '%s' "${out}" | tail -25)"
    fi
}

# ─── 2: wiring + doc smoke ───────────────────────────────────────────────────

test_wiring_and_docs() {
    section "2 — call-site wiring and agent-facing docs"

    assert_file_has "land calls _run_post_land_script" \
        "src/endless/worktree_cmd.py" "_run_post_land_script(" 2>/dev/null
    assert_file_has "post-land hook dir constant is defined" \
        "src/endless/worktree_cmd.py" 'POST_LAND_HOOK_DIR = ".endless/hooks/post-land"'
    assert_file_has "guide documents the post-land script" \
        "docs/guide/orchestration.md" ".endless/hooks/post-land/e-<id>.sh"
    assert_file_has "guide states non-fatal-and-loud contract" \
        "docs/guide/orchestration.md" "Failure is non-fatal and loud"
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

    if ! command -v uv >/dev/null 2>&1; then
        printf 'ERROR: uv not on PATH\n' >&2
        exit 2
    fi

    printf '%sE-1799 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_pytest_suite
    test_wiring_and_docs

    summary
}

main "$@"
