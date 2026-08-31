#!/usr/bin/env bash
#
# E-1800 verification — post-land residue check in `endless worktree land`.
#
# After a land (and after the E-1799 post-land script), land verifies the
# OUTCOME: residue = (ignored-and-present on main BEFORE the land) ∩
# (untracked-and-present AFTER it). A path in both was un-ignored by this land
# and left behind. Empty → silent success (also the common no-op). Non-empty →
# a loud error listing the paths, non-fatal to the already-advanced merge but
# exiting non-zero so automation notices. Compares git's own classifications;
# never parses .gitignore.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1800
#
# This is the single fail-fast gate. It folds:
#   1. The task's pytest suite (tests/test_worktree_land_post_land_residue.py) —
#      the I∩U computation + reporting (mocked untracked set), the two ls-files
#      parsers over a real repo, the call-site placement, and five REAL lands
#      (real git rebase + ff-merge; only the DB/registry emit is mocked) for the
#      plan's five cases. The E-1799 post-land-script suite rides along as a
#      regression on the step this one sequences after.
#   2. Wiring + doc smoke checks (snapshot + check call sites present, guide
#      section present).
#
# Exit 0 all-passed, 1 any failure, 2 setup error. Structure per the house
# per-task verify convention (shape borrowed from .endless/tasks/e-1799/verify.sh).

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
    section "1 — pytest: residue computation + 5 real lands + E-1799 regression"

    local out rc
    out=$(uv run pytest \
        tests/test_worktree_land_post_land_residue.py \
        tests/test_worktree_land_post_land_script.py \
        -x -q 2>&1)
    rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "residue + post-land-script suites pass ($(printf '%s' "${out}" | tail -1))"
    else
        report_fail "residue + post-land-script suites pass" \
            "pytest exits 0" "$(printf '%s' "${out}" | tail -25)"
    fi
}

# ─── 2: wiring + doc smoke ───────────────────────────────────────────────────

test_wiring_and_docs() {
    section "2 — call-site wiring and agent-facing docs"

    assert_file_has "land captures the pre-land ignored snapshot" \
        "src/endless/worktree_cmd.py" "_ignored_present_files(main_root)"
    assert_file_has "land runs the residue check" \
        "src/endless/worktree_cmd.py" "_check_post_land_residue(main_root, canonical, ignored_before)"
    assert_file_has "residue check compares git's untracked-present set" \
        "src/endless/worktree_cmd.py" "def _untracked_present_files"
    assert_file_has "guide documents the residue check" \
        "docs/guide/orchestration.md" "Post-land residue check"
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

    printf '%sE-1800 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_pytest_suite
    test_wiring_and_docs

    summary
}

main "$@"
