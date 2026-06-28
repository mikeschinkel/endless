#!/usr/bin/env bash
#
# E-986 land-readiness gate — pluggable post-worktree-create hook.
#
# Run from inside the worktree:
#   esu
#   ./tests/tasks/e-986-verify.sh
#
# This is a LAND-READINESS GATE, not a test suite: run it to decide whether
# E-986 is ready to land. It must therefore exercise the CANDIDATE code on this
# branch, not whatever `endless` happens to be installed globally. It does that
# two ways, both self-contained:
#
#   1. Unit layer — `uv run pytest tests/test_worktree_create.py` against the
#      worktree source (the hook runner's discovery/exec/cwd/arg/failure logic).
#   2. End-to-end layer — drives the worktree's own editable `endless`
#      (<worktree>/.venv/bin/endless, which imports <worktree>/src/endless) through
#      `task add` + `task claim` and asserts the hook actually fired.
#
# The candidate binary is the worktree's editable venv install; the script
# creates it with `uv sync` if absent. Everything runs against a throwaway git
# project under a temp XDG_CONFIG_HOME/XDG_CACHE_HOME, so the real ledger and the
# worktree's own sandbox are never touched. cwd stays in the temp (non-self-dev)
# project so the self-dev --db gate doesn't apply. The temp tree is removed on exit.
#
# Because this gates landing, it is GREEN pre-land when the code is correct. Once
# E-986 has landed this script's job is done; if it later stops working (the
# worktree's venv is gone, paths drift) that's fine — it's kept for posterity.
#
# Output: pass/fail per check + summary. Exit 0 all-passed, 1 any failure, 2 setup error.

set -u

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="--------------------------------------------------------------"

section()     { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s[ok]%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT+1)); }
report_fail() {
    printf '  %s[XX]%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT+1)); FAILED_TESTS+=("$1")
}
summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'; return 1
}

assert_file_contains() {
    local desc="$1" file="$2" needle="$3"
    if [[ -f "${file}" ]] && grep -qF -- "${needle}" "${file}"; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "file ${file} contains: ${needle}" "$( [[ -f ${file} ]] && cat "${file}" || echo MISSING )"
}
assert_path_exists() { [[ -e "$2" ]] && { report_pass "$1"; return; }; report_fail "$1" "path exists: $2" "absent"; }
assert_contains()    { [[ "$2" == *"$3"* ]] && { report_pass "$1"; return; }; report_fail "$1" "output contains: $3" "$2"; }

WORKTREE_ROOT=""
CANDIDATE_ENDLESS=""
TMP=""
cleanup() { [[ -n "${TMP}" && -d "${TMP}" ]] && rm -rf "${TMP}"; }
trap cleanup EXIT

# All `endless` calls in the E2E checks route through the worktree's candidate
# build, never the globally-installed one.
endless() { "${CANDIDATE_ENDLESS}" "$@"; }

LAST_ID=""; LAST_WT=""; LAST_OUT=""
claim_new_worktree() {
    LAST_OUT="$(endless task add "$1" 2>&1)"
    LAST_ID="$(printf '%s\n' "${LAST_OUT}" | grep -oE 'E-[0-9]+' | head -1)"
    [[ -z "${LAST_ID}" ]] && { LAST_WT=""; return 1; }
    LAST_OUT="$(endless task claim "${LAST_ID}" --no-session 2>&1)"
    LAST_WT="$(endless worktree for-task "${LAST_ID}" 2>/dev/null | tr -d '[:space:]')"
    return 0
}

write_hook() {
    mkdir -p "${PROJ}/.endless/hooks"
    printf '%s\n' "$1" > "${PROJ}/.endless/hooks/post-worktree-create.sh"
    chmod +x "${PROJ}/.endless/hooks/post-worktree-create.sh"
}
rm_hook() { rm -f "${PROJ}/.endless/hooks/post-worktree-create.sh"; }

# --- T0: candidate runner logic (unit layer) -------------------------------
test_unit_suite() {
    section "T0 - candidate hook-runner unit tests (pytest, worktree source)"
    local out
    if out="$(cd "${WORKTREE_ROOT}" && uv run pytest tests/test_worktree_create.py -q 2>&1)"; then
        report_pass "tests/test_worktree_create.py passes against worktree source"
    else
        report_fail "tests/test_worktree_create.py passes against worktree source" \
            "pytest exit 0" "$(printf '%s' "${out}" | tail -3)"
    fi
}

test_hook_runs_with_arg_and_cwd() {
    section "T1 - hook runs with worktree path as arg and cwd"
    local sentinel="${TMP}/sentinel-t1"
    write_hook "#!/usr/bin/env bash
printf 'ARG=%s\n' \"\$1\" > '${sentinel}'
printf 'CWD=%s\n' \"\$(pwd)\" >> '${sentinel}'"
    claim_new_worktree "Verify E-986 hook arg/cwd" || { report_fail "claim created a worktree" "a worktree" "claim failed: ${LAST_OUT}"; return; }
    assert_path_exists "claim created the worktree" "${LAST_WT}"
    assert_file_contains "hook ran with worktree path as \$1" "${sentinel}" "ARG=${LAST_WT}"
    assert_file_contains "hook ran with cwd = worktree" "${sentinel}" "CWD=${LAST_WT}"
}

test_failure_non_fatal_and_loud() {
    section "T2 - hook failure is non-fatal and loud (worktree kept)"
    write_hook "#!/usr/bin/env bash
echo 'boom from post-worktree-create' >&2
exit 1"
    claim_new_worktree "Verify E-986 hook failure non-fatal" || { report_fail "claim still created a worktree" "a worktree" "claim failed: ${LAST_OUT}"; return; }
    assert_path_exists "worktree still created despite hook failure" "${LAST_WT}"
    assert_contains "claim surfaced a loud hook-failure warning" "${LAST_OUT}" "post-worktree-create"
}

test_no_hook_unaffected() {
    section "T3 - no hook present -> creation proceeds unchanged"
    rm_hook
    claim_new_worktree "Verify E-986 no hook" || { report_fail "claim created a worktree with no hook" "a worktree" "claim failed: ${LAST_OUT}"; return; }
    assert_path_exists "worktree created with no hook present" "${LAST_WT}"
}

test_endless_ships_its_hook() {
    section "T4 - endless ships its own post-worktree-create.sh"
    assert_path_exists "endless repo has .endless/hooks/post-worktree-create.sh" \
        "${WORKTREE_ROOT}/.endless/hooks/post-worktree-create.sh"
    [[ -x "${WORKTREE_ROOT}/.endless/hooks/post-worktree-create.sh" ]] \
        && report_pass "endless's shipped hook is executable" \
        || report_fail "endless's shipped hook is executable" "executable bit set" "not executable"
}

resolve_candidate_endless() {
    CANDIDATE_ENDLESS="${WORKTREE_ROOT}/.venv/bin/endless"
    if [[ ! -x "${CANDIDATE_ENDLESS}" ]]; then
        echo "  building candidate venv (uv sync) ..."
        ( cd "${WORKTREE_ROOT}" && uv sync --quiet ) >/dev/null 2>&1 || true
    fi
    [[ -x "${CANDIDATE_ENDLESS}" ]]
}

main() {
    WORKTREE_ROOT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -z "${WORKTREE_ROOT}" ]] && { echo "ERROR: run from inside the worktree" >&2; exit 2; }
    command -v uv >/dev/null 2>&1 || { echo "ERROR: uv not on PATH (needed for the candidate build + pytest)" >&2; exit 2; }
    resolve_candidate_endless || { echo "ERROR: could not build the candidate endless at ${WORKTREE_ROOT}/.venv/bin/endless" >&2; exit 2; }

    TMP="$(mktemp -d)"
    export XDG_CONFIG_HOME="${TMP}/config" XDG_CACHE_HOME="${TMP}/cache"
    mkdir -p "${XDG_CONFIG_HOME}" "${XDG_CACHE_HOME}"

    PROJ="${TMP}/proj"
    mkdir -p "${PROJ}/.endless"
    printf '{"name": "e986-verify"}\n' > "${PROJ}/.endless/config.json"
    git -C "${PROJ}" init -q -b main
    git -C "${PROJ}" config user.email t@e.x; git -C "${PROJ}" config user.name t
    git -C "${PROJ}" config commit.gpgsign false
    printf 'x\n' > "${PROJ}/README"; git -C "${PROJ}" add -A; git -C "${PROJ}" commit -q -m init
    cd "${PROJ}" || exit 2
    endless register "${PROJ}" --infer --name e986-verify --status active >/dev/null 2>&1 || true

    printf '%sE-986 land-readiness gate%s  (verifies the candidate build on this branch)\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  candidate:    %s\n  temp project: %s\n' "${CANDIDATE_ENDLESS}" "${PROJ}"

    test_unit_suite
    test_hook_runs_with_arg_and_cwd
    test_failure_non_fatal_and_loud
    test_no_hook_unaffected
    test_endless_ships_its_hook
    summary
}

main "$@"
