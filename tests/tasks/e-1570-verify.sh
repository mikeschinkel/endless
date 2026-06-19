#!/usr/bin/env bash
#
# E-1570 verification script — exercises the two bg-agent attach verbs
# end-to-end against the worktree's sandbox DB.
#
#   `endless task spawn --attach <id>`  — opens a NEW tmux window on a live
#                                          bg agent (view modifier, not a
#                                          dispatcher; mutually exclusive --bg).
#   `endless task attach <id>`          — execs the current process into
#                                          `claude attach`; refuses inside a
#                                          Claude session (CLAUDECODE=1) sans
#                                          --force.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1570-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on environment error. Each run seeds fresh tasks in the sandbox;
# the script does NOT wipe the sandbox between runs (pollution is bounded and
# inspectable via `uv run endless task list --db sandbox`).
#
# SCOPE NOTE — what this script can and cannot automate:
#   This script ONLY exercises the GUARD/refusal paths, because the happy paths
#   replace or spawn real processes:
#     - `task attach` happy path execs `claude attach <short>` IN PLACE (replaces
#       this shell). Untestable in a script without losing the runner.
#     - `task spawn --attach` happy path opens a real tmux window running
#       `claude attach`. Testable only by visual inspection.
#   Those two are listed under "MANUAL CHECKS" at the end of a passing run.
#
# Follows the shape/output convention prototyped in tests/tasks/e-1577-verify.sh
# (formalization tracked under E-1596).

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

GO_BIN=""   # set in main() once repo root is known
CFG=""      # sandbox config dir (holds endless.db), set in main()

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
        printf '\n  %sALL PASSED%s\n' "${GREEN}${BOLD}" "${RESET}"
        manual_checks
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

manual_checks() {
    printf '\n%sMANUAL CHECKS (happy paths — replace/spawn real processes)%s\n' \
        "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  From a tmux session, with a real bg agent dispatched:\n'
    printf '    1. %sendless task spawn --bg E-<child>%s\n' "${DIM}" "${RESET}"
    printf '    2. %sendless task spawn --attach E-<child>%s'   "${DIM}" "${RESET}"
    printf ' → a NEW tmux window opens running `claude attach`.\n'
    printf '    3. Detach (← / Ctrl+Z); `claude agents --json` still lists it.\n'
    printf '    4. From a FRESH terminal: %sendless task attach E-<child>%s' \
        "${DIM}" "${RESET}"
    printf ' drops in-place.\n\n'
}

# ─── helpers ────────────────────────────────────────────────────────────────

# Wrap the CLI so every invocation routes through the sandbox DB.
endless() {
    uv run endless "$@" --db sandbox
}

# Same, but simulating a Claude-spawned subprocess (the documented marker the
# attach verb keys off to protect the caller's own session).
endless_in_claude() {
    CLAUDECODE=1 uv run endless "$@" --db sandbox
}

# Help text is global-flag-independent; call without --db to keep it isolated.
endless_nodb() {
    uv run endless "$@"
}

# Create a task and emit just its E-NNN id on stdout. All other output goes to
# stderr so callers can capture only the id.
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

# Seed a live background-agent sessions row (kind=background, state=working) for
# a task, mirroring what `task spawn --bg` records — without launching a real
# `claude --bg`. Uses the same Go helper Python's dispatch calls. ID is numeric.
seed_bg_agent() {
    local num="$1"
    local short="$2"
    "${GO_BIN}" --config-dir "${CFG}" \
        session-query record-bg-agent \
        --task-id "${num}" --short-id "${short}"
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_refused DESC PATTERN CMD [ARGS...]
#   Pass if CMD exits non-zero AND its combined output contains PATTERN.
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

# ─── checks: CLI wiring ──────────────────────────────────────────────────────

test_wiring() {
    section "Wiring — flag + subcommand registered on the CLI"

    assert_contains "'task spawn --help' documents --attach" \
        "--attach" endless_nodb task spawn --help

    assert_contains "'task spawn --help' notes --attach is exclusive with --bg" \
        "Mutually exclusive with" endless_nodb task spawn --help

    assert_contains "'task attach' subcommand exists with help" \
        "background agent" endless_nodb task attach --help

    assert_contains "'task attach --help' documents --force escape hatch" \
        "--force" endless_nodb task attach --help
}

# ─── checks: mutual exclusion ────────────────────────────────────────────────

test_mutual_exclusion() {
    section "Mutual exclusion — --bg and --attach cannot combine"

    local tid
    tid=$(add_task_get_id "Add mutual-exclusion target")
    assert_refused "'spawn --bg --attach' is refused" \
        "mutually exclusive" \
        endless task spawn "${tid}" --bg --attach
}

# ─── checks: no live bg agent ────────────────────────────────────────────────

test_no_bg_agent() {
    section "Guard — both verbs refuse when no live bg agent exists"

    local tid
    tid=$(add_task_get_id "Add no-bg-agent target")

    assert_refused "'task attach' refuses (no exec) without a bg agent" \
        "has no live bg agent" \
        endless task attach "${tid}"

    # spawn --attach hits the tmux gate first; inside tmux it reaches the
    # bg-agent check, outside tmux it stops at the gate. Both are correct
    # refusals — assert the message appropriate to the environment.
    if [[ -n "${TMUX:-}" ]]; then
        assert_refused "'spawn --attach' refuses without a bg agent (in tmux)" \
            "has no live bg agent" \
            endless task spawn "${tid}" --attach
    else
        assert_refused "'spawn --attach' refuses at tmux gate (no tmux)" \
            "tmux" \
            endless task spawn "${tid}" --attach
    fi
}

# ─── checks: CLAUDECODE self-destruct guard ──────────────────────────────────

test_claudecode_guard() {
    section "Guard — 'task attach' protects the caller's Claude session"

    local tid num
    tid=$(add_task_get_id "Add claudecode-guard target")
    num="${tid#E-}"

    # short_id is UNIQUE in sessions; derive from the (always-fresh) task num so
    # repeat runs never collide.
    local seed_err
    seed_err=$(seed_bg_agent "${num}" "bg${num}seed" 2>&1 >/dev/null)
    if [[ -n "${seed_err}" ]]; then
        report_fail "seed a live bg agent for ${tid}" \
            "record-bg-agent exit 0" "${seed_err}"
        return
    fi
    report_pass "seeded a live bg agent for ${tid}"

    # CLAUDECODE=1 + no --force must refuse and NOT exec. Running the non-force,
    # non-CLAUDECODE happy path here would exec `claude attach` and replace this
    # runner, so it is deliberately omitted (see SCOPE NOTE / MANUAL CHECKS).
    assert_refused "refuses inside a Claude session without --force" \
        "inside a Claude session" \
        endless_in_claude task attach "${tid}"
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

    GO_BIN="${repo_root}/bin/endless-go"
    if [[ ! -x "${GO_BIN}" ]]; then
        printf 'ERROR: %s missing — run `just build` first\n' "${GO_BIN}" >&2
        exit 2
    fi

    local db
    db=$(uv run endless db path --db sandbox 2>/dev/null)
    if [[ -z "${db}" ]]; then
        printf 'ERROR: could not resolve sandbox DB path\n' >&2
        exit 2
    fi
    CFG=$(dirname "${db}")

    printf '%sE-1570 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox (%s)\n' "${db}"
    local tmux_state="no (spawn --attach exercises the tmux-gate path)"
    [[ -n "${TMUX:-}" ]] && tmux_state="yes"
    printf '  tmux:    %s\n' "${tmux_state}"
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_wiring
    test_mutual_exclusion
    test_no_bg_agent
    test_claudecode_guard

    summary
}

main "$@"
