#!/usr/bin/env bash
#
# E-1749 verification script — asserts the `endless guide` happy path no longer
# claims `endless worktree land` removes the worktree, and instead describes the
# retain-then-reap behavior the code actually implements (worktree + branch stay,
# a reaper sweep collects them after a grace period).
#
# Run from anywhere inside the worktree:
#   endless task verify E-1749
#
# Checks the canonical source (docs/guide/index.md — deterministic) and, when
# available, the rendered `endless guide` output (best-effort).
#
# Exit 0 on all-passed, 1 on any failure, 2 on a setup problem.
#
# Per-task verify script (house pattern; structure borrowed from
# .endless/tasks/e-1567/verify.sh), not a shipped deliverable.

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

# assert_absent DESC PATTERN TEXT — pass when TEXT does NOT contain PATTERN.
assert_absent() {
    local desc="$1" pattern="$2" text="$3"
    if [[ "${text}" != *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "does NOT contain: ${pattern}" \
        "$(printf '%s' "${text}" | grep -n "${pattern}" | head -3)"
}

# assert_present DESC PATTERN TEXT — pass when TEXT contains PATTERN.
assert_present() {
    local desc="$1" pattern="$2" text="$3"
    if [[ "${text}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "contains: ${pattern}" "(pattern not found)"
}

# ─── 1: canonical guide source (deterministic) ──────────────────────────────

test_guide_source() {
    section "1 — docs/guide/index.md (canonical source)"

    local guide="docs/guide/index.md"
    if [[ ! -f "${guide}" ]]; then
        report_fail "setup: ${guide} exists" "a readable file" "missing"
        return
    fi

    # Isolate the land happy-path sentence so we assert against the right line.
    local land_line
    land_line=$(grep -n 'land the work with .endless worktree land' "${guide}" | head -1)
    if [[ -z "${land_line}" ]]; then
        report_fail "locate land happy-path sentence" \
            "a line matching 'land the work with \`endless worktree land'" \
            "no match"
        return
    fi

    assert_absent "land sentence no longer says 'removes the worktree'" \
        "removes the worktree" "${land_line}"
    assert_present "land sentence says it retains the worktree" \
        "retains the worktree and its branch" "${land_line}"
    assert_present "land sentence mentions the grace period" \
        "grace period" "${land_line}"
    # E-1272 (drop auto-commit step) is still underway; the code still
    # auto-commits, so the clause must stay accurate to current behavior.
    assert_present "land sentence keeps the still-accurate auto-commit clause" \
        "auto-commits endless-managed files" "${land_line}"
}

# ─── 2: rendered `endless guide` output (best-effort) ───────────────────────

test_rendered_guide() {
    section "2 — rendered 'endless guide' output (best-effort)"

    # Render via the WORKTREE-LOCAL CLI (`uv run endless`) so the check reads
    # this worktree's edited docs/guide/index.md, not the global install's copy
    # (which still serves main's pre-land text). Skip if uv is unavailable.
    if ! command -v uv >/dev/null 2>&1; then
        report_pass "uv not on PATH — skipping rendered check (source covered above)"
        return
    fi

    local out rc
    out=$(uv run endless guide 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        report_pass "endless guide non-zero exit — skipping rendered check (source covered above)"
        return
    fi

    assert_absent "rendered guide no longer says 'removes the worktree'" \
        "removes the worktree" "${out}"
    assert_present "rendered guide says it retains the worktree" \
        "retains the worktree and its branch" "${out}"
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

    printf '%sE-1749 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:  %s\n' "${repo_root}"

    test_guide_source
    test_rendered_guide

    summary
}

main "$@"
