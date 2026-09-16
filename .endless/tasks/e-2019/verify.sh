#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2019 and records what was true when E-2019
# landed. Edit it only if you ARE E-2019. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2019 verification — goose owns the schema.
#
# WHAT CHANGED:
#
#   internal/schema stops being a string of SQL that every database-creation
#   path exec'd, and becomes a versioned migration set driven by pressly/goose
#   as a library over an existing *sql.DB. The set is embedded with //go:embed,
#   so it ships inside the binary rather than being read off a checkout that may
#   not be installed. schema.Migrate is the single way a database acquires its
#   schema: monitor.DB(), the sandbox seeder, the ledger projector, 72 test call
#   sites, and — through `endless-go event migrate` — the Python CLI.
#
#   WHEN the schema is applied did not change. monitor.DB() still brings a
#   database up to date on every connect; it just does it from the migration set
#   instead of schema.sql. E-2020 is what changes the timing, and E-2158 is what
#   deletes the change-file mechanism this task deliberately left standing.
#
# THE ONE DATABASE THAT MATTERS:
#
#   There is exactly one database in the world that predates versioning — the
#   ledger at ~/.config/endless/endless.db. It carries the shape and no version,
#   so its first connect after this task must be INERT: create nothing, destroy
#   nothing, leave the rows alone, and come out recorded at version 1.
#
#   That is why 00001_baseline.sql is idempotent and why nothing stamps. SQLite
#   does not keep IF NOT EXISTS in sqlite_master, so the generated dump came back
#   without it and the generator puts it back — for this migration and no other.
#   Check 2 is that property. It was also run for real, once, against a 127MB
#   VACUUM INTO snapshot of the live ledger (1,448 tasks, 1,199 sessions): shape
#   identical, row counts identical, 40ms, version 1.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-2019
#
# Requires `just build` first — every replay drives the CANDIDATE bin/endless-go,
# not the global install. (Setup builds it if it is missing.) Replays go through
# `endless-go event migrate` rather than a Go snippet, so what is verified is the
# connect a user's command actually performs, gates and all, and the suite never
# writes a file into the source tree.
#
# What it checks:
#   0. FAIL-FAST: go build, and this task's own unit tests. A failure here
#      short-circuits the rest — the remaining checks would report on rubble.
#   1. The baseline replays to exactly the schema.sql PINNED beside this suite.
#   2. A pre-versioning database is left intact and recorded at the baseline.
#   3. A fresh database reaches the latest version and passes the enum gates.
#   4. The migration set is embedded, not read from the checkout.
#   5. Python no longer builds a database from schema.sql.
#   6. No cgo.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WORKTREE="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
PINNED_SCHEMA="${ENDLESS_VERIFY_DIR}/schema-at-land.sql"
GO_BIN="${WORKTREE}/bin/endless-go"
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

cd "${WORKTREE}" || setup_error "cannot enter the worktree at ${WORKTREE}"
[[ -f "${PINNED_SCHEMA}" ]] || setup_error "the pinned schema fixture is missing: ${PINNED_SCHEMA}"
command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required by this suite"

# shape renders a database's schema as one comparable, whitespace-collapsed
# string. SQLite's own objects and goose's bookkeeping are excluded: neither is
# the schema, and including the version table would make every comparison
# between a migrated and an unmigrated database trivially false.
shape() {
    sqlite3 "file:$1?mode=ro" \
        "SELECT type||' '||name||': '||COALESCE(sql,'')
           FROM sqlite_master
          WHERE name NOT LIKE 'sqlite_%' AND name <> 'goose_db_version'
          ORDER BY type, name;" 2>/dev/null | tr -s ' \t\n' ' '
}

# ---------------------------------------------------------------------------
section "0. Fail-fast: the build, and this task's own tests"

if ! build_out="$(go build ./... 2>&1)"; then
    report_fail "go build ./... succeeds" "a clean build" "${build_out}"
    summary
fi
report_pass "go build ./... succeeds"

if ! test_out="$(go test ./internal/schema/... 2>&1)"; then
    report_fail "the migration unit tests pass" "ok" "${test_out}"
    summary
fi
report_pass "the migration unit tests pass"

if [[ ! -x "${GO_BIN}" ]]; then
    go build -o "${GO_BIN}" ./cmd/endless-go \
        || setup_error "could not build the candidate bin/endless-go"
fi

# ---------------------------------------------------------------------------
section "1. The baseline replays to the schema pinned at land time"

# Pinned, not live. schema.sql is still committed and E-2021 will make it
# generated; comparing against the live file would let a later edit silently
# redefine what the baseline is claimed to be. The live comparison is the
# DURABLE test's job (internal/schema: TestMigrate_MatchesSchemaSQL), which is
# supposed to move. This one never can.
sqlite3 "${WORK}/pinned.db" < "${PINNED_SCHEMA}" >/dev/null 2>&1 \
    || setup_error "the pinned schema would not build a database"

# shape() discards sqlite3's errors, so a dump that failed would come back empty
# — and two empty dumps compare equal. Refuse to compare anything until a known
# database is proven to render as more than nothing, or every "identical" below
# could be a vacuous pass.
[[ "$(shape "${WORK}/pinned.db" | grep -o 'table tasks:' || true)" == "table tasks:" ]] \
    || setup_error "shape() rendered the pinned database without its tasks table; comparisons would be vacuous"

# migrate runs the candidate binary's connect against a database directory.
migrate() { "${GO_BIN}" --db-dir "$1" event migrate 2>&1; }

mkdir -p "${WORK}/fresh/endless"
# Through the real binary, so the connect that runs is monitor.DB() — which
# applies the migrations AND runs the four fail-closed enum integrity gates
# (task_types, process_kinds, gate_kinds, session_task_relations). A successful
# exit is itself the assertion that the seeds survived the replay: a database
# with the right shape and no enum rows fails those gates on its first connect.
if ! fresh_out="$(migrate "${WORK}/fresh/endless")"; then
    setup_error "migrating a fresh database failed: ${fresh_out}"
fi
FRESH_DB="${WORK}/fresh/endless/endless.db"

assert_eq "a migrated database is schema-identical to the pinned schema.sql" \
    "$(shape "${WORK}/pinned.db")" "$(shape "${FRESH_DB}")"

# ---------------------------------------------------------------------------
section "2. A pre-versioning database is left intact, not replayed onto"

# Built the way the real ledger was: exec the schema, record no version.
mkdir -p "${WORK}/legacy/endless"
LEGACY_DB="${WORK}/legacy/endless/endless.db"
cp "${WORK}/pinned.db" "${LEGACY_DB}"
sqlite3 "${LEGACY_DB}" \
    "INSERT INTO projects (id, name, path) VALUES (1, 'alpha', '~/a');
     INSERT INTO tasks (id, project_id, title) VALUES (7, 1, 'survives');" >/dev/null 2>&1 \
    || setup_error "could not seed the legacy database"

assert_eq "it carries no version table before migrating" "0" \
    "$(sqlite3 "file:${LEGACY_DB}?mode=ro" \
        "SELECT count(*) FROM sqlite_master WHERE name='goose_db_version';")"

legacy_before="$(shape "${LEGACY_DB}")"
if ! legacy_out="$(migrate "${WORK}/legacy/endless")"; then
    report_fail "migrating a pre-versioning database succeeds" "no error" "${legacy_out}"
else
    report_pass "migrating a pre-versioning database succeeds"
fi

assert_eq "the replay changed nothing about its schema" \
    "${legacy_before}" "$(shape "${LEGACY_DB}")"

assert_eq "the rows it already held survived" "survives" \
    "$(sqlite3 "file:${LEGACY_DB}?mode=ro" "SELECT title FROM tasks WHERE id=7;")"

assert_eq "it is now recorded at the baseline version" "1" \
    "$(sqlite3 "file:${LEGACY_DB}?mode=ro" \
        "SELECT max(version_id) FROM goose_db_version WHERE is_applied;")"

# ---------------------------------------------------------------------------
section "3. A fresh database reaches the latest version and passes the enum gates"

assert_contains "endless-go event migrate reports status ok" '"status":"ok"' "${fresh_out}"

version="$(printf '%s' "${fresh_out}" | sed -n 's/.*"version":\([0-9]*\).*/\1/p')"
latest="$(printf '%s' "${fresh_out}" | sed -n 's/.*"latest":\([0-9]*\).*/\1/p')"
assert_eq "the fresh database is at the binary's latest version" "${latest}" "${version}"

assert_eq "the enum mirrors are populated (task_types|process_kinds|gate_kinds|relations)" \
    "5|2|2|5" \
    "$(sqlite3 "file:${FRESH_DB}?mode=ro" \
        "SELECT (SELECT count(*) FROM task_types)||'|'||
                (SELECT count(*) FROM process_kinds)||'|'||
                (SELECT count(*) FROM gate_kinds)||'|'||
                (SELECT count(*) FROM session_task_relations);")"

# ---------------------------------------------------------------------------
section "4. The migration set is embedded, not read from the checkout"

# The binaries are symlinked into /usr/local/bin from a checkout the user is
# free to move or not to have. Run from a directory that is not the checkout,
# with no relative path back to internal/schema/migrations, and a complete
# database must still come out.
mkdir -p "${WORK}/elsewhere/endless"
if ! away_out="$(cd "${WORK}/elsewhere" && "${GO_BIN}" --db-dir "${WORK}/elsewhere/endless" event migrate 2>&1)"; then
    report_fail "migrating from outside the checkout succeeds" "ok" "${away_out}"
else
    report_pass "migrating from outside the checkout succeeds"
fi

assert_eq "the database it built there has the full schema" \
    "$(shape "${FRESH_DB}")" "$(shape "${WORK}/elsewhere/endless/endless.db")"

# ---------------------------------------------------------------------------
section "5. Python no longer builds a database from schema.sql"

# Scoped to the construction path on purpose. db.py still holds DDL — the legacy
# _migrate / _migrate_v2..v6 ladder that repairs databases older than the Go
# schema — and deleting that is E-2158's, not this task's. What E-2019 claims
# is narrower and exact: a NEW database is built by Go, and Python neither reads
# schema.sql's contents nor executes it to get one.
init_schema_body="$(awk '/^def _init_schema\(/{on=1; print; next} on && /^def /{exit} on{print}' src/endless/db.py)"

[[ -n "${init_schema_body}" ]] || setup_error "could not find _init_schema in src/endless/db.py"

assert_not_contains "_init_schema executes no SQL itself" \
    "executescript" "${init_schema_body}"

assert_contains "_init_schema builds the database by shelling out to Go" \
    "event_bridge.init_schema()" "${init_schema_body}"

schema_reads="$(grep -c '_SCHEMA_PATH.read_text' src/endless/db.py || true)"
assert_eq "nothing in db.py reads schema.sql's contents any more" "0" "${schema_reads}"

bridge_call="$(grep -o '"event", "migrate"' src/endless/event_bridge.py || true)"
assert_eq "the bridge invokes the migrate verb" '"event", "migrate"' "${bridge_call}"

# ---------------------------------------------------------------------------
section "6. No cgo"

# modernc.org/sqlite is a pure-Go driver and goose must not have quietly
# introduced a dependency that needs a C toolchain.
if ! cgo_out="$(CGO_ENABLED=0 go build ./... 2>&1)"; then
    report_fail "CGO_ENABLED=0 go build ./... succeeds" "a clean build" "${cgo_out}"
else
    report_pass "CGO_ENABLED=0 go build ./... succeeds"
fi

summary
