#!/usr/bin/env bash
#
# E-2036 verification — a missing column is reported as a missing column.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-2036
#
# WHAT LANDED
#   src/endless/db.py routed EVERY "no such table" / "no such column" from
#   execute()/query()/scalar() into _missing_schema_hint(), whose message is
#   "endless database is uninitialized at <path> … db file: exists (N bytes)
#   but has no endless schema", followed by advice about XDG_CONFIG_HOME. One
#   unapplied schema change was therefore reported as a database that had never
#   been initialized — on E-1920, `decision show` selected a column an
#   unapplied change adds and got that message while `decision list`, which did
#   not select it, worked in the same second.
#
#   The handler now asks a second question before it answers. If the database
#   has no endless schema at all, the old message is still the right one and is
#   unchanged. If it HAS the schema and lacks one object, the message names the
#   object, the outstanding change file that adds it, and the `endless db
#   apply-change` line that applies it. When nothing outstanding adds it, the
#   message says so and points at the query instead — "out of date" would send
#   the reader after a migration that does not exist.
#
#   Two smaller things came along, both the same root cause: INSERT's phrasing
#   ("table T has no column named C") was not recognized at all and surfaced as
#   a bare OperationalError, and the failing statement was never printed.
#
# WHAT THIS SUITE HAS TO PROVE
#   Three failure modes, none of which a source grep reaches:
#
#     1. The new branch never fires. The classifier is on the error TEXT, so a
#        phrasing it misses silently falls back to the old message. Layer A
#        drives the real `endless` against real databases that are missing a
#        real column and a real view, and requires the new diagnosis.
#     2. The old message is lost. It is correct — for a database that genuinely
#        has no schema, which is the case E-1160 wrote it for. Layer A's third
#        fixture is that database, and requires the old message verbatim.
#     3. The named file is wrong. Naming a change that does NOT add the object,
#        or one already recorded in _schema_version, sends the reader to
#        re-run a no-op. Layer A pins the file by name against the real
#        internal/schema/changes/; layer B pins the marker and DDL-vs-mention
#        logic against synthetic ones.
#
# Layers:
#   A. FAIL-FAST — real `endless`, real databases, the three diagnoses.
#      If this does not hold, nothing below matters.
#   B. The diagnosis logic (pytest, worktree source).
#   C. Project-wide regression — build, vet, go test, Python suite.
#
# Every fixture is a throwaway database under a temp XDG_CONFIG_HOME, with cwd
# in a temp non-self-dev project, so the real ledger and this worktree's sandbox
# are never touched.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 setup error.

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

REPO_ROOT=""
TMP_DIR=""
ENDLESS_BIN=""     # the worktree's editable venv build — the candidate code

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'
    BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
note()    { printf '  %s%s%s\n' "${DIM}" "$1" "${RESET}"; }

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
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" \
        "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_contains DESC HAYSTACK NEEDLE — whitespace-collapsed, so an assertion
# pins the wording and not the column alignment.
assert_contains() {
    local desc="$1" hay needle
    hay=$(printf '%s' "$2" | tr -s '[:space:]' ' ')
    needle=$(printf '%s' "$3" | tr -s '[:space:]' ' ')
    if [[ "${hay}" == *"${needle}"* ]]; then report_pass "${desc}"; return 0; fi
    report_fail "${desc}" "output contains '$3'" "$(printf '%s' "$2" | head -4)"
    return 1
}

# assert_lacks DESC HAYSTACK NEEDLE
assert_lacks() {
    local desc="$1" hay needle
    hay=$(printf '%s' "$2" | tr -s '[:space:]' ' ')
    needle=$(printf '%s' "$3" | tr -s '[:space:]' ' ')
    if [[ "${hay}" != *"${needle}"* ]]; then report_pass "${desc}"; return 0; fi
    report_fail "${desc}" "output does NOT contain '$3'" "$(printf '%s' "$2" | head -4)"
    return 1
}

# assert_cmd DESC CMD... — plain exit-0 check for the regression layer.
assert_cmd() {
    local desc="$1"; shift
    local output rc
    output=$(cd "${REPO_ROOT}" && "$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return 0; fi
    report_fail "${desc}" "exit 0" "$(printf '%s' "${output}" | tail -5)"
    return 1
}

# ─── fixtures ───────────────────────────────────────────────────────────────

# make_fixture NAME MUTATION_SQL — a temp XDG_CONFIG_HOME whose endless.db is
# built from THIS tree's schema.sql and then damaged by MUTATION_SQL, plus a
# temp project for cwd.
#
# Dropping an object out of the current schema is how an out-of-date database
# is reproduced without archaeology: `tasks.type_id` and the `live_tasks` view
# are each added to an existing database by a real change file that is still on
# disk, so what the fixture is missing is exactly what an unapplied change
# supplies. Nothing here disables migration — the fixture faces the same
# get_db() path a user's database does.
make_fixture() {
    local name="$1" mutation="$2"
    local root="${TMP_DIR}/${name}"
    mkdir -p "${root}/config/endless" "${root}/proj/.endless" || return 1
    printf '{"name": "e2036-%s"}\n' "${name}" > "${root}/proj/.endless/config.json"
    SCHEMA="${REPO_ROOT}/internal/schema/schema.sql" \
    DB="${root}/config/endless/endless.db" \
    PROJ="${root}/proj" NAME="e2036-${name}" MUTATION="${mutation}" \
    python3 - <<'PY'
import os, sqlite3
conn = sqlite3.connect(os.environ["DB"])
conn.executescript(open(os.environ["SCHEMA"]).read())
if os.environ["MUTATION"]:
    conn.executescript(os.environ["MUTATION"])
conn.execute(
    "INSERT INTO projects (name, path, status, created_at, updated_at) "
    "VALUES (?, ?, 'active', '2026-08-21T00:00:00', '2026-08-21T00:00:00')",
    (os.environ["NAME"], os.environ["PROJ"]),
)
conn.commit()
conn.close()
PY
}

# make_empty_fixture NAME — a database file with no schema at all: the case the
# "uninitialized" message was written for, and must keep.
make_empty_fixture() {
    local name="$1"
    local root="${TMP_DIR}/${name}"
    mkdir -p "${root}/config/endless" "${root}/proj/.endless" || return 1
    printf '{"name": "e2036-%s"}\n' "${name}" > "${root}/proj/.endless/config.json"
    DB="${root}/config/endless/endless.db" python3 - <<'PY'
import os, sqlite3
sqlite3.connect(os.environ["DB"]).close()
PY
}

# endless_in NAME ARGS... — the candidate `endless`, cwd inside the fixture
# project, resolving the fixture's database. cwd is load-bearing twice: it
# picks the project, and it keeps this out of the self-dev worktree where
# `--db` would be mandatory.
endless_in() {
    local name="$1"; shift
    (cd "${TMP_DIR}/${name}/proj" \
     && XDG_CONFIG_HOME="${TMP_DIR}/${name}/config" "${ENDLESS_BIN}" "$@" 2>&1)
}

# raw_sqlite_error NAME SQL — what sqlite3 itself says, for the counterfactual.
raw_sqlite_error() {
    local name="$1" sql="$2"
    DB="${TMP_DIR}/${name}/config/endless/endless.db" SQL="${sql}" python3 - <<'PY'
import os, sqlite3
conn = sqlite3.connect(os.environ["DB"])
schemad = conn.execute(
    "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='projects'"
).fetchone()[0] > 0
try:
    conn.execute(os.environ["SQL"])
    print("NO ERROR")
except sqlite3.OperationalError as e:
    print(f"{e}|schemad={schemad}")
PY
}

# ─── setup ──────────────────────────────────────────────────────────────────

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || {
        printf 'setup error: not inside a git repository\n' >&2
        exit 2
    }
    if [[ ! -f "${REPO_ROOT}/src/endless/db.py" ]]; then
        printf 'setup error: src/endless/db.py not found — is this the E-2036 worktree?\n' >&2
        exit 2
    fi
    for tool in go uv git python3 just; do
        command -v "${tool}" >/dev/null 2>&1 || {
            printf 'setup error: %s not on PATH\n' "${tool}" >&2
            exit 2
        }
    done

    TMP_DIR=$(mktemp -d) || { printf 'setup error: mktemp failed\n' >&2; exit 2; }
    trap 'rm -rf "${TMP_DIR}"' EXIT

    if ! (cd "${REPO_ROOT}" && uv sync --quiet) 2>"${TMP_DIR}/sync.err"; then
        printf 'setup error: uv sync failed:\n%s\n' "$(cat "${TMP_DIR}/sync.err")" >&2
        exit 2
    fi
    ENDLESS_BIN="${REPO_ROOT}/.venv/bin/endless"
    [[ -x "${ENDLESS_BIN}" ]] || {
        printf 'setup error: %s missing after uv sync\n' "${ENDLESS_BIN}" >&2
        exit 2
    }

    # tasks.type_id — added to existing databases by e-1538-task-types-fk.sql.
    make_fixture stale-column "ALTER TABLE tasks DROP COLUMN type_id;" || exit 2
    # the live_tasks view — created on existing databases by e-1929-add-tasks-removed.go.
    make_fixture stale-view "DROP VIEW live_tasks;" || exit 2
    make_empty_fixture no-schema || exit 2
}

# ─── layer A: fail-fast — the three diagnoses, end to end ───────────────────

layer_a() {
    section "A. Real \`endless\`, real databases: what each failure is called"
    note "the defect was a message, so only a real command against a real database settles it"

    # --- A1: the reported symptom. A column one unapplied change adds. -------
    local raw out
    raw=$(raw_sqlite_error stale-column "SELECT type_id FROM tasks")
    assert_contains "fixture reproduces the error the old code misrouted" \
        "${raw}" "no such column: type_id|schemad=True" || return 1
    note "sqlite says 'no such column'; the database HAS the endless schema — the old"
    note "predicate matched on that prefix alone and answered 'uninitialized'"

    out=$(endless_in stale-column task show E-1)
    assert_contains "names the missing column" "${out}" "missing column: type_id" || return 1
    assert_lacks "does not call the database uninitialized" \
        "${out}" "endless database is uninitialized" || return 1
    assert_lacks "does not send the reader to XDG_CONFIG_HOME" \
        "${out}" "XDG_CONFIG_HOME" || return 1
    assert_contains "names the change file that adds it" \
        "${out}" "e-1538-task-types-fk.sql" || return 1
    assert_contains "hands over the command that applies it" \
        "${out}" "apply it: endless db apply-change" || return 1
    assert_contains "prints the statement that failed" "${out}" "query: SELECT" || return 1

    # A change that merely mentions type_id in prose must not be offered as the
    # fix; only the one whose DDL adds it.
    assert_lacks "does not offer a change that only mentions the column" \
        "${out}" "e-1571-sessions-active-epic-and-kind.go" || return 1

    # --- A2: the same treatment for a missing table ------------------------
    out=$(endless_in stale-view task list)
    assert_contains "names the missing table" "${out}" "missing table: live_tasks" || return 1
    assert_contains "names the change file that creates it" \
        "${out}" "e-1929-add-tasks-removed.go" || return 1
    assert_lacks "missing table is not called uninitialized either" \
        "${out}" "endless database is uninitialized" || return 1

    # --- A3: the old message, where it is the true one ---------------------
    out=$(endless_in no-schema task list)
    assert_contains "a schema-less database is still called uninitialized" \
        "${out}" "endless database is uninitialized" || return 1
    assert_contains "and still names the resolution mechanism" \
        "${out}" "resolved via XDG_CONFIG_HOME" || return 1
    assert_contains "and still names the file's state" \
        "${out}" "but has no endless schema" || return 1
    assert_lacks "and is not mistaken for an out-of-date one" \
        "${out}" "schema is out of date" || return 1

    return 0
}

# ─── layer B: the diagnosis logic ───────────────────────────────────────────

layer_b() {
    section "B. The diagnosis logic"
    note "the _schema_version marker, DDL-vs-mention, the INSERT phrasing, the absent changes dir"

    assert_cmd "tests/test_db_error_diagnostic.py passes against worktree source" \
        uv run pytest tests/test_db_error_diagnostic.py -q
}

# ─── layer C: project-wide regression ───────────────────────────────────────

layer_c() {
    section "C. Project-wide regression"
    note "every endless command reads through db.query(); the blast radius is the whole CLI"

    assert_cmd "go build ./..." go build ./...
    assert_cmd "go vet ./..."   go vet ./...

    # -timeout 20m for the PRE-EXISTING sandboxcmd destroy-test slowness tracked
    # as E-1908, not anything this task introduced. Drop the flag once it lands.
    assert_cmd "go test ./... (-timeout 20m; see E-1908)" go test -timeout 20m ./...

    assert_cmd "just test (Python suite)" just test
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup
    printf '%sE-2036 — a missing column is reported as a missing column%s\n' \
        "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  candidate: %s\n  fixtures:  %s\n' "${ENDLESS_BIN}" "${TMP_DIR}"

    if ! layer_a; then
        note "fail-fast: the diagnosis is wrong at the surface; skipping later layers"
        summary
        return 1
    fi
    layer_b
    layer_c

    summary
}

main "$@"
