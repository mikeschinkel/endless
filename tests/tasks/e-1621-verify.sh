#!/usr/bin/env bash
#
# E-1621 verification script — exercises the `endless agents` command
# (epic-scoped background-agent listing) end-to-end against the worktree's
# sandbox DB.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1621-verify.sh
#
# Background-agent rows are created by the dispatch-time helper
# `endless-go session-query record-bg-agent` (the same insert `task spawn --bg`
# performs), invoked via the worktree's candidate `bin/endless-go` with the
# sandbox config dir threaded explicitly (resolved from `endless db path`). That
# writes to THIS worktree's sandbox DB — the same DB `uv run endless ... --db
# sandbox` reads. No real `claude --bg` process is launched.
#
# The worktree's bin/ is prepended to PATH so the Python `endless agents` command
# resolves its `endless-go` to the candidate build (which carries the new
# list-bg-agents verb), not the global install.
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error. Each run creates fresh ids; the script does NOT wipe
# the sandbox between runs (inspect via `uv run endless task list --db sandbox`).
#
# Shape/output mirrors the E-1540 / E-1577 scripts referenced by E-1596.

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

# ─── helpers ────────────────────────────────────────────────────────────────

# Route every Python CLI invocation through the sandbox DB and worktree source.
endless() {
    uv run endless "$@" --db sandbox
}

# Set in main() once the repo root and sandbox config dir are known.
CANDIDATE_GO=""
SANDBOX_CFG=""

# Per-run-unique bg-agent short ids, set in §1, reused by §2. Derived from the
# run's fresh child-task ids so re-runs never collide on the UNIQUE short_id.
ALPHA_SHORT=""
BETA_SHORT=""

add_epic_get_id() {
    local output rc
    output=$(endless epic add "$1" 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        printf 'ERROR: epic add failed: %s\n' "${output}" >&2; return 1
    fi
    printf '%s\n' "${output}" | grep -oE 'E-[0-9]+' | head -1
}

add_task_get_id() {
    local title="$1"; shift
    local output rc
    output=$(endless task add "${title}" "$@" 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        printf 'ERROR: task add failed: %s\n' "${output}" >&2; return 1
    fi
    printf '%s\n' "${output}" | grep -oE 'E-[0-9]+' | head -1
}

# strip the E- prefix → bare integer id
bare() { printf '%s\n' "${1#E-}"; }

# record_bg_agent TASK_ID SHORT_ID — insert a working bg-agent dispatch row
# whose active_epic_id is TASK_ID's nearest epic ancestor, into the sandbox DB.
# short_id is globally UNIQUE and the sandbox is not wiped between runs, so
# callers must pass a per-run-unique value (derive it from a fresh task id).
# A failed insert (e.g. a stray collision) aborts as a setup error rather than
# silently leaving the epic empty.
record_bg_agent() {
    local task_id="$1" short_id="$2"
    local output rc
    output=$("${CANDIDATE_GO}" --config-dir "${SANDBOX_CFG}" \
        session-query record-bg-agent \
        --task-id "$(bare "${task_id}")" --short-id "${short_id}" 2>&1)
    rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        printf 'ERROR: record-bg-agent failed for %s/%s: %s\n' \
            "${task_id}" "${short_id}" "${output}" >&2
        exit 2
    fi
}

# ─── assertions ─────────────────────────────────────────────────────────────

assert_succeeds() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

assert_contains() {
    local desc="$1" pattern="$2"; shift 2
    local output; output=$("$@" 2>&1)
    if [[ "${output}" == *"${pattern}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output contains: ${pattern}" "${output}"
}

assert_not_contains() {
    local desc="$1" pattern="$2"; shift 2
    local output; output=$("$@" 2>&1)
    if [[ "${output}" != *"${pattern}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "output does NOT contain: ${pattern}" "${output}"
}

assert_refused() {
    local desc="$1" pattern="$2"; shift 2
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]] && [[ "${output}" == *"${pattern}"* ]]; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "exit != 0 AND output contains: ${pattern}" \
        "exit=${rc} | output=${output}"
}

# ─── §1: agents --epic scopes to one epic ───────────────────────────────────

test_agents_by_epic() {
    section "§1 — agents --epic (scopes to a single epic; excludes other epics)"

    local epic_a epic_b child_a child_b
    epic_a=$(add_epic_get_id "Verify epic alpha") || exit 2
    epic_b=$(add_epic_get_id "Verify epic beta") || exit 2
    child_a=$(add_task_get_id "Build the alpha child task" --parent "${epic_a}") || exit 2
    child_b=$(add_task_get_id "Build the beta child task" --parent "${epic_b}") || exit 2

    # Unique per run: child ids are freshly allocated each invocation.
    ALPHA_SHORT="alpha$(bare "${child_a}")"
    BETA_SHORT="beta$(bare "${child_b}")"
    record_bg_agent "${child_a}" "${ALPHA_SHORT}"
    record_bg_agent "${child_b}" "${BETA_SHORT}"

    assert_succeeds "agents --epic exits 0" \
        endless agents --epic "${epic_a}"
    assert_contains "agents --epic lists the alpha agent" \
        "${ALPHA_SHORT}" \
        endless agents --epic "${epic_a}"
    assert_contains "agents --epic shows the alpha child task id" \
        "${child_a}" \
        endless agents --epic "${epic_a}"
    assert_not_contains "agents --epic excludes the beta agent" \
        "${BETA_SHORT}" \
        endless agents --epic "${epic_a}"
}

# ─── §2: agents --all spans epics in the project ────────────────────────────

test_agents_all() {
    section "§2 — agents --all (drops epic filter; both agents present)"

    assert_contains "agents --all includes the alpha agent" \
        "${ALPHA_SHORT}" \
        endless agents --all
    assert_contains "agents --all includes the beta agent" \
        "${BETA_SHORT}" \
        endless agents --all
}

# ─── §3: empty epic renders the empty-state line ────────────────────────────

test_agents_empty() {
    section "§3 — agents --epic on an agent-free epic (empty-state line)"

    local quiet
    quiet=$(add_epic_get_id "Verify quiet epic") || exit 2
    assert_contains "agents --epic on a quiet epic reports none" \
        "No background agents working under ${quiet}." \
        endless agents --epic "${quiet}"
}

# ─── §4: --epic + --all is refused ──────────────────────────────────────────

test_agents_both_flags() {
    section "§4 — agents --epic with --all is refused"

    assert_refused "agents --epic --all refuses" \
        "not both" \
        endless agents --epic "E-1" --all
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    local repo_root
    repo_root=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${repo_root}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2; exit 2
    fi
    cd "${repo_root}" || exit 2

    if ! command -v uv >/dev/null 2>&1; then
        printf 'ERROR: uv not on PATH\n' >&2; exit 2
    fi

    CANDIDATE_GO="${repo_root}/bin/endless-go"
    if [[ ! -x "${CANDIDATE_GO}" ]]; then
        printf 'ERROR: %s missing — run `just build`\n' "${CANDIDATE_GO}" >&2; exit 2
    fi
    # Prepend the candidate bin/ so Python's `endless agents` resolves its
    # `endless-go` to the build under test (which carries list-bg-agents).
    export PATH="${repo_root}/bin:${PATH}"

    # Sandbox config dir = the directory holding the sandbox endless.db.
    local sandbox_db
    sandbox_db=$(uv run endless db path --db sandbox 2>/dev/null | tail -1)
    if [[ -z "${sandbox_db}" ]]; then
        printf 'ERROR: could not resolve sandbox DB path (run from inside a self-dev worktree)\n' >&2
        exit 2
    fi
    SANDBOX_CFG=$(dirname "${sandbox_db}")

    printf '%sE-1621 verification — endless agents%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox (%s)\n' "${SANDBOX_CFG}"
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_agents_by_epic
    test_agents_all
    test_agents_empty
    test_agents_both_flags

    summary
}

main "$@"
