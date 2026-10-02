#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2158 and records what was true when E-2158
# landed. Edit it only if you ARE E-2158. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2158: delete the per-ticket change-file mechanism; goose is the only way
# the schema moves.
#
# THE CLAIMS
#   C1  internal/schema/changes/ and internal/schemachange/ are gone, and no
#       file outside .endless/ names the retired mechanism (the migrations
#       directory and the test that proves the drop are the only exceptions).
#   C2  endless-migrate offers exactly `up`; `apply` is an unknown command;
#       its build links nothing but dbcontext and the migration set.
#   C3  A fresh database reaches the new latest version with _schema_version
#       gone, and schema.sql agrees; a database stamped at the old latest loses
#       the table on its next `up`.
#   C4  A database older than both versioning schemes (projects, no
#       goose_db_version, user_version < 6) is refused — by endless-migrate up
#       and by endless-go's connect — with the too-old message, and is left
#       byte-for-byte schema-identical. A goose database at user_version 0
#       migrates normally.
#   C5  land.toml: absent or empty lands; `[self_dev] schema_order` is refused
#       before the merge by name as retired; any other key is refused.
#   C6  Python: a missing column names `endless db upgrade`; a Python read
#       runs no migration ladder.
#   C7  END TO END: a real self_dev land against a main database held at 00012
#       (with _schema_version) migrates and records in one run; the same land
#       carrying the retired key is refused with main and the DB untouched.
#
# Layers:
#   A. Fail-fast: build, and this task's Go and Python tests.
#   B. The claims by test name and by observation (C1-C6).
#   C. End to end through the real binaries (C4, C7).
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required to stage fixtures"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

# ─── A. fail-fast ───────────────────────────────────────────────────────────

section "A. The tree builds and this task's tests pass"

if out=$(just go 2>&1 && just migrate-bin 2>&1); then
    report_pass "just go && just migrate-bin"
else
    report_fail "just go && just migrate-bin" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    summary
fi

GO_PKGS=(./cmd/endless-migrate/ ./internal/schema/... ./internal/eventcmd/
         ./internal/events/ ./internal/monitor/)
if out=$(go test -count=1 "${GO_PKGS[@]}" 2>&1); then
    report_pass "go test: endless-migrate, schema, eventcmd, events, monitor"
else
    report_fail "go test: endless-migrate, schema, eventcmd, events, monitor" "exit 0" \
        "$(printf '%s' "${out}" | tail -30)"
    summary
fi

PY_TESTS=(tests/test_worktree_land_migrate_up.py
          tests/test_worktree_land_migrate_exec.py
          tests/test_worktree_land_schema_apply.py
          tests/test_db_error_diagnostic.py
          tests/test_db_gate.py
          tests/test_event_bridge_worktree_binary.py
          tests/test_guide_conditionals.py)
if out=$(uv run pytest -q "${PY_TESTS[@]}" 2>&1); then
    report_pass "pytest: land, db diagnostics, db gate, event bridge, guide"
else
    report_fail "pytest: land, db diagnostics, db gate, event bridge, guide" "exit 0" \
        "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# ─── B. the claims ──────────────────────────────────────────────────────────

section "B1. The mechanism is gone (C1)"

for d in internal/schema/changes internal/schemachange; do
    assert_eq "${d} tracks no files" "" "$(git ls-files -- "${d}")"
done

hits=$(git grep -n -E 'schema/changes|schemachange|apply-change|apply_change|_schema_version|change file|change-file|ENDLESS_AUTO_MIGRATE|_migrate_v[0-9]' \
    -- ':!.endless' ':!docs/research-*.tsv' \
       ':!internal/schema/migrations' ':!internal/schema/migrate_test.go' || true)
assert_eq "no file outside .endless/ names the retired mechanism" "" "${hits}"

section "B2. The claims, by test name (C2-C6)"

go_contract() {
    local pkg="$1" t="$2"
    if go test -count=1 "${pkg}" -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}"; then
        report_pass "go: ${t}"
    else
        report_fail "go: ${t}" "--- PASS: ${t}" "no PASS line"
    fi
}
go_contract ./cmd/endless-migrate/ TestMigrateExecutable_LinksNothingButTheMigrationMachinery
go_contract ./cmd/endless-migrate/ TestMigrateExecutable_OffersOnlyUp
go_contract ./cmd/endless-migrate/ TestMigrateExecutable_HasNoApplicationSurface
go_contract ./cmd/endless-migrate/ TestMigrateExecutable_UpRefusesADatabaseOlderThanBothVersioningSchemes
go_contract ./internal/schema/ TestMigrate_MatchesSchemaSQL
go_contract ./internal/schema/ TestMigrate_FreshDatabaseReachesLatest
go_contract ./internal/schema/ TestMigrate_LeavesAPreVersioningDatabaseIntact
go_contract ./internal/schema/ TestMigrate_RefusesADatabaseOlderThanBothVersioningSchemes
go_contract ./internal/schema/ TestMigrate_AGooseDatabaseAtUserVersionZeroMigrates

py_contract() {
    local node="$1"
    if out=$(uv run pytest -q "${node}" 2>&1) && [[ "${out}" == *passed* ]]; then
        report_pass "py: ${node##*::}"
    else
        report_fail "py: ${node##*::}" "passed" "$(printf '%s' "${out}" | tail -10)"
    fi
}
UP=tests/test_worktree_land_migrate_up.py
py_contract "${UP}::test_missing_or_empty_land_toml_lands"
py_contract "${UP}::test_retired_schema_order_is_refused_by_name"
py_contract "${UP}::test_land_toml_refusals_name_the_file_and_the_offender"
py_contract "${UP}::test_invalid_land_toml_refuses_before_the_merge"
py_contract "${UP}::test_up_runs_after_the_merge_and_before_the_record"
DIAG=tests/test_db_error_diagnostic.py
py_contract "${DIAG}::test_missing_column_names_the_column_and_db_upgrade"
py_contract "${DIAG}::test_python_read_does_not_modify_a_user_version_zero_db"

# ─── C. through the real binaries ───────────────────────────────────────────

section "C1. The real binaries surface (C2, C3)"

MIGRATE="${WT}/bin/endless-migrate"
out=$("${MIGRATE}" --help 2>&1)
assert_not_contains "endless-migrate --help offers no apply" "  apply" "${out}"
out=$("${MIGRATE}" apply x.sql 2>&1)
assert_contains "endless-migrate apply is an unknown command" "unknown command" "${out}"

LATEST=$(ls internal/schema/migrations/ | grep -E '^[0-9]+_' | sed -E 's/^0*([0-9]+)_.*/\1/' | sort -n | tail -1)
fresh="${TMP}/fresh"
mkdir -p "${fresh}" && : >"${fresh}/endless.db"
"${MIGRATE}" --db-dir "${fresh}" up >/dev/null 2>&1
assert_eq "a fresh database reaches the latest version (${LATEST})" "${LATEST}" \
    "$(sqlite3 "${fresh}/endless.db" 'SELECT max(version_id) FROM goose_db_version')"
assert_eq "a fresh database has no _schema_version" "0" \
    "$(sqlite3 "${fresh}/endless.db" "SELECT count(*) FROM sqlite_master WHERE name='_schema_version'")"

section "C2. The pre-versioning refusal, through both binaries (C4)"

# stage_ancient <dir> — Endless data, no goose_db_version, user_version 5.
stage_ancient() {
    mkdir -p "$1"
    sqlite3 "$1/endless.db" "CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT);
                             CREATE TABLE tasks (id INTEGER PRIMARY KEY);
                             PRAGMA user_version = 5;"
}
master() { sqlite3 "$1/endless.db" "SELECT type, name, sql FROM sqlite_master ORDER BY 1, 2"; }

old="${TMP}/ancient-migrate"
stage_ancient "${old}"
before=$(master "${old}")
if out=$("${MIGRATE}" --db-dir "${old}" up 2>&1); then
    report_fail "endless-migrate up refuses a pre-versioning database" "non-zero exit" "${out}"
else
    report_pass "endless-migrate up refuses a pre-versioning database"
fi
assert_contains "the refusal names the database" "${old}/endless.db" "${out}"
assert_contains "the refusal names its user_version" "user_version 5" "${out}"
assert_contains "the refusal says it is too old" "too old" "${out}"
assert_eq "endless-migrate left the database untouched" "${before}" "$(master "${old}")"

old="${TMP}/ancient-connect"
stage_ancient "${old}"
before=$(master "${old}")
out=$(cd "${TMP}" && "${WT}/bin/endless-go" --db-dir "${old}" event migrate 2>&1)
assert_contains "endless-go's connect refuses it with the too-old message" "too old" "${out}"
assert_eq "endless-go left the database untouched" "${before}" "$(master "${old}")"

goose0="${TMP}/goose0"
mkdir -p "${goose0}" && : >"${goose0}/endless.db"
"${MIGRATE}" --db-dir "${goose0}" up >/dev/null 2>&1
sqlite3 "${goose0}/endless.db" "PRAGMA user_version = 0;
    DELETE FROM goose_db_version WHERE version_id = ${LATEST};"
if out=$("${MIGRATE}" --db-dir "${goose0}" up 2>&1); then
    report_pass "a goose database at user_version 0 migrates normally"
else
    report_fail "a goose database at user_version 0 migrates normally" "exit 0" "${out}"
fi

section "C3. A real land against a main database held at 00012 (C7)"

# stage_behind_db <home> — a main database under <home>, built by a copy of
# this tree's endless-go OUTSIDE the worktree path, then held at 00012 with the
# retired _schema_version table restored.
stage_behind_db() {
    local home="$1" db="$1/.config/endless/endless.db"
    mkdir -p "${home}"
    cp "${WT}/bin/endless-go" "${TMP}/installed-endless-go"
    (cd "${TMP}" && HOME="${home}" XDG_CONFIG_HOME= \
        "${TMP}/installed-endless-go" --db main event migrate >/dev/null) \
        || setup_error "could not build the scratch main database"
    sqlite3 "${db}" "CREATE TABLE _schema_version (name TEXT PRIMARY KEY,
                         applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')));
                     DELETE FROM goose_db_version WHERE version_id >= 13;" \
        || setup_error "could not hold the scratch database at 00012"
    printf '{}\n' >"${home}/.config/endless/config.json"
    [[ "$(sqlite3 "${db}" 'SELECT max(version_id) FROM goose_db_version')" == 12 ]] \
        || setup_error "the scratch database is not at 00012"
}

land() {
    local home="${TMP}/home-$1"
    stage_behind_db "${home}"
    OUT=$(cd "${TMP}" && HOME="${home}" XDG_CONFIG_HOME= \
        uv run --project "${WT}" python "${ENDLESS_VERIFY_DIR}/land_e2e.py" \
            "${WT}" "${TMP}/repo-$1" "$1" 2>&1)
}

land fixed
assert_contains "fixed: the database starts with _schema_version" \
    "schema_version_before=yes" "${OUT}"
assert_contains "fixed: the land completes" "outcome=landed" "${OUT}"
assert_contains "fixed: main advances" "main_advanced=yes" "${OUT}"
assert_contains "fixed: the database reaches 00013" "db_version=13" "${OUT}"
assert_contains "fixed: _schema_version is gone" "schema_version_after=no" "${OUT}"
assert_contains "fixed: the landing is recorded in the same run" "landings=1" "${OUT}"

land retired
assert_contains "retired: the land is refused, naming the retired key" \
    "schema_order, which is retired" "${OUT}"
assert_contains "retired: refused before the merge" "main_advanced=no" "${OUT}"
assert_contains "retired: the database was not migrated" "db_version=12" "${OUT}"
assert_contains "retired: nothing was recorded" "landings=0" "${OUT}"

summary
