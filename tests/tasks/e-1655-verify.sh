#!/usr/bin/env bash
#
# E-1655 verification script — confirms worktree + sandbox handling is
# collapsed to the canonical `e-<id>` name and that named-alternate `-slug`
# tolerance is gone (ED-1515).
#
# The change spans both languages:
#   - Go:  WorktreePathForTask (internal/monitor/worktree.go) resolves ONLY a
#          bare `e-<id>` dir; the regexes in worktree_lock.go (TaskIDFromWorktreePath)
#          and db.go (worktreeDirName) drop the optional `-slug`.
#   - Py:  config.py collapses sandbox naming to `e-<id>` (worktree_dir_name;
#          worktree_task_id removed) and worktree_cmd.py's _WORKTREE_TASK_ID_RE
#          drops the suffix.
#
# Each behavior is exercised against the REAL code path by the existing Go and
# Python tests — this script runs each as a named check so a single command
# tells Mike pass/fail per case.
#
# Run from anywhere inside the worktree (esu cd's here and exports the session):
#   ./tests/tasks/e-1655-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on environment/setup error.

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

MONITOR_PKG="./internal/monitor/"

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
    local detail="$2"
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "${desc}"
    printf '      %sdetail:%s %s\n' "${DIM}" "${RESET}" "${detail}"
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

# assert_cmd DESC CMD [ARGS...]
#   Pass if CMD exits 0. On failure, report the tail of its combined output.
assert_cmd() {
    local desc="$1"
    shift
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "exit=${rc} | $(printf '%s' "${output}" | tail -3 | tr '\n' '⏎')"
}

# assert_go_test DESC TESTNAME
#   Run exactly one Go test in the monitor package (fresh, uncached).
assert_go_test() {
    local desc="$1"
    local name="$2"
    assert_cmd "${desc}" \
        go test -count=1 -run "^${name}$" "${MONITOR_PKG}"
}

# assert_py_test DESC NODEID
#   Run exactly one pytest node and pass iff it succeeds.
assert_py_test() {
    local desc="$1"
    local node="$2"
    assert_cmd "${desc}" \
        uv run pytest -q "${node}"
}

# ─── checks ─────────────────────────────────────────────────────────────────

test_compiles() {
    section "Build — packages compile"
    assert_cmd "internal/monitor compiles" go build "${MONITOR_PKG}"
}

test_go_resolution() {
    section "Go — only the canonical bare e-<id> resolves"

    assert_go_test "WorktreePathForTask resolves a bare e-<id> dir" \
        "TestWorktreePathForTask_FindsBareDir"
    assert_go_test "WorktreePathForTask IGNORES a named-alternate e-<id>-slug dir" \
        "TestWorktreePathForTask_IgnoresSluggedDir"
    assert_go_test "TaskIDFromWorktreePath matches only bare e-<id>" \
        "TestTaskIDFromWorktreePath_BasicMatch"
    assert_go_test "selfDevProjectRoot rejects a named-alternate dir" \
        "TestSelfDevProjectRoot"
    assert_go_test "worktreeDirName returns bare e-<id>, rejects slug" \
        "TestWorktreeDirName"
    assert_go_test "SelfDetectWorktreeSandbox no-ops on a named-alternate dir" \
        "TestSelfDetectWorktreeSandbox"
}

test_py_resolution() {
    section "Python — sandbox naming + path regex collapsed to e-<id>"

    assert_py_test "worktree_dir_name returns bare e-<id>, rejects slug" \
        "tests/test_db_gate.py::test_worktree_dir_name_detects_segment"
    assert_py_test "--db sandbox derives the sandbox dir from e-<id>" \
        "tests/test_db_gate.py::test_apply_db_choice_sandbox"
    assert_py_test "_task_id_from_worktree_path ignores a named-alternate dir" \
        "tests/test_worktree_create.py::test_task_id_from_worktree_path_ignores_named_alternate"
    assert_py_test "re-exec gate does not fire from a named-alternate dir" \
        "tests/test_cli_python_reexec.py::test_reexec_target_ignores_named_alternate_worktree"
    assert_py_test "endless-go resolver ignores a named-alternate dir" \
        "tests/test_event_bridge_worktree_binary.py::test_resolver_helper_ignores_named_alternate"
}

test_no_regression() {
    section "Regression — full suites stay green"

    assert_cmd "internal/monitor full suite passes" \
        go test -count=1 "${MONITOR_PKG}"
    assert_cmd "Python db-gate suite passes" \
        uv run pytest -q tests/test_db_gate.py
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

    if ! command -v go >/dev/null 2>&1; then
        printf 'ERROR: go not on PATH\n' >&2
        exit 2
    fi

    # Worktrees need a go.work pointing at the local go-pkgs/ modules; without it
    # the replace directives resolve at the wrong depth and the build fails.
    if [[ ! -f "${repo_root}/go.work" ]]; then
        if command -v just >/dev/null 2>&1; then
            just go-work-init >/dev/null 2>&1
        fi
        if [[ ! -f "${repo_root}/go.work" ]]; then
            printf 'ERROR: go.work missing and could not be generated (run: just go-work-init)\n' >&2
            exit 2
        fi
    fi

    printf '%sE-1655 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  go:      %s\n' "$(go version 2>&1 | awk '{print $3}')"

    test_compiles
    test_go_resolution
    test_py_resolution
    test_no_regression

    summary
}

main "$@"
