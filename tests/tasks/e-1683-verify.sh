#!/usr/bin/env bash
#
# E-1683 verification script — exercises `session_tasks.do_order` end-to-end
# through the WORKTREE-BUILT endless-go binary against a self-contained temp DB
# built from the shipped internal/schema/schema.sql. This proves three things
# unit tests can't on their own:
#   1. The shipped schema.sql actually declares session_tasks.do_order.
#   2. The migration change file applies cleanly to a pre-migration DB.
#   3. The real binary dispatches the new session_tasks.ordered event kind and
#      its executor applies sequence/parallel/replace-all/foreign-rejection
#      through the full emit path (ledger commit included).
#
# Run from anywhere inside the worktree:
#   ./tests/tasks/e-1683-verify.sh
#
# Self-contained: builds its own temp DB + git repo, touches nothing in the
# sandbox or real ledger. Exit 0 on all-passed, 1 on any failure.

set -u

# ─── locate worktree + binary ───────────────────────────────────────────────

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
WT_ROOT=$(cd "${SCRIPT_DIR}/../.." && pwd)
BIN="${WT_ROOT}/bin/endless-go"
SCHEMA="${WT_ROOT}/internal/schema/schema.sql"
CHANGE="${WT_ROOT}/internal/schema/changes/e-1683-add-session-tasks-do-order.sql"

if [[ ! -x "${BIN}" ]]; then
    printf 'ERROR: %s not found; run `just build` first.\n' "${BIN}" >&2
    exit 1
fi

# ─── output ─────────────────────────────────────────────────────────────────

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
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}

assert_eq() {
    local desc="$1" want="$2" got="$3"
    if [[ "${got}" == "${want}" ]]; then report_pass "${desc}"; else report_fail "${desc}" "${want}" "${got}"; fi
}
assert_emit_ok() {
    local desc="$1"; shift
    local out rc
    out=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; else report_fail "${desc}" "exit 0" "exit=${rc} | ${out}"; fi
}
assert_emit_fails() {
    local desc="$1" pattern="$2"; shift 2
    local out rc
    out=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]] && [[ "${out}" == *"${pattern}"* ]]; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "exit != 0 AND output ~ ${pattern}" "exit=${rc} | ${out}"
    fi
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── temp environment ───────────────────────────────────────────────────────

TMP=$(mktemp -d)
trap 'rm -rf "${TMP}"' EXIT
CFG="${TMP}/config"
mkdir -p "${CFG}"
DB="${CFG}/endless.db"
REPO="${TMP}/repo"
mkdir -p "${REPO}"
git -C "${REPO}" init -q
git -C "${REPO}" config user.email t@t && git -C "${REPO}" config user.name t

q() { sqlite3 "${DB}" "$1"; }
do_order() { q "SELECT COALESCE(do_order, 'NULL') FROM session_tasks WHERE session_id=42 AND task_id=$1;"; }

emit() {
    # --config-dir is a global pre-subcommand flag that pins the DB path and
    # wins over E-1368 cwd-based sandbox self-detection (this script runs from
    # inside the self-dev worktree, whose sandbox would otherwise be chosen).
    "${BIN}" --config-dir "${CFG}" event emit \
        --kind session_tasks.ordered \
        --project p \
        --entity-type session_tasks \
        --entity-id 0 \
        --actor-kind cli \
        --actor-id verify@host \
        --session-id 42 \
        --node-id 00a1 \
        --project-root "${REPO}" \
        --payload "$1"
}

seed_db() {
    sqlite3 "${DB}" < "${SCHEMA}" >/dev/null
    sqlite3 "${DB}" "
        INSERT INTO projects (id, name, path, status, created_at, updated_at)
          VALUES (1, 'p', '${REPO}', 'active', '2026-06-29T00:00:00', '2026-06-29T00:00:00');
        INSERT INTO sessions (id, state) VALUES (42, 'working');
        INSERT INTO session_tasks (session_id, task_id, created_at, updated_at) VALUES
          (42, 100, '2026-06-29T00:00:00', '2026-06-29T00:00:00'),
          (42, 101, '2026-06-29T00:00:00', '2026-06-29T00:00:00'),
          (42, 102, '2026-06-29T00:00:00', '2026-06-29T00:00:00'),
          (42, 103, '2026-06-29T00:00:00', '2026-06-29T00:00:00');
    "
}

# ─── checks ─────────────────────────────────────────────────────────────────

section "Schema — shipped schema.sql declares session_tasks.do_order"
seed_db
assert_eq "do_order column present" "do_order" \
    "$(q "SELECT name FROM pragma_table_info('session_tasks') WHERE name='do_order';")"
assert_eq "do_order is nullable (notnull=0)" "0" \
    "$(q "SELECT \"notnull\" FROM pragma_table_info('session_tasks') WHERE name='do_order';")"

section "Migration — change file adds do_order to a pre-migration DB"
PRE="${TMP}/pre.db"
# Build a pre-migration session_tasks (no do_order) then apply the change file.
sqlite3 "${PRE}" "
    CREATE TABLE session_tasks (
        id INTEGER PRIMARY KEY, session_id INTEGER NOT NULL, task_id INTEGER NOT NULL,
        created_at TEXT NOT NULL, updated_at TEXT NOT NULL, UNIQUE(session_id, task_id));
"
assert_eq "pre-migration table lacks do_order" "" \
    "$(sqlite3 "${PRE}" "SELECT name FROM pragma_table_info('session_tasks') WHERE name='do_order';")"
sqlite3 "${PRE}" < "${CHANGE}" >/dev/null
assert_eq "change file adds do_order" "do_order" \
    "$(sqlite3 "${PRE}" "SELECT name FROM pragma_table_info('session_tasks') WHERE name='do_order';")"

section "Executor — compact sequence + parallel (E-100 E-101|E-102 E-103 → 1,2,2,3)"
assert_emit_ok "emit session_tasks.ordered succeeds" \
    emit '{"process":"__session_id=42","groups":[["E-100"],["E-101","E-102"],["E-103"]]}'
assert_eq "E-100 do_order=1" "1" "$(do_order 100)"
assert_eq "E-101 do_order=2" "2" "$(do_order 101)"
assert_eq "E-102 do_order=2 (parallel with E-101)" "2" "$(do_order 102)"
assert_eq "E-103 do_order=3" "3" "$(do_order 103)"

section "Executor — replace-all resets omitted tasks to NULL"
assert_emit_ok "re-order with only E-103|E-100" \
    emit '{"process":"__session_id=42","groups":[["E-103","E-100"]]}'
assert_eq "E-103 do_order=1" "1" "$(do_order 103)"
assert_eq "E-100 do_order=1 (parallel)" "1" "$(do_order 100)"
assert_eq "E-101 reset to NULL (omitted)" "NULL" "$(do_order 101)"
assert_eq "E-102 reset to NULL (omitted)" "NULL" "$(do_order 102)"

section "Executor — foreign task id rejected, nothing mutated"
# Re-establish a known ordering, then attempt one that references E-999.
emit '{"process":"__session_id=42","groups":[["E-100"],["E-101"]]}' >/dev/null
assert_emit_fails "emit referencing E-999 is refused" "E-999" \
    emit '{"process":"__session_id=42","groups":[["E-100"],["E-999"]]}'
assert_eq "E-100 unchanged (do_order still 1)" "1" "$(do_order 100)"
assert_eq "E-101 unchanged (do_order still 2)" "2" "$(do_order 101)"

summary
