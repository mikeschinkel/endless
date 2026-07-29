#!/usr/bin/env bash
#
# E-1807 verification script — exercises the spawn/claim ownership guard's
# dead-pane self-heal: a ghost owner (a non-ended `sessions` row pointing at a
# tmux pane that no longer exists, left behind when a session died without
# firing SessionEnd) is reaped to `ended` before the guard reads ownership, so
# the task reads as free and the spawn/claim proceeds with no user step.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1807-verify.sh
#
# Structure (mirrors tests/tasks/e-1802-verify.sh):
#   1. Fail-fast unit gate: the Go `reap-dead-panes` binary test plus the Python
#      `_check_task_ownership` test. Abort before the E2E if either fails.
#   2. E2E against this worktree's sandbox DB:
#      A. the Go reaper (`endless-go session-query reap-dead-panes`) flips a
#         seeded dead-pane session to `ended` with `process` NULLed;
#      B. the Python ownership guard (`_check_task_ownership`) treats a
#         dead-pane-owned task as FREE (no "already active" collision) and
#         reaps the ghost as a side effect.
#
# The reaper inspects the live tmux server to decide what's dead, so the E2E
# requires a reachable tmux server (present under `esu`, which runs in tmux).
# Each run creates fresh task/session ids; the sandbox is not wiped between runs
# (pollution is bounded and inspectable via `uv run endless task list --db sandbox`).

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
    local desc="$1" expected="$2" actual="$3"
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

# Wrap the Python CLI so every seed/mutation routes through the sandbox DB.
endless() { uv run endless "$@" --db sandbox; }

# Create a task and emit just its E-NNN id on stdout (other output → stderr).
add_task_get_id() {
    local title="$1"; shift
    local output rc
    output=$(endless task add "${title}" "$@" 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]]; then
        printf 'ERROR: add failed for %q: %s\n' "${title}" "${output}" >&2
        return 1
    fi
    printf '%s\n' "${output}" | grep -oE 'E-[0-9]+' | head -1
}

# numeric id from an "E-NNN" token.
num_id() { printf '%s' "${1#E-}"; }

# Seed a non-ended session owning task $2 via dead tmux pane $3, in the task's
# project. session_id is suffixed with the task id so runs never collide.
seed_ghost_owner() {
    local tag="$1" task="$2" pane="$3" pid
    pid=$(sqlite3 "${SANDBOX_DB}" "SELECT project_id FROM tasks WHERE id=${task};")
    sqlite3 "${SANDBOX_DB}" \
        "INSERT INTO sessions (session_id, project_id, platform, state, process, active_task_id, last_activity)
         VALUES ('e1807-${tag}-${task}', ${pid}, 'claude', 'working', '${pane}', ${task}, '2026-07-29T00:00:00');"
}

# state|process for the ghost owner of task $1 (the '%'-pane row we seeded).
ghost_row() {
    local task="$1"
    sqlite3 "${SANDBOX_DB}" \
        "SELECT state || '|' || COALESCE(process,'<null>')
         FROM sessions WHERE active_task_id=${task} AND session_id LIKE 'e1807-%';"
}

# Run the real Python ownership guard against the sandbox for task $1, current
# session None. Prints FREE / OWNED / 'RAISED: <msg>'. Routes via --db sandbox
# semantics (apply_db_choice), so the guard's own reap + live-set reads target
# the sandbox and its candidate Go binary.
run_ownership_guard() {
    local task="$1"
    uv run python -c "
import sys
from endless import config, task_cmd
config.apply_db_choice('sandbox')
try:
    r = task_cmd._check_task_ownership(${task}, None)
except Exception as e:
    print('RAISED: ' + str(e).replace(chr(10), ' '))
    sys.exit(0)
print('FREE' if r is False else 'OWNED')
"
}

# ─── assertions ──────────────────────────────────────────────────────────────

assert_eq() {
    local desc="$1" want="$2" got="$3"
    if [[ "${got}" == "${want}" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "${want}" "${got}"
}

# ─── scenario A: the Go reaper ends a dead-pane ghost ─────────────────────────

test_go_reaper_ends_ghost() {
    section "A. reap-dead-panes flips a dead-pane session to ended + NULL process"

    local task before after
    task=$(num_id "$(add_task_get_id 'Reap e1807 go-reaper ghost')")
    seed_ghost_owner "goreap" "${task}" "%999996"

    before=$(ghost_row "${task}")
    assert_eq "seeded ghost starts non-ended on its dead pane" \
        "working|%999996" "${before}"

    ./bin/endless-go session-query reap-dead-panes --project-root "${REPO_ROOT}" \
        >/tmp/e1807-reap.out 2>&1
    local rc=$?
    assert_eq "reap-dead-panes exits 0" "0" "${rc}"
    assert_eq "reap-dead-panes emits nothing on success" "" "$(cat /tmp/e1807-reap.out)"

    after=$(ghost_row "${task}")
    assert_eq "ghost row is now ended with process NULLed" \
        "ended|<null>" "${after}"
}

# ─── scenario B: the ownership guard reads the task as free ───────────────────

test_guard_reads_free() {
    section "B. _check_task_ownership self-heals a dead-pane owner to FREE"

    local task verdict after
    task=$(num_id "$(add_task_get_id 'Reap e1807 guard ghost')")
    seed_ghost_owner "guard" "${task}" "%999995"

    # The guard reaps the ghost internally, then its non-ended-owner query
    # excludes it → free. No pre-reap here: this exercises the guard's own path.
    verdict=$(run_ownership_guard "${task}")
    assert_eq "guard treats the dead-pane-owned task as free (no collision)" \
        "FREE" "${verdict}"

    after=$(ghost_row "${task}")
    assert_eq "guard reaped the ghost as a side effect (ended + NULL)" \
        "ended|<null>" "${after}"
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${REPO_ROOT}" || exit 2

    # Put the worktree's freshly-built binary first on PATH so the ownership
    # guard's own `endless-go` subprocess (the reap + live-set shellouts) runs
    # the candidate, not the stale globally-installed symlink that predates the
    # `reap-dead-panes` subcommand (mirrors tests/conftest.py's PATH prepend).
    export PATH="${REPO_ROOT}/bin:${PATH}"

    for tool in uv sqlite3 tmux; do
        if ! command -v "${tool}" >/dev/null 2>&1; then
            printf 'ERROR: %s not on PATH\n' "${tool}" >&2
            exit 2
        fi
    done
    if [[ ! -x ./bin/endless-go ]]; then
        printf 'ERROR: ./bin/endless-go not built — run `just build` first\n' >&2
        exit 2
    fi
    # The reaper reads the live tmux server; without a reachable server every
    # tmux-pane row would look dead, so refuse rather than assert on a no-op.
    if ! tmux list-panes -a >/dev/null 2>&1; then
        printf 'ERROR: no reachable tmux server — run this from inside tmux (esu)\n' >&2
        exit 2
    fi

    SANDBOX_DB="${HOME}/.cache/endless/sandboxes/$(basename "${REPO_ROOT}")/endless/endless.db"

    printf '%sE-1807 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${REPO_ROOT}"
    printf '  db:      sandbox\n'
    printf '  go bin:  ./bin/endless-go\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    # Fail-fast unit gate: the Go reaper verb and the Python ownership guard must
    # pass before the E2E is worth running.
    section "Unit gate (Go reap-dead-panes + Python ownership guard)"
    if go test ./internal/sessionquerycmd/ >/tmp/e1807-gotest.log 2>&1; then
        report_pass "go test ./internal/sessionquerycmd/"
    else
        report_fail "go test ./internal/sessionquerycmd/" "package passes" \
            "$(cat /tmp/e1807-gotest.log)"
        summary
        exit 1
    fi
    if uv run pytest tests/test_check_task_ownership.py -q >/tmp/e1807-pytest.log 2>&1; then
        report_pass "pytest tests/test_check_task_ownership.py"
    else
        report_fail "pytest tests/test_check_task_ownership.py" "all pass" \
            "$(cat /tmp/e1807-pytest.log)"
        summary
        exit 1
    fi

    # Materialize the sandbox DB before any sqlite3 seed.
    endless task list >/dev/null 2>&1 || true
    if [[ ! -f "${SANDBOX_DB}" ]]; then
        printf 'ERROR: sandbox DB not found at %s\n' "${SANDBOX_DB}" >&2
        printf '       run `just dev-sandbox-init` from this worktree first\n' >&2
        exit 2
    fi

    test_go_reaper_ends_ghost
    test_guard_reads_free

    summary
}

main "$@"
