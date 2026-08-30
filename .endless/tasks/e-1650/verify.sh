#!/usr/bin/env bash
#
# E-1650 verification script — `esp` returns to the project root with no
# active Claude session (the cwd-based fallback in `session cd --target
# project`).
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1650-verify.sh
#
# Single entry point for verifying E-1650 (per E-1596). It runs the Python
# suites that cover the new behavior, asserts the regenerated shell-init no
# longer guards `esp` on a session, and drives `session cd --target project`
# (no session-ref) end-to-end to confirm it now resolves the project root from
# cwd instead of erroring. Output: pass/fail per check, then a summary. Exit 0
# on all-passed, 1 on any failure. No irreducible manual step.
#
# Model: tests/tasks/e-1542-verify.sh (the E-1596 reference prototype).

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
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
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
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
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}"
    printf '\n  %sFAILED:%s\n' "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do
        printf '    - %s\n' "${t}"
    done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_succeeds DESC CMD [ARGS...]
assert_succeeds() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

# assert_eq DESC EXPECTED ACTUAL  (literal string equality)
assert_eq() {
    local desc="$1" expected="$2" actual="$3"
    if [[ "${actual}" == "${expected}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${expected}" "${actual}"
}

# assert_absent DESC NEEDLE HAYSTACK  (NEEDLE must NOT appear in HAYSTACK)
assert_absent() {
    local desc="$1" needle="$2" haystack="$3"
    if [[ "${haystack}" != *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "absent: ${needle}" "present"
}

# assert_present DESC NEEDLE HAYSTACK  (NEEDLE must appear in HAYSTACK)
assert_present() {
    local desc="$1" needle="$2" haystack="$3"
    if [[ "${haystack}" == *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "present: ${needle}" "absent"
}

# ─── build + automated suites ───────────────────────────────────────────────

test_build_and_suites() {
    section "Build & automated suites"

    # The checks below are pure-Python (CliRunner + session_cmd, which falls
    # back gracefully when endless-go is absent), so a full build isn't
    # required — but rebuild if the Go binaries are missing/stale to keep the
    # script self-sufficient when run in a fresh worktree.
    if [[ ! -f bin/endless-go ]] || [[ -n "$(find . -name '*.go' -not -path './vendor/*' -newer bin/endless-go 2>/dev/null | head -1)" ]]; then
        assert_succeeds "just build (binaries stale)" just build
    else
        report_pass "binaries up to date (skipping build)"
    fi

    # Python: the cwd-based project-root resolution + the shell-init guard drop.
    assert_succeeds "pytest tests/test_session_cd.py tests/test_shell_init.py" \
        uv run pytest tests/test_session_cd.py tests/test_shell_init.py -q
}

# ─── shell-init content (worktree source string) ────────────────────────────

test_shell_init() {
    section "shell-init: esp guard dropped, esf guard kept"

    local out
    out=$(uv run endless shell-init 2>&1)

    assert_present "esp() is defined" "esp()" "${out}"
    # The whole point of E-1650: esp no longer refuses without a session.
    assert_absent "esp no longer emits 'no active session'" \
        "esp: no active session" "${out}"
    # esf genuinely needs a session and keeps its guard.
    assert_present "esf still guards on a session" \
        "esf: no active session" "${out}"
}

# ─── behavior E2E (the regression itself) ───────────────────────────────────

test_project_root_fallback() {
    section "E2E — session cd --target project resolves cwd's project root"

    # The main checkout is this worktree's git common dir's parent. The real
    # ledger registers the endless project at that path, so resolving the
    # project root from the worktree cwd should return it. This is read-only
    # (session cd performs no writes) so we run it against --db main, exactly
    # as the live `esp` helper does — no sandbox needed, no ledger pollution.
    local common real_root out rc
    common=$(git rev-parse --git-common-dir 2>/dev/null)
    real_root=$(cd "$(dirname "${common}")" 2>/dev/null && pwd)

    if [[ -z "${real_root}" || ! -d "${real_root}" ]]; then
        report_fail "resolve main checkout path" "an existing directory" "${real_root}"
        return
    fi

    # Subshell so unsetting ENDLESS_SESSION_ID (exported by esu) doesn't leak.
    out=$( unset ENDLESS_SESSION_ID
           uv run endless session cd --target project --db main 2>/dev/null )
    rc=$?

    # Core regression: previously this errored ("no active session" / "Outside
    # tmux, an explicit session id is required"); now it exits 0.
    assert_eq "exits 0 with no session and no session-ref" "0" "${rc}"
    # And resolves to the project root (the main checkout) via the cwd walk-up.
    assert_eq "stdout is the project root" "${real_root}" "${out}"
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

    printf '%sE-1650 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"

    test_build_and_suites
    test_shell_init
    test_project_root_fallback

    summary
}

main "$@"
