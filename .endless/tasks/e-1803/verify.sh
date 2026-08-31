#!/usr/bin/env bash
#
# E-1803 verification script — the report channel enforcement (Arm 1) + coverage
# (Arm 2) reinforcement, plus mid-session usability of `endless task report`.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1803
#
# Single entry point (per E-1596). It ensures binaries are current, runs the Go
# unit suite that pins the hook detection/response shapes, checks the coverage
# rule is documented, and drives `endless task report` at a NON-terminal status
# through the real CLI -> worktree endless-go -> sandbox DB to prove it is usable
# at an arbitrary mid-session checkpoint (not just session end). Exit 0 on
# all-passed, 1 on any failure.
#
# Honest limit (by design): the tests assert the reinforcement FIRES and has the
# right shape. They cannot assert a live model OBEYS it — Claude Code always lets
# the model author its final message and no hook replaces it, so "report authors
# the whole turn" is a strong nudge, not a hard gate. Compliance *visibility* is
# E-1826, not a gate here; there is nothing to check by hand.
#
# Model: .endless/tasks/e-1542/verify.sh.

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

# ─── helpers ────────────────────────────────────────────────────────────────

# Wrap the CLI so every invocation routes through the sandbox DB.
endless() {
    uv run endless "$@" --db sandbox
}

# Create a task and emit just its numeric id on stdout.
add_task_get_id() {
    local title="$1"; shift
    local output rc eid
    output=$(endless task add "${title}" "$@" 2>&1)
    rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        printf 'ERROR: add failed for %q: %s\n' "${title}" "${output}" >&2
        return 1
    fi
    eid=$(printf '%s\n' "${output}" | grep -oE 'E-[0-9]+' | head -1)
    printf '%s\n' "${eid#E-}"
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

# assert_contains DESC PATTERN CMD [ARGS...]
assert_contains() {
    local desc="$1" pattern="$2"; shift 2
    local output
    output=$("$@" 2>&1)
    if [[ "${output}" == *"${pattern}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output contains: ${pattern}" "${output}"
}

# assert_file_contains DESC PATTERN FILE
assert_file_contains() {
    local desc="$1" pattern="$2" file="$3"
    if [[ -f "${file}" ]] && grep -qF -- "${pattern}" "${file}"; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "${file} contains: ${pattern}" "absent"
}

# ─── build + automated suites (fail-fast on the E-1803 unit contract) ────────

test_build_and_suites() {
    section "Build & automated suites"

    if [[ ! -f bin/endless-go ]] || [[ -n "$(find . -name '*.go' -not -path './vendor/*' -newer bin/endless-go 2>/dev/null | head -1)" ]]; then
        assert_succeeds "just build (binaries stale)" just build
    else
        report_pass "binaries up to date (skipping build)"
    fi

    # Fail-fast: the E-1803 unit contract — Arm 1 detector + response shape,
    # Arm 2 coverage-rule composition.
    assert_succeeds "go test hookcmd (E-1803 detection/response/coverage)" \
        go test ./internal/hookcmd/... \
        -run 'TestTaskReportRe|TestReportRelayResponse_Shape|TestComposeSessionStartContext'

    # Full hookcmd package — no regression in the surrounding hook logic.
    assert_succeeds "go test ./internal/hookcmd/... (full package)" \
        go test ./internal/hookcmd/...
}

# ─── coverage rule is documented (Arm 2) ────────────────────────────────────

test_coverage_docs() {
    section "Coverage rule documented (Arm 2)"
    # Functional wording, not an enumerated checklist of situations.
    assert_file_contains "guide broadens report to every checkpoint" \
        "any in-session user-facing checkpoint" docs/guide/tasks.md
    assert_file_contains "guide keeps the functional (non-enumerated) framing" \
        "functional, not" docs/guide/tasks.md
}

# ─── report usable mid-session, non-terminal status (Arm 2 usability) ────────

test_report_mid_session() {
    section "task report at a NON-terminal status (real CLI -> Go -> sandbox)"

    local tid
    if ! tid=$(add_task_get_id "Build e-1803 mid-session report target"); then
        report_fail "seed mid-session report target" "task add succeeds" "add failed"
        return
    fi

    # Leave it at a non-terminal status to prove report is status-agnostic and
    # does not assume session-end.
    assert_succeeds "move target to underway" \
        endless task update "${tid}" --status underway

    # No payload -> no Haiku, computed facts only. It must run cleanly at a
    # non-terminal status and emit a steering prompt (proving it does not assume
    # session-end). Assert on "the user" — present in every steer variant
    # (empty and non-empty) — rather than one variant's exact wording, which the
    # report command tunes over time.
    assert_succeeds "report runs at underway (no payload)" \
        endless task report "${tid}"
    assert_contains "report emits a steering prompt at underway" \
        "the user" endless task report "${tid}"
    assert_contains "report does not change status (still underway)" \
        "underway" endless task show "${tid}"
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

    printf '%sE-1803 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'

    test_build_and_suites
    test_coverage_docs
    test_report_mid_session

    summary
}

main "$@"
