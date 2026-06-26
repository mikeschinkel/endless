#!/usr/bin/env bash
#
# E-1368 verification script — proves the endless-go binary self-detects the
# per-worktree sandbox from cwd (replacing the deleted bin-sandbox/ wrappers).
#
# Run from anywhere inside the worktree:
#   esu
#   ./tests/tasks/e-1368-verify.sh
#
# What it checks (against THIS worktree's sandbox):
#   1. Bare `./bin/endless-go` (no XDG_CONFIG_HOME, no --config-dir) inside the
#      worktree opens the DB (the E-1429 gate is satisfied by self-detect) and
#      reads the SANDBOX (sees a probe task created via the Python CLI under
#      --db sandbox).
#   2. An explicit --config-dir still wins: pointed at a throwaway dir, the same
#      bare binary does NOT see the sandbox probe.
#   3. `sandbox bind` no longer creates a bin-sandbox/ directory.
#   4. `sandbox bind` still injects XDG_CONFIG_HOME into .claude/settings.json
#      (retained for the Python CLI's default routing).
#
# The "no-op outside a worktree" / "explicit context skips self-detect" routing
# rules are also covered by Go unit tests in internal/monitor/db_gate_test.go
# (TestSelfDetectWorktreeSandbox); this script is the end-to-end check.
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error. The probe task is left in the sandbox (bounded,
# inspectable via `uv run endless task list --db sandbox`).

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

# ─── command wrappers ───────────────────────────────────────────────────────

# Python CLI, explicitly routed to the sandbox (creates/updates the probe task).
py() {
    uv run endless --db sandbox "$@"
}

# The worktree-built Go binary, invoked with NO XDG_CONFIG_HOME and NO
# --config-dir so its cwd-based self-detect is what does the routing.
gobin() {
    env -u XDG_CONFIG_HOME ./bin/endless-go "$@"
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_succeeds DESC CMD [ARGS...]
assert_succeeds() {
    local desc="$1"
    shift
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -eq 0 ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

# assert_contains DESC PATTERN CMD [ARGS...]
assert_contains() {
    local desc="$1"
    local pattern="$2"
    shift 2
    local output
    output=$("$@" 2>&1)
    if [[ "${output}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output contains: ${pattern}" "${output}"
}

# assert_not_contains DESC PATTERN CMD [ARGS...]
assert_not_contains() {
    local desc="$1"
    local pattern="$2"
    shift 2
    local output
    output=$("$@" 2>&1)
    if [[ "${output}" != *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output does NOT contain: ${pattern}" "${output}"
}

# assert_path_absent DESC PATH
assert_path_absent() {
    local desc="$1"
    local path="$2"
    if [[ ! -e "${path}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "path does not exist: ${path}" "present"
}

# ─── setup ──────────────────────────────────────────────────────────────────

GATE_REFUSAL="refusing to open the database"
PROBE_ID=""
PROBE_NUM=""
MARKER=""

make_probe() {
    MARKER="E1368-PROBE-$$-$(date +%s)"
    local out
    out=$(py task add "Verify E-1368 self-detect probe ${MARKER}" 2>&1)
    if [[ $? -ne 0 ]]; then
        printf 'ERROR: creating probe task failed: %s\n' "${out}" >&2
        exit 2
    fi
    PROBE_ID=$(printf '%s\n' "${out}" | grep -oE 'E-[0-9]+' | head -1)
    PROBE_NUM="${PROBE_ID#E-}"
    if [[ -z "${PROBE_NUM}" ]]; then
        printf 'ERROR: could not parse probe task id from: %s\n' "${out}" >&2
        exit 2
    fi
    # tasks.text is what `session-query task-text` returns; stamp the marker there.
    if ! py task update "${PROBE_ID}" --text "${MARKER}" >/dev/null 2>&1; then
        printf 'ERROR: stamping probe text failed for %s\n' "${PROBE_ID}" >&2
        exit 2
    fi
}

# ─── checks ─────────────────────────────────────────────────────────────────

check_self_detect_routes_to_sandbox() {
    section "1 — bare endless-go self-detects + reads the sandbox"

    assert_succeeds "bare endless-go opens the DB (E-1429 gate satisfied by self-detect)" \
        gobin session-query task-text --id "${PROBE_NUM}"

    assert_not_contains "no gate refusal from a bare invocation" \
        "${GATE_REFUSAL}" \
        gobin session-query task-text --id "${PROBE_NUM}"

    assert_contains "reads the SANDBOX DB (sees the probe task's text)" \
        "${MARKER}" \
        gobin session-query task-text --id "${PROBE_NUM}"
}

check_explicit_config_dir_wins() {
    section "2 — explicit --config-dir overrides self-detect"

    local throwaway
    throwaway=$(mktemp -d)

    # Pointed at an empty throwaway dir, the same bare binary must NOT consult
    # the sandbox — proving the explicit flag wins over self-detect.
    assert_not_contains "explicit --config-dir does NOT see the sandbox probe" \
        "${MARKER}" \
        gobin --config-dir "${throwaway}" session-query task-text --id "${PROBE_NUM}"

    rm -rf "${throwaway}"
}

check_no_bin_sandbox() {
    section "3 — sandbox bind creates no bin-sandbox/ directory"

    local name
    name=$(basename "$(pwd)")
    rm -rf bin-sandbox

    assert_succeeds "sandbox bind succeeds" \
        ./bin/endless-go sandbox bind "$(pwd)" "${name}"

    assert_path_absent "no bin-sandbox/ directory after bind" \
        "$(pwd)/bin-sandbox"
}

check_settings_xdg_retained() {
    section "4 — .claude/settings.json still injects XDG_CONFIG_HOME"

    local name sandbox_dir
    name=$(basename "$(pwd)")
    sandbox_dir="${XDG_CACHE_HOME:-${HOME}/.cache}/endless/sandboxes/${name}"

    assert_contains "settings.json env carries XDG_CONFIG_HOME=<sandbox>" \
        "${sandbox_dir}" \
        cat .claude/settings.json
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
    if [[ ! -x "${repo_root}/bin/endless-go" ]]; then
        printf 'ERROR: %s/bin/endless-go missing — run `just build` first\n' "${repo_root}" >&2
        exit 2
    fi
    case "$(basename "${repo_root}")" in
        e-[0-9]*) ;;
        *)
            printf 'ERROR: not a self-dev worktree (basename must be e-NNN[-slug]): %s\n' "${repo_root}" >&2
            exit 2
            ;;
    esac

    printf '%sE-1368 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  binary:  ./bin/endless-go (worktree build)\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    make_probe
    printf '  probe:   %s (text marker %s)\n' "${PROBE_ID}" "${MARKER}"

    check_self_detect_routes_to_sandbox
    check_explicit_config_dir_wins
    check_no_bin_sandbox
    check_settings_xdg_retained

    summary
}

main "$@"
