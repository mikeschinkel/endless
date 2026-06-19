#!/usr/bin/env bash
#
# E-1568 verification script — exercises `endless task spawn --bg` end-to-end:
# the schema reshape (nullable session_id + short_id), the record-bg-agent Go
# helper (epic-ancestor resolution, kind=background, NULL session_id), the
# inline UNIQUE(short_id) constraint, the old→new migration, and the
# foreground/background handoff render split.
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1568-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on setup error.
#
# Design: unlike a sandbox-routed script, this builds its own throwaway DBs
# under a temp dir (via `endless-go --config-dir`) so runs are deterministic,
# pollution-free, and repeatable. It does NOT launch a real `claude --bg`
# agent — that live smoke test (the bg agent's SessionStart attaching its real
# UUID via CLAUDE_JOB_DIR) costs tokens and is not cleanly repeatable; it is
# documented as a manual step at the end. Everything below the line IS
# automated against the real compiled code.
#
# Shape/output follow the E-1577 prototype referenced by E-1596.

set -u

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ───────────────────────────────────────────────────────────────

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

report_skip() { printf '  %s∅%s %s %s(%s)%s\n' "${DIM}" "${RESET}" "$1" "${DIM}" "$2" "${RESET}"; }

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
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"; else report_fail "$1" "$2" "$3"; fi
}

# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output contains: $2" "$3"; fi
}

# assert_not_contains DESC NEEDLE HAYSTACK
assert_not_contains() {
    if [[ "$3" != *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output does NOT contain: $2" "$3"; fi
}

# assert_fails DESC PATTERN -- CMD...   (CMD must exit non-zero AND match PATTERN)
assert_fails() {
    local desc="$1" pattern="$2"; shift 3   # drop desc, pattern, the literal --
    local out rc; out=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -ne 0 && "${out}" == *"${pattern}"* ]]; then report_pass "${desc}"
    else report_fail "${desc}" "exit!=0 AND output ~ ${pattern}" "exit=${rc} | ${out}"; fi
}

# ─── globals wired in main ───────────────────────────────────────────────────

REPO_ROOT=""
EGO=""          # path to the worktree-built endless-go
TMP=""

# eg: run endless-go against the throwaway DB at $TMP/endless.db
eg() { "${EGO}" --config-dir "${TMP}" "$@"; }

# q: run a read query against $TMP/endless.db, print the scalar/row
q() { sqlite3 "${TMP}/endless.db" "$1"; }

# seed a fresh new-schema DB from internal/schema/schema.sql + base rows
seed_fresh_db() {
    local dir="$1"
    # schema.sql runs PRAGMA journal_mode=WAL / busy_timeout, which echo their
    # results ("wal", "5000") — discard so they don't pollute the report.
    sqlite3 "${dir}/endless.db" < "${REPO_ROOT}/internal/schema/schema.sql" >/dev/null
    sqlite3 "${dir}/endless.db" "
        INSERT INTO projects (id, name, path, status) VALUES (1, 'p', '/p', 'active');
        INSERT INTO tasks (id, project_id, title, status, type_id) VALUES (50, 1, 'epic', 'in_progress', 4);
        INSERT INTO tasks (id, project_id, parent_id, title, status, type_id) VALUES (51, 1, 50, 'child', 'in_progress', 1);
        INSERT INTO tasks (id, project_id, title, status, type_id) VALUES (60, 1, 'standalone', 'in_progress', 1);
    "
}

# ─── checks ──────────────────────────────────────────────────────────────────

test_schema_shape() {
    section "Schema — schema.sql declares the post-E-1568 sessions shape"
    assert_eq "session_id is nullable (notnull=0)" \
        "0" "$(q "SELECT \"notnull\" FROM pragma_table_info('sessions') WHERE name='session_id';")"
    assert_eq "short_id column present" \
        "short_id" "$(q "SELECT name FROM pragma_table_info('sessions') WHERE name='short_id';")"
    assert_eq "kind_id FK to session_kinds preserved" \
        "session_kinds" "$(q "SELECT \"table\" FROM pragma_foreign_key_list('sessions') WHERE \"table\"='session_kinds';")"
    assert_eq "both end-of-life process triggers present" \
        "2" "$(q "SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'sessions_null_process%';")"
}

test_unique_short_id() {
    section "Constraint — inline UNIQUE(short_id): NULLs coexist, handles unique"
    q "INSERT INTO sessions (session_id, project_id, state, kind_id, short_id, started_at) VALUES (NULL,1,'working',2,'dup',  't1');" >/dev/null 2>&1
    q "INSERT INTO sessions (session_id, project_id, state, kind_id, short_id, started_at) VALUES (NULL,1,'working',2,'other','t2');" >/dev/null 2>&1
    local nulls; nulls=$(q "INSERT INTO sessions (session_id, project_id, state, kind_id, short_id, started_at) VALUES (NULL,1,'working',1,NULL,'t3'),(NULL,1,'working',1,NULL,'t4');" 2>&1)
    assert_eq "multiple NULL short_id rows coexist" "" "${nulls}"
    local err; err=$(q "INSERT INTO sessions (session_id, project_id, state, kind_id, short_id, started_at) VALUES (NULL,1,'working',2,'dup','t5');" 2>&1)
    assert_contains "duplicate non-NULL short_id rejected" "UNIQUE constraint failed: sessions.short_id" "${err}"
}

test_record_bg_agent() {
    section "record-bg-agent — dispatch row write + epic-ancestor resolution"

    local printed row
    printed=$(eg session-query record-bg-agent --task-id 51 --short-id h-child 2>&1)
    assert_eq "child-of-epic dispatch prints the inserted sessions.id" \
        "$(q "SELECT id FROM sessions WHERE short_id='h-child';")" "${printed}"
    row=$(q "SELECT IFNULL(session_id,'NULL')||'|'||kind_id||'|'||active_task_id||'|'||IFNULL(active_epic_id,'NULL') FROM sessions WHERE short_id='h-child';")
    assert_eq "child row: session_id NULL, kind=2, task=51, epic=50" "NULL|2|51|50" "${row}"

    eg session-query record-bg-agent --task-id 60 --short-id h-standalone >/dev/null 2>&1
    assert_eq "standalone task → active_epic_id NULL" \
        "NULL" "$(q "SELECT IFNULL(active_epic_id,'NULL') FROM sessions WHERE short_id='h-standalone';")"

    eg session-query record-bg-agent --task-id 50 --short-id h-epic >/dev/null 2>&1
    assert_eq "epic dispatched directly → active_epic_id is itself (50)" \
        "50" "$(q "SELECT active_epic_id FROM sessions WHERE short_id='h-epic';")"

    assert_fails "missing --short-id refused" "short-id is required" -- \
        eg session-query record-bg-agent --task-id 51
}

test_handoff_split() {
    section "Handoff — bg variant drops tmux return lines, fg keeps them"
    local vars_fg vars_bg out_fg out_bg
    vars_fg='{"spawned_id":1568,"title":"t","spawner_task":1564,"return_anchor":"%216","worktree_path":"/wt","branch":"b","child_count":0,"bg":false}'
    vars_bg='{"spawned_id":1568,"title":"t","spawner_task":1564,"return_anchor":"%216","worktree_path":"/wt","branch":"b","child_count":0,"bg":true}'
    out_fg=$(printf '%s' "${vars_fg}" | "${EGO}" template render handoff/task 2>&1)
    out_bg=$(printf '%s' "${vars_bg}" | "${EGO}" template render handoff/task 2>&1)
    assert_contains  "fg handoff keeps 'tmux switch-client'" "tmux switch-client -t %216" "${out_fg}"
    assert_not_contains "bg handoff drops 'tmux switch-client'" "tmux switch-client" "${out_bg}"
    assert_not_contains "bg handoff drops 'tmux move-window'"   "tmux move-window"   "${out_bg}"
    assert_contains  "bg handoff offers 'claude attach'"        "claude attach"      "${out_bg}"
    assert_contains  "bg handoff still says STOP and ask"       "STOP and ask"       "${out_bg}"
}

# The pre-E-1568 sessions shape, built INLINE so the migration test is fully
# deterministic — no git archaeology (which can't reliably tell old from new:
# both schemas contain the substring "session_id TEXT NOT NULL", because
# session_messages has that column too). Only the sessions table is needed: the
# `apply-change` run opens monitor.DB() first, which applies schema.sql and
# creates every OTHER table (projects/tasks/session_kinds/...) via CREATE TABLE
# IF NOT EXISTS, leaving this old sessions table untouched for the change file
# to rebuild. The forward FK references resolve fine — SQLite permits them at
# CREATE TABLE time even before the referenced tables exist.
OLD_SESSIONS_DDL="
CREATE TABLE sessions (
    id INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL,
    project_id INTEGER,
    platform TEXT NOT NULL DEFAULT 'claude',
    state TEXT NOT NULL DEFAULT 'working',
    active_task_id INTEGER,
    active_epic_id INTEGER,
    kind_id INTEGER NOT NULL DEFAULT 1,
    plan_file_path TEXT,
    process TEXT,
    started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    last_activity TEXT,
    transcript_offset INTEGER NOT NULL DEFAULT 0,
    transcript_path TEXT,
    summary TEXT,
    hidden INTEGER NOT NULL DEFAULT 0,
    needs_recap INTEGER NOT NULL DEFAULT 0,
    summary_seq INTEGER NOT NULL DEFAULT 0,
    UNIQUE (session_id),
    FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE SET NULL,
    FOREIGN KEY (active_task_id) REFERENCES tasks(id) ON DELETE SET NULL,
    FOREIGN KEY (active_epic_id) REFERENCES tasks(id) ON DELETE SET NULL,
    FOREIGN KEY (kind_id) REFERENCES session_kinds(id)
);
INSERT INTO sessions (session_id, state, kind_id, started_at)
    VALUES ('uuid-A', 'working', 1, '2026-01-01T00:00:00');
"

test_migration() {
    section "Migration — apply e-1568 change to a pre-E-1568 DB (chicken-and-egg safe)"
    local change mdir applied
    change=$(ls "${REPO_ROOT}"/internal/schema/changes/e-1568-*.go 2>/dev/null | head -1)
    if [[ -z "${change}" ]]; then
        report_skip "old→new migration" "change file not present in this checkout"
        return
    fi

    mdir="${TMP}/migrate"; mkdir -p "${mdir}"
    printf '%s' "${OLD_SESSIONS_DDL}" | sqlite3 "${mdir}/endless.db" >/dev/null

    assert_eq "PRE: old DB has session_id NOT NULL, no short_id" \
        "1|0" \
        "$(sqlite3 "${mdir}/endless.db" "SELECT (SELECT \"notnull\" FROM pragma_table_info('sessions') WHERE name='session_id')||'|'||(SELECT count(*) FROM pragma_table_info('sessions') WHERE name='short_id');")"

    applied=$("${EGO}" --config-dir "${mdir}" event apply-change "${change}" 2>&1)
    assert_contains "apply-change succeeds on the old DB" '"status":"applied"' "${applied}"
    assert_eq "POST: session_id relaxed to nullable" \
        "0" "$(sqlite3 "${mdir}/endless.db" "SELECT \"notnull\" FROM pragma_table_info('sessions') WHERE name='session_id';")"
    assert_eq "POST: short_id column added" \
        "short_id" "$(sqlite3 "${mdir}/endless.db" "SELECT name FROM pragma_table_info('sessions') WHERE name='short_id';")"
    assert_eq "POST: existing session row preserved" \
        "uuid-A" "$(sqlite3 "${mdir}/endless.db" "SELECT session_id FROM sessions;")"
    assert_eq "POST: foreign-key integrity intact" \
        "" "$(sqlite3 "${mdir}/endless.db" "PRAGMA foreign_key_check;")"
}

test_cli_surface() {
    section "CLI — \`endless task spawn --help\` advertises --bg"
    local help; help=$(uv run endless task spawn --help 2>&1)
    assert_contains "--bg flag listed" "--bg" "${help}"
    assert_contains "help mentions claude attach" "claude attach" "${help}"
}

# ─── main ────────────────────────────────────────────────────────────────────

main() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    cd "${REPO_ROOT}" || exit 2

    EGO="${REPO_ROOT}/bin/endless-go"
    [[ -x "${EGO}" ]] || { printf 'ERROR: %s not built — run `just go` first\n' "${EGO}" >&2; exit 2; }
    command -v sqlite3 >/dev/null 2>&1 || { printf 'ERROR: sqlite3 not on PATH\n' >&2; exit 2; }
    command -v uv      >/dev/null 2>&1 || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }

    TMP=$(mktemp -d)
    trap 'rm -rf "${TMP}"' EXIT
    seed_fresh_db "${TMP}"

    printf '%sE-1568 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  repo:    %s\n  endless-go: %s\n  scratch: %s (throwaway)\n' \
        "${REPO_ROOT}" "${EGO}" "${TMP}"

    test_schema_shape
    test_unique_short_id
    test_record_bg_agent
    test_handoff_split
    test_migration
    test_cli_surface

    printf '\n%sManual live smoke (not automated — launches a real bg agent, costs tokens):%s\n' "${DIM}" "${RESET}"
    printf '%s  1. claim an epic, then: endless task spawn --bg <child-id>%s\n' "${DIM}" "${RESET}"
    printf '%s  2. endless sql "SELECT session_id,kind_id,short_id FROM sessions ORDER BY id DESC LIMIT 1" --db main%s\n' "${DIM}" "${RESET}"
    printf '%s     → session_id NULL, kind_id=2, short_id set%s\n' "${DIM}" "${RESET}"
    printf '%s  3. after the bg agent boots, re-query → session_id now the real UUID%s\n' "${DIM}" "${RESET}"
    printf '%s  4. claude attach <short_id>%s\n' "${DIM}" "${RESET}"

    summary
}

main "$@"
