#!/usr/bin/env bash
#
# E-1819 verification — `endless task add` accepts --analysis (inline) and
# --analysis-file (path), persisting to the task's analysis field at creation,
# for parity with `endless task update`.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-1819
#
# Two layers:
#   0. FAIL-FAST unit layer: the pytest that mirrors the update-analysis test
#      (tests/test_task_add_analysis.py). If it fails, stop before the slower
#      end-to-end layer.
#   1. End-to-end CLI layer, driven against a FULLY ISOLATED throwaway env
#      (temp XDG_CONFIG_HOME + fresh DB, temp git project) using the CANDIDATE
#      Python CLI (`.venv/bin/endless`) and worktree-built `bin/endless-go`.
#      Nothing touches the real ledger, the real repo, or this worktree branch —
#      teardown is `rm -rf`.
#
# What the end-to-end layer checks:
#   1. `task add "X" --analysis M` -> `task show <id> --analysis` shows M
#   2. `task add "X" --analysis-file f` -> show shows the file content
#   3. `task add "X" --analysis A --analysis-file f` -> errors ("not both")
#   4. regression: `task add "X" --text T` -> plan still persists (show --text)
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

# assert_shows DESC TASK_ID SHOW_FLAG PATTERN — `task show <id> <flag>` contains PATTERN.
assert_shows() {
    local desc="$1" id="$2" flag="$3" pat="$4" out
    out=$(en task show "${id}" "${flag}" 2>&1)
    if printf '%s' "${out}" | grep -q -- "${pat}"; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "show ${flag} contains: ${pat}" "${out}"
}

# assert_errors DESC PATTERN CMD... — command exits non-zero AND output matches PATTERN.
assert_errors() {
    local desc="$1" pat="$2"; shift 2
    local out rc
    out=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]] && printf '%s' "${out}" | grep -q -- "${pat}"; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "exit != 0 and output ~ ${pat}" "exit=${rc} | ${out}"
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

add_task() {
    local out
    out=$(en task add "$@" 2>&1) || { printf '%s' "${out}"; return 1; }
    printf '%s\n' "${out}" | grep -oE 'E-[0-9]+' | head -1
}

run_unit_layer() {
    section "0 — fail-fast unit layer (pytest)"
    local out rc
    out=$( cd "${REPO_ROOT}" && uv run pytest -q tests/test_task_add_analysis.py 2>&1 ); rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "tests/test_task_add_analysis.py"
        return 0
    fi
    report_fail "tests/test_task_add_analysis.py" "pytest exit == 0" "${out}"
    summary
    exit 1
}

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    EN="${REPO_ROOT}/.venv/bin/endless"
    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    [[ -x "${REPO_ROOT}/bin/endless-go" ]] || {
        printf 'ERROR: %s/bin/endless-go missing — run `just build`\n' "${REPO_ROOT}" >&2; exit 2; }
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
    git -C "${PROJ}" add README.md
    git -C "${PROJ}" commit -q -m "init"

    en project register "${PROJ}" --infer --name verify1819 --status active >/dev/null 2>&1 || {
        printf 'ERROR: registering temp project failed\n' >&2; exit 2; }
}

# ─── checks ─────────────────────────────────────────────────────────────────

check_add_analysis() {
    section "1–4 — task add --analysis / --analysis-file"

    local id1
    id1=$(add_task "Inline analysis task" --analysis "ANALYSIS-INLINE-MARKER") || {
        report_fail "task add --analysis returns an id" "E-NNN" "${id1}"; return; }
    report_pass "task add --analysis returns an id (${id1})"
    assert_shows "inline analysis persisted (show --analysis)" \
        "${id1}" "--analysis" "ANALYSIS-INLINE-MARKER"

    local afile="${WORK}/analysis.md"
    printf 'ANALYSIS-FROM-FILE-MARKER\nsecond line\n' > "${afile}"
    local id2
    id2=$(add_task "File analysis task" --analysis-file "${afile}") || {
        report_fail "task add --analysis-file returns an id" "E-NNN" "${id2}"; return; }
    report_pass "task add --analysis-file returns an id (${id2})"
    assert_shows "analysis-file content persisted (show --analysis)" \
        "${id2}" "--analysis" "ANALYSIS-FROM-FILE-MARKER"

    assert_errors "task add --analysis + --analysis-file errors" "not both" \
        en task add "Both forms" --analysis "x" --analysis-file "${afile}"

    # Regression: the existing --text plan path on `task add` still persists.
    local id3
    id3=$(add_task "Text regression task" --text "PLAN-TEXT-MARKER") || {
        report_fail "task add --text returns an id" "E-NNN" "${id3}"; return; }
    report_pass "task add --text returns an id (${id3})"
    assert_shows "text still persisted (show --text)" \
        "${id3}" "--text" "PLAN-TEXT-MARKER"
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup
    run_unit_layer

    printf '\n%sE-1819 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  cli:     %s\n' "${EN}"
    printf '  go:      %s/bin/endless-go\n' "${REPO_ROOT}"
    printf '  env:     isolated (%s)\n' "${WORK}"

    check_add_analysis

    summary
}

main "$@"
