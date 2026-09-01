#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1802 and records what was true when E-1802
# landed. Edit it only if you ARE E-1802. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1802 verification script — exercises the NO-GOAL `session status` view:
# when a session has no claimed task (active_task_id NULL), it still lists the
# tasks that session filed (surfaced) or touched (revisited), instead of hiding
# them behind the "claim or bind" hint.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1802
#
# It seeds tasks via the Python CLI (`endless ... --db sandbox`), seeds a
# goal-less session plus its surfaced/revisited/goal session_tasks rows directly
# via sqlite3 (a bare shell has no live Claude session to record them), then
# reads them back through the worktree's candidate Go binary in its headless
# no-goal mode (`./bin/endless-go session-status --session <id>`), which bypasses
# tmux/session resolution and reads the same self-detected sandbox DB. Output:
# pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any failure.
# Each run creates fresh task/session IDs; the sandbox is not wiped between runs
# (pollution is bounded and inspectable via `uv run endless task list --db sandbox`).

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
    GREEN=$'\033[32m'
    RED=$'\033[31m'
    DIM=$'\033[2m'
    BOLD=$'\033[1m'
    RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

UNDERLINE="──────────────────────────────────────────────────────────────"

# Glyph the renderer uses for the block column (internal/sessionstatuscmd).
BLOCKED_GLYPH="⊗"

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

# Wrap the Python CLI so every seed/mutation routes through the sandbox DB.
endless() {
    uv run endless "$@" --db sandbox
}

# Render the no-goal session-status view for an explicit session through the
# worktree's candidate Go binary, headless. NO_COLOR + a wide --cols keep the
# output ANSI-free and untruncated so the row/glyph greps are reliable.
go_session_status() {
    NO_COLOR=1 ./bin/endless-go session-status --cols 200 "$@"
}

# Create a task and emit just its E-NNN id on stdout (other output → stderr).
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

# numeric id from an "E-NNN" token.
num_id() { printf '%s' "${1#E-}"; }

# Insert a session_tasks row (session → task) with an explicit relation_id
# (1=goal, 2=surfaced, 3=revisited) directly into the sandbox DB.
seed_session_task() {
    local session="$1" task="$2" relation="$3"
    sqlite3 "${SANDBOX_DB}" \
        "INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at)
         VALUES (${session}, ${task}, ${relation}, '2026-07-28T00:00:00', '2026-07-28T00:00:00');"
}

# Create a goal-less session (active_task_id NULL) and echo its integer id.
seed_goalless_session() {
    sqlite3 "${SANDBOX_DB}" \
        "INSERT INTO sessions (state, process, last_activity)
         VALUES ('working', 'e1802-verify', '2026-07-28T00:00:00');
         SELECT last_insert_rowid();"
}

# The row line for task E-ID within captured session-status OUTPUT, or "".
row_for() {
    local id="$1" output="$2"
    printf '%s\n' "${output}" | grep -E "E-${id} " | head -1
}

# ─── assertions ──────────────────────────────────────────────────────────────

assert_row_present() {
    local desc="$1" id="$2" output="$3"
    if [[ -n "$(row_for "${id}" "${output}")" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "a row for E-${id}" "no E-${id} row in:\n${output}"
}

assert_row_absent() {
    local desc="$1" id="$2" output="$3"
    if [[ -z "$(row_for "${id}" "${output}")" ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "no row for E-${id}" "$(row_for "${id}" "${output}")"
}

assert_row_has_glyph() {
    local desc="$1" id="$2" glyph="$3" output="$4"
    local row
    row=$(row_for "${id}" "${output}")
    if [[ -n "${row}" ]] && [[ "${row}" == *"${glyph}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "E-${id} row contains '${glyph}'" "${row:-<row absent>}"
}

assert_contains() {
    local desc="$1" needle="$2" output="$3"
    if [[ "${output}" == *"${needle}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output contains '${needle}'" "${output}"
}

assert_absent() {
    local desc="$1" needle="$2" output="$3"
    if [[ "${output}" != *"${needle}"* ]]; then
        report_pass "${desc}"
        return
    fi
    report_fail "${desc}" "output does NOT contain '${needle}'" "${output}"
}

# ─── scenario 1: no-goal session surfaces its filed/touched work ─────────────

test_no_goal_surfaces_work() {
    section "No-goal session lists surfaced (filed) + revisited (touched) tasks"

    local surfaced revisited goal done_touched blocker sess out out_all
    surfaced=$(add_task_get_id "Build e1802 filed this session")
    revisited=$(add_task_get_id "Build e1802 touched this session")
    goal=$(add_task_get_id "Build e1802 goal task")
    done_touched=$(add_task_get_id "Build e1802 touched but terminal")
    blocker=$(add_task_get_id "Build e1802 open blocker")

    # Terminal state on the touched-but-done task; open blocker on the revisited.
    endless task confirm "${done_touched}" >/dev/null 2>&1
    endless task block "${revisited}" --by "${blocker}" >/dev/null 2>&1

    sess=$(seed_goalless_session)
    seed_session_task "${sess}" "$(num_id "${surfaced}")" 2      # surfaced
    seed_session_task "${sess}" "$(num_id "${revisited}")" 3     # revisited
    seed_session_task "${sess}" "$(num_id "${goal}")" 1          # goal (focal view only)
    seed_session_task "${sess}" "$(num_id "${done_touched}")" 3  # revisited, terminal

    out=$(go_session_status --session "${sess}")
    out_all=$(go_session_status --session "${sess}" --all)

    assert_row_present "surfaced task ${surfaced} appears in the no-goal view" \
        "$(num_id "${surfaced}")" "${out}"
    assert_row_present "revisited task ${revisited} appears in the no-goal view" \
        "$(num_id "${revisited}")" "${out}"
    assert_row_has_glyph "revisited ${revisited} carries ⊗ (open blocker)" \
        "$(num_id "${revisited}")" "${BLOCKED_GLYPH}" "${out}"
    assert_row_absent "goal-relation task ${goal} is NOT in the surfaced/revisited view" \
        "$(num_id "${goal}")" "${out}"
    assert_row_absent "terminal touched task ${done_touched} omitted by default" \
        "$(num_id "${done_touched}")" "${out}"
    assert_row_present "terminal touched task ${done_touched} surfaced under --all" \
        "$(num_id "${done_touched}")" "${out_all}"
    assert_absent "no-goal view with work does NOT print the claim/bind hint" \
        "claim or bind" "${out}"
}

# ─── scenario 2: an empty session still shows the claim/bind hint ────────────

test_empty_session_shows_hint() {
    section "Session with no surfaced/revisited rows still shows the claim/bind hint"

    local sess out
    sess=$(seed_goalless_session)  # no session_tasks rows seeded

    out=$(go_session_status --session "${sess}")

    assert_contains "empty no-goal session falls back to the claim/bind hint" \
        "claim or bind" "${out}"
}

# ─── scenario 3: the no-goal read does NOT write session_tasks ──────────────

test_read_is_side_effect_free() {
    section "No-goal session-status read does NOT write session_tasks"

    local surfaced sess before after
    surfaced=$(add_task_get_id "Build e1802 read-only check")
    sess=$(seed_goalless_session)
    seed_session_task "${sess}" "$(num_id "${surfaced}")" 2

    before=$(sqlite3 "${SANDBOX_DB}" "SELECT count(*) FROM session_tasks;" 2>&1)
    go_session_status --session "${sess}" >/dev/null 2>&1
    after=$(sqlite3 "${SANDBOX_DB}" "SELECT count(*) FROM session_tasks;" 2>&1)

    if [[ "${before}" =~ ^[0-9]+$ ]] && [[ "${after}" == "${before}" ]]; then
        report_pass "session_tasks row count unchanged (${before} → ${after})"
    else
        report_fail "session_tasks row count unchanged after no-goal read" \
            "after == before (numeric)" "before=${before} after=${after}"
    fi
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
    if ! command -v sqlite3 >/dev/null 2>&1; then
        printf 'ERROR: sqlite3 not on PATH\n' >&2
        exit 2
    fi
    if [[ ! -x ./bin/endless-go ]]; then
        printf 'ERROR: ./bin/endless-go not built — run `just build` first\n' >&2
        exit 2
    fi

    # Deterministic sandbox DB path for this worktree (basename matches the
    # sandbox dir basename, E-1281). The Python seed and the Go read both resolve
    # this same DB (self-detect from cwd, E-1368); we also read it via sqlite3.
    SANDBOX_DB="${HOME}/.cache/endless/sandboxes/$(basename "${repo_root}")/endless/endless.db"

    # A `task add` first materializes the sandbox DB before any sqlite3 seed.
    endless task list >/dev/null 2>&1 || true
    if [[ ! -f "${SANDBOX_DB}" ]]; then
        printf 'ERROR: sandbox DB not found at %s\n' "${SANDBOX_DB}" >&2
        printf '       run `just dev-sandbox-init` from this worktree first\n' >&2
        exit 2
    fi

    printf '%sE-1802 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  go bin:  ./bin/endless-go\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    # Fail fast on the unit layer: the no-goal query (monitor) and its render gate
    # (sessionstatuscmd) must pass before the E2E checks are worth running.
    section "Unit tests (monitor + sessionstatuscmd)"
    if go test ./internal/monitor/ ./internal/sessionstatuscmd/ >/tmp/e1802-gotest.log 2>&1; then
        report_pass "go test ./internal/monitor/ ./internal/sessionstatuscmd/"
    else
        report_fail "go test ./internal/monitor/ ./internal/sessionstatuscmd/" \
            "both packages pass" "$(cat /tmp/e1802-gotest.log)"
        summary
        exit 1
    fi

    test_no_goal_surfaces_work
    test_empty_session_shows_hint
    test_read_is_side_effect_free

    summary
}

main "$@"
