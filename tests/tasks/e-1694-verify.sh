#!/usr/bin/env bash
#
# E-1694 verification script — exercises the spawner/parent split in
# `session status`: the ↑ parent row is the focal's REAL task-tree parent
# (tasks.parent_id), and the ↩ from row is the spawning session's active task
# (session lineage). They are distinct concepts; before E-1694 the spawner was
# mislabeled ↑ parent.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1694-verify.sh
#
# Seeds tasks via the Python CLI (`endless ... --db sandbox`) and reads them back
# through the worktree's candidate Go binary headless:
#   ./bin/endless-go session-status --task <focal> [--from-session <sid>]
# --task names the focal (no tmux), --from-session supplies the spawning session
# id the live path would read from @endless_spawned_by. The spawning session row
# is inserted directly into the sandbox sessions table (the hook normally writes
# it). Output: pass/fail per check, then a summary. Exit 0 on all-passed.

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

PARENT_GLYPH="↑"
FROM_GLYPH="↩"

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

endless() { uv run endless "$@" --db sandbox; }

# NO_COLOR + wide --cols keep output ANSI-free and untruncated for greps.
go_session_status() {
    NO_COLOR=1 ./bin/endless-go session-status --cols 200 "$@"
}

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

num_id() { printf '%s' "${1#E-}"; }

# The row line for E-ID in captured output, or "". Trailing space disambiguates
# E-2 from E-20 (the id is left-justified to 6 cols then a space).
row_for() {
    local id="$1" output="$2"
    printf '%s\n' "${output}" | grep -E "E-${id} " | head -1
}

# Insert a spawning session row directly (the hook normally creates it). Emits
# the new sessions.id. project_id 1 is seeded lazily below.
seed_spawner_session() {
    local active_task="$1"
    sqlite3 "${SANDBOX_DB}" \
        "INSERT INTO sessions (session_id, project_id, platform, state, active_task_id, kind_id, started_at, last_activity)
         VALUES (NULL, 1, 'claude', 'working', ${active_task}, 1, '2026-06-30T00:00:00', '2026-06-30T00:00:00');
         SELECT last_insert_rowid();" 2>&1
}

# ─── assertions ──────────────────────────────────────────────────────────────

assert_row_has_glyph() {
    local desc="$1" id="$2" glyph="$3" output="$4" row
    row=$(row_for "${id}" "${output}")
    if [[ -n "${row}" ]] && [[ "${row}" == *"${glyph}"* ]]; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "E-${id} row contains '${glyph}'" "${row:-<row absent>}"
}

assert_row_lacks_glyph() {
    local desc="$1" id="$2" glyph="$3" output="$4" row
    row=$(row_for "${id}" "${output}")
    if [[ -n "${row}" ]] && [[ "${row}" != *"${glyph}"* ]]; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "E-${id} row present WITHOUT '${glyph}'" "${row:-<row absent>}"
}

assert_contains() {
    local desc="$1" needle="$2" output="$3"
    if [[ "${output}" == *"${needle}"* ]]; then
        report_pass "${desc}"; return
    fi
    report_fail "${desc}" "output contains '${needle}'" "${output}"
}

# ─── scenario 1: ↑ parent is the real task-tree parent ───────────────────────

test_parent_is_real_parent() {
    section "↑ parent row is the focal's real task-tree parent (tasks.parent_id)"

    local p f out
    p=$(add_task_get_id "Build E1694 real parent")
    f=$(add_task_get_id "Build E1694 focal under parent" --parent "${p}")

    out=$(go_session_status --task "$(num_id "${f}")")

    assert_row_has_glyph "real parent ${p} carries ↑ in focal ${f}'s view" \
        "$(num_id "${p}")" "${PARENT_GLYPH}" "${out}"
    # The focal row itself must not be tagged parent.
    assert_row_lacks_glyph "focal ${f} is not tagged ↑ parent" \
        "$(num_id "${f}")" "${PARENT_GLYPH}" "${out}"
}

# ─── scenario 2: ↩ from is the spawner; parent ≠ from when distinct ──────────

test_from_is_spawner_distinct_from_parent() {
    section "↩ from row is the spawner; distinct from ↑ parent"

    local p f s sid out
    p=$(add_task_get_id "Build E1694 parent for split")
    f=$(add_task_get_id "Build E1694 focal for split" --parent "${p}")
    s=$(add_task_get_id "Build E1694 spawner task (sibling-ish)")
    sid=$(seed_spawner_session "$(num_id "${s}")")
    if ! [[ "${sid}" =~ ^[0-9]+$ ]]; then
        report_fail "seed spawning session" "a numeric sessions.id" "${sid}"
        return
    fi

    out=$(go_session_status --task "$(num_id "${f}")" --from-session "${sid}")

    assert_row_has_glyph "real parent ${p} carries ↑ parent" \
        "$(num_id "${p}")" "${PARENT_GLYPH}" "${out}"
    assert_row_has_glyph "spawner ${s} carries ↩ from" \
        "$(num_id "${s}")" "${FROM_GLYPH}" "${out}"
    assert_row_lacks_glyph "spawner ${s} is NOT tagged ↑ parent" \
        "$(num_id "${s}")" "${PARENT_GLYPH}" "${out}"
}

# ─── scenario 3: --tree roots on real parent, annotates focal with spawner ───

test_tree_roots_parent_annotates_from() {
    section "--tree roots on the real parent and annotates the focal ← spawner"

    local p f s sid out
    p=$(add_task_get_id "Build E1694 tree parent")
    f=$(add_task_get_id "Build E1694 tree focal" --parent "${p}")
    s=$(add_task_get_id "Build E1694 tree spawner")
    sid=$(seed_spawner_session "$(num_id "${s}")")
    if ! [[ "${sid}" =~ ^[0-9]+$ ]]; then
        report_fail "seed spawning session (tree)" "a numeric sessions.id" "${sid}"
        return
    fi

    out=$(go_session_status --task "$(num_id "${f}")" --from-session "${sid}" --tree)
    local root_line
    root_line=$(printf '%s\n' "${out}" | head -1)

    # Spine: parent is the flush-left root line; focal nests under it marked * and
    # annotated ← spawner; the spawner is NOT the root (pre-E-1694 behavior).
    if [[ "${root_line}" == "${p}" ]]; then
        report_pass "tree root line is the real parent ${p}"
    else
        report_fail "tree root line is the real parent ${p}" "${p}" "${root_line}"
    fi
    assert_contains "focal annotated '*${f} ← ${s}'" \
        "*${f} ← ${s}" "${out}"
    if [[ "${root_line}" != "${s}" ]]; then
        report_pass "spawner ${s} is NOT the tree root line"
    else
        report_fail "spawner ${s} is NOT the tree root line" "root != ${s}" "${root_line}"
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

    SANDBOX_DB="${HOME}/.cache/endless/sandboxes/$(basename "${repo_root}")/endless/endless.db"

    # Seed one task first so the sandbox DB + project row exist before the direct
    # sqlite3 session inserts reference project_id 1.
    endless task add "E1694 sandbox bootstrap" >/dev/null 2>&1
    if [[ ! -f "${SANDBOX_DB}" ]]; then
        printf 'ERROR: sandbox DB not found after bootstrap: %s\n' "${SANDBOX_DB}" >&2
        exit 2
    fi

    printf '%sE-1694 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${repo_root}"
    printf '  db:      sandbox\n'
    printf '  go bin:  ./bin/endless-go\n'
    printf '  python:  %s\n' "$(uv run python --version 2>&1 | tail -1)"

    test_parent_is_real_parent
    test_from_is_spawner_distinct_from_parent
    test_tree_roots_parent_annotates_from

    summary
}

main "$@"
