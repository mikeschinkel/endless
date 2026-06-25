#!/usr/bin/env bash
#
# E-1657 verification script — confirms `brainstorm` is a first-class task type:
# the requester-led sibling of research (ED-1516).
#
# Covers all three layers the change touches:
#   1. Go enum (internal/tasktype): TaskTypeBrainstorm=5 + the task_types/enum
#      integrity check, exercised by `go test ./internal/tasktype/`.
#   2. Python CLI + gates (against the worktree's sandbox DB): ungated creation,
#      the terminal-status gate (brainstorm rejects unverified/assumed/confirmed),
#      and the completable-verb exemption (brainstorm completes via `completed`
#      even with a non-completable title verb, like epics — ED-1511).
#   3. The interview-mode handoff variant and the guide's behavioral contract.
#
# It also runs no-regression controls: a plain task with a non-completable verb
# is STILL blocked from `completed` (exemption is scoped to brainstorm), and the
# research justification gate still fires.
#
# Run from anywhere inside the worktree (esu cd's here and exports the session):
#   esu && ./tests/tasks/e-1657-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on environment/setup error. A fresh task id is allocated per run;
# the script does NOT wipe the sandbox between runs (pollution is bounded and
# inspectable via `uv run endless task list --all --db sandbox`).

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

TASKTYPE_PKG="./internal/tasktype/"

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

# ─── helpers ────────────────────────────────────────────────────────────────

# Wrap the CLI so every invocation routes through the sandbox DB and runs the
# WORKTREE source (uv run resolves endless from the worktree's editable install).
# When ENDLESS_SESSION_ID is unset (script run without `esu`), add --no-session
# so write commands don't hit the session-attribution gate; it's consumed
# globally and is a harmless no-op on read commands.
endless() {
    if [[ -n "${ENDLESS_SESSION_ID:-}" ]]; then
        uv run endless "$@" --db sandbox
    else
        uv run endless "$@" --db sandbox --no-session
    fi
}

# Create a task and emit just its E-NNN id on stdout; other output to stderr.
add_task_get_id() {
    local title="$1"
    shift
    local output
    output=$(endless task add "${title}" "$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        printf 'ERROR: add failed for %q: %s\n' "${title}" "${output}" >&2
        return 1
    fi
    printf '%s\n' "${output}" | grep -oE 'E-[0-9]+' | head -1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_cmd DESC CMD [ARGS...] — pass iff CMD exits 0.
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
    report_fail "${desc}" "exit == 0" \
        "exit=${rc} | $(printf '%s' "${output}" | tail -3 | tr '\n' '⏎')"
}

# assert_refused DESC PATTERN CMD [ARGS...] — pass iff non-zero exit AND output
# contains PATTERN.
assert_refused() {
    local desc="$1"
    local pattern="$2"
    shift 2
    local output
    output=$("$@" 2>&1)
    local rc=$?
    if [[ "${rc}" -ne 0 ]] && [[ "${output}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" \
        "exit != 0 AND output contains: ${pattern}" \
        "exit=${rc} | output=${output}"
}

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

# ─── 1. Go enum ─────────────────────────────────────────────────────────────

test_go_enum() {
    section "Go enum — TaskTypeBrainstorm=5 + task_types/enum integrity"

    assert_cmd "internal/tasktype compiles" go build "${TASKTYPE_PKG}"
    assert_cmd "internal/tasktype tests pass (Parse/String/All + VerifyIntegrity)" \
        go test -count=1 "${TASKTYPE_PKG}"
}

# ─── 2. ungated creation ────────────────────────────────────────────────────

test_ungated_create() {
    section "Create — brainstorm is ungated (no --justification, unlike research)"

    assert_succeeds "brainstorm task adds with no justification" \
        endless task add "Explore the membership-tiers idea" --type brainstorm

    # The research gate must still fire (no-regression control).
    assert_refused "research STILL requires --justification" \
        "requires --justification" \
        endless task add "Compare alpha vs beta" --type research
}

# ─── 3. terminal-status gate ────────────────────────────────────────────────

test_terminal_gate() {
    section "Terminal gate — brainstorm rejects the verification track"

    local bid
    bid=$(add_task_get_id "Explore brainstorm-gate" --type brainstorm) || return

    assert_refused "brainstorm rejects 'task update --status unverified'" \
        "'unverified'" endless task update "${bid}" --status unverified
    assert_refused "brainstorm rejects 'task update --status assumed'" \
        "'assumed'" endless task update "${bid}" --status assumed
    assert_refused "brainstorm rejects 'task update --status confirmed'" \
        "'confirmed'" endless task update "${bid}" --status confirmed
    assert_refused "brainstorm rejects 'task confirm'" \
        "'confirmed'" endless task confirm "${bid}"
    assert_refused "brainstorm rejects 'task assume'" \
        "'assumed'" endless task assume "${bid}"
}

# ─── 4. completable-verb exemption ──────────────────────────────────────────

test_completed_exemption() {
    section "Complete — brainstorm is exempt from the completable-verb gate"

    # Title verb 'Add' is registered but NOT completable. A plain task with this
    # verb cannot reach 'completed' (control below); a brainstorm can, because
    # the type itself signals an information deliverable (mirrors epic, ED-1511).
    local bid
    bid=$(add_task_get_id "Add the loyalty-program concept" --type brainstorm) || return
    assert_succeeds "brainstorm completes via 'completed --outcome' despite non-completable verb" \
        endless task update "${bid}" --status completed \
            --outcome "Synthesis: tiers = free/pro/team; spawned follow-ups."

    # Outcome is still required (universal gate).
    local bid2
    bid2=$(add_task_get_id "Add the referral-flow concept" --type brainstorm) || return
    assert_refused "brainstorm 'completed' without --outcome is refused" \
        "outcome is required" \
        endless task update "${bid2}" --status completed

    # No-regression control: the exemption is scoped to brainstorm. A plain task
    # with the same non-completable verb is STILL blocked.
    local tid
    tid=$(add_task_get_id "Add a control widget" --type task) || return
    assert_refused "plain task with non-completable verb STILL blocked from 'completed'" \
        "completable lead verb" \
        endless task update "${tid}" --status completed --outcome "x"
}

# ─── 5. interview-mode handoff ──────────────────────────────────────────────

test_handoff_variant() {
    section "Handoff — brainstorm renders the interview-mode variant"

    local bid
    bid=$(add_task_get_id "Explore brainstorm-handoff" --type brainstorm) || return

    assert_contains "handoff opens in interview mode" \
        "interviewing the requester" \
        endless task handoff "${bid}"
    assert_contains "handoff labels the task requester-led / interview-mode" \
        "requester-led, interview-mode" \
        endless task handoff "${bid}"
    # Must NOT fall back to the research variant's framing.
    assert_not_contains "handoff is NOT the research variant" \
        "Findings are the deliverable" \
        endless task handoff "${bid}"
}

# ─── 6. guide behavioral contract ───────────────────────────────────────────

test_guide_contract() {
    section "Guide — the brainstorm behavioral contract ships in 'guide tasks'"

    assert_contains "'guide tasks' documents --type brainstorm" \
        "Brainstorm tasks" \
        endless guide tasks
    assert_contains "'guide tasks' states the interview-first contract" \
        "Open by interviewing the requester" \
        endless guide tasks
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

    local missing=0
    command -v go >/dev/null 2>&1 || { printf 'ERROR: go not on PATH\n' >&2; missing=1; }
    command -v uv >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; missing=1; }
    [[ "${missing}" -eq 0 ]] || exit 2

    # Worktrees need a go.work pointing at local go-pkgs/ modules. Generate on
    # demand so the script is self-contained.
    if [[ ! -f "${repo_root}/go.work" ]]; then
        command -v just >/dev/null 2>&1 && just go-work-init >/dev/null 2>&1
        if [[ ! -f "${repo_root}/go.work" ]]; then
            printf 'ERROR: go.work missing and could not be generated (run: just go-work-init)\n' >&2
            exit 2
        fi
    fi

    printf '%sE-1657 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  go:      %s\n' "$(go version 2>&1 | awk '{print $3}')"
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_go_enum
    test_ungated_create
    test_terminal_gate
    test_completed_exemption
    test_handoff_variant
    test_guide_contract

    summary
}

main "$@"
