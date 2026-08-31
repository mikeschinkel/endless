#!/usr/bin/env bash
#
# E-1462 verification — session_tasks.relation classification.
#
# Exercises the three layers of the change end-to-end, all in isolation (no real
# DB / ledger writes):
#   1. Go behavior — classification (claim→goal, create/import→surfaced,
#      else→revisited), set-once semantics, and the enum integrity check, via the
#      package unit tests.
#   2. schema.sql shape — a fresh DB built from schema.sql seeds the
#      session_task_relations mirror (3 rows) and carries session_tasks.relation_id.
#   3. Migration — the per-ticket change file, applied to a synthetic
#      pre-migration session_tasks, adds the mirror table + the relation_id column.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1462
#
# Exit 0 on all-passed, 1 on any failure.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCHEMA="${REPO_ROOT}/internal/schema/schema.sql"
CHANGE="${REPO_ROOT}/internal/schema/changes/e-1462-add-session-tasks-relation.sql"
TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

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

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"; else report_fail "$1" "$2" "$3"; fi
}

# ─── 1. Go behavior ─────────────────────────────────────────────────────────

section "1 — Go behavior (classification, set-once, enum integrity)"

GO_OUT="$(cd "${REPO_ROOT}" && go test \
    ./internal/sessiontaskrelation/ ./internal/events/ \
    -run 'TestSessionTasks_Relation|TestVerifyIntegrity|TestParse|TestRelation_|TestAll_' \
    2>&1)"
if [[ $? -eq 0 ]]; then
    report_pass "relation classification + set-once + integrity unit tests pass"
else
    report_fail "relation unit tests" "go test exit 0" "${GO_OUT}"
fi

# ─── 2. schema.sql shape ────────────────────────────────────────────────────

section "2 — schema.sql shape (fresh DB)"

DB_A="${TMP}/schema.db"
sqlite3 "${DB_A}" < "${SCHEMA}" >/dev/null 2>&1

assert_eq "session_task_relations seeds 3 rows" \
    "3" "$(sqlite3 "${DB_A}" 'SELECT count(*) FROM session_task_relations;')"
assert_eq "seeded slugs are goal/surfaced/revisited" \
    "goal,surfaced,revisited" \
    "$(sqlite3 "${DB_A}" "SELECT group_concat(slug) FROM (SELECT slug FROM session_task_relations ORDER BY id);")"
assert_eq "session_tasks has relation_id column" \
    "relation_id" \
    "$(sqlite3 "${DB_A}" "SELECT name FROM pragma_table_info('session_tasks') WHERE name='relation_id';")"
assert_eq "relation_id is a FK to session_task_relations" \
    "session_task_relations" \
    "$(sqlite3 "${DB_A}" "SELECT \"table\" FROM pragma_foreign_key_list('session_tasks') WHERE \"from\"='relation_id';")"

# ─── 3. Migration change file ───────────────────────────────────────────────

section "3 — migration change file (synthetic pre-migration DB)"

DB_B="${TMP}/premig.db"
# Pre-E-1462 session_tasks: no relation_id, no mirror table.
sqlite3 "${DB_B}" "CREATE TABLE session_tasks (
    id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL, task_id INTEGER NOT NULL,
    created_at TEXT NOT NULL, updated_at TEXT NOT NULL, do_order INTEGER,
    UNIQUE(session_id, task_id));"
assert_eq "pre-migration DB lacks relation_id" \
    "" "$(sqlite3 "${DB_B}" "SELECT name FROM pragma_table_info('session_tasks') WHERE name='relation_id';")"

sqlite3 "${DB_B}" < "${CHANGE}" >/dev/null 2>&1

assert_eq "migration adds session_task_relations (3 rows)" \
    "3" "$(sqlite3 "${DB_B}" 'SELECT count(*) FROM session_task_relations;')"
assert_eq "migration adds session_tasks.relation_id" \
    "relation_id" \
    "$(sqlite3 "${DB_B}" "SELECT name FROM pragma_table_info('session_tasks') WHERE name='relation_id';")"

# ─── summary ────────────────────────────────────────────────────────────────

printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
if [[ "${FAIL_COUNT}" -eq 0 ]]; then
    printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
    exit 0
fi
printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
    "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
printf '\n'
exit 1
