#!/usr/bin/env bash
#
# E-1781 verification script — the single verification command for this task.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1781-verify.sh
#
# Asserts the deliverables of E-1781 — removal of the `session activity`
# command — against the worktree's OWN source (not the main checkout):
#
#   1. The command is gone: the worktree-built Python CLI's `session --help`
#      exits 0 and does not list `activity`, and `session activity` errors as
#      an unknown subcommand.
#   2. src/endless/session_activity.py is deleted.
#   3. src/endless/ has no remaining `session_activity` / `run_activity` ref.
#   4. internal/tmuxcmd/show_menu.go has no `Session Activity` / `session
#      activity` menu item, and `go build ./cmd/endless-go` succeeds.
#   5. event_bridge.py's session_id comment no longer mentions "activity".
#   6. Fold-in regression (fail-fast): `go test ./internal/tmuxcmd/...` and a
#      targeted `uv run pytest` over the session CLI tests pass. (Targeted,
#      not full `just test`.)
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure.
#
# Interim ad-hoc location tests/tasks/ (matches the prototype convention);
# migrates to .endless/tasks/<id>/ once the manifest runner lands.

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

REPO_ROOT=""
EN=""

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

# assert_succeeds DESC CMD [ARGS...]  — pass if CMD exits 0.
assert_succeeds() {
    local desc="$1"; shift
    local out; out=$("$@" 2>&1); local rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | ${out}"
}

# assert_absent DESC PATH  — filesystem PATH does not exist.
assert_absent() {
    local desc="$1"; local path="$2"
    if [[ ! -e "${path}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "absent" "present: ${path}"
}

# assert_no_ref DESC DIR PATTERN  — no file under DIR contains PATTERN.
assert_no_ref() {
    local desc="$1"; local dir="$2"; local pat="$3"
    local hits; hits=$(grep -rlF -- "${pat}" "${dir}" 2>/dev/null)
    if [[ -z "${hits}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "no reference to '${pat}'" "${hits}"
}

# assert_file_no_ref DESC FILE PATTERN  — FILE does not contain PATTERN (case-insensitive).
assert_file_no_ref() {
    local desc="$1"; local file="$2"; local pat="$3"
    if grep -qiF -- "${pat}" "${file}" 2>/dev/null; then
        report_fail "${desc}" "no '${pat}' in ${file}" "present"; return
    fi
    report_pass "${desc}"
}

# ─── checks ─────────────────────────────────────────────────────────────────

check_command_gone() {
    section "1 — session-activity command removed from the CLI"

    local help rc
    help=$(cd "${REPO_ROOT}" && "${EN}" session --help 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "session --help exits 0"
    else report_fail "session --help exits 0" "exit 0" "exit ${rc}
${help}"; fi

    if [[ "${help}" != *activity* ]]; then
        report_pass "session --help does not list 'activity'"
    else
        report_fail "session --help does not list 'activity'" "absent" "present"
    fi

    local out
    out=$(cd "${REPO_ROOT}" && "${EN}" session activity 2>&1); rc=$?
    if [[ "${rc}" -ne 0 && "${out}" == *"No such command 'activity'"* ]]; then
        report_pass "session activity errors as an unknown subcommand"
    else
        report_fail "session activity errors as an unknown subcommand" \
            "nonzero exit + \"No such command 'activity'\"" "exit ${rc} | ${out}"
    fi
}

check_module_gone() {
    section "2 — session_activity.py deleted, no dangling references"
    assert_absent "src/endless/session_activity.py does not exist" \
        "${REPO_ROOT}/src/endless/session_activity.py"
    assert_no_ref "src/endless/ has no 'session_activity' reference" \
        "${REPO_ROOT}/src/endless" "session_activity"
    assert_no_ref "src/endless/ has no 'run_activity' reference" \
        "${REPO_ROOT}/src/endless" "run_activity"
}

check_tmux_menu() {
    section "3 — tmux menu item dropped, endless-go still builds"
    local menu="${REPO_ROOT}/internal/tmuxcmd/show_menu.go"
    assert_file_no_ref "show_menu.go has no 'Session Activity' item" \
        "${menu}" "Session Activity"
    assert_file_no_ref "show_menu.go has no 'session activity' invocation" \
        "${menu}" "session activity"
    ( cd "${REPO_ROOT}" && go build -o bin/endless-go ./cmd/endless-go )
    assert_succeeds "go build ./cmd/endless-go" \
        bash -c "cd '${REPO_ROOT}' && go build -o bin/endless-go ./cmd/endless-go"
}

check_comment_reworded() {
    section "4 — event_bridge.py comment no longer cites 'activity'"
    # The emit() docstring paragraph that documents session_id must not use
    # the word "activity" anymore (the population behavior stays).
    local file="${REPO_ROOT}/src/endless/event_bridge.py"
    local para
    para=$(awk '/`session_id` populates/{p=1} p{print} /resolver falls back/{p=0}' "${file}")
    if [[ -n "${para}" && "${para}" != *[Aa]ctivity* ]]; then
        report_pass "session_id comment paragraph drops the 'activity' rationale"
    else
        report_fail "session_id comment paragraph drops the 'activity' rationale" \
            "no 'activity' in the session_id paragraph" "${para}"
    fi
}

check_regression() {
    section "5 — fold-in regression (Go + targeted Python)"
    assert_succeeds "go test ./internal/tmuxcmd/..." \
        bash -c "cd '${REPO_ROOT}' && go test ./internal/tmuxcmd/..."
    assert_succeeds "uv run pytest (session CLI suite)" \
        bash -c "cd '${REPO_ROOT}' && uv run pytest -q \
            tests/test_cli.py \
            tests/test_session_show.py \
            tests/test_session_resolver.py"
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2; exit 2
    fi
    cd "${REPO_ROOT}" || exit 2

    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }

    printf '%sE-1781 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  repo:   %s\n' "${REPO_ROOT}"

    # Materialize the worktree's own venv so the CLI under test is this
    # branch's source (the global `endless` reads the main checkout).
    EN="${REPO_ROOT}/.venv/bin/endless"
    if [[ ! -x "${EN}" ]]; then
        ( cd "${REPO_ROOT}" && uv run endless --version >/dev/null 2>&1 ) || {
            printf 'ERROR: could not materialize .venv (uv run endless failed)\n' >&2
            exit 2; }
    fi

    check_command_gone
    check_module_gone
    check_tmux_menu
    check_comment_reworded
    check_regression

    summary
}

main "$@"
