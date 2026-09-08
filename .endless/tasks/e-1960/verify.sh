#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1960 and records what was true when E-1960
# landed. Edit it only if you ARE E-1960. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1960 verification — "Add the missing project qualifier to the errors table
# and fault detail log".
#
# WHAT LANDED
#   One Endless database holds every project on the machine, but the `errors`
#   table E-698 created had no project column and the JSONL detail line had no
#   project field. Every recorded fault from every project landed in one
#   undifferentiated table: `errors show` could not filter, the status badge
#   could not scope, and the partial unique index on (source, code, fingerprint)
#   COLLIDED across unrelated projects — two projects hitting the same condition
#   became ONE incident with a doubled count.
#
#   Landed: errors.project_id (FK to projects, ON DELETE SET NULL), a
#   project-qualified open-incident index, Detail.project, faults.ProjectScope
#   over the read/clear paths, `errors show|clear --project/--all-projects`, and
#   a project-scoped badge on the project board.
#
# THE CLAIMS
#   C1  TWO PROJECTS NO LONGER COLLIDE. The same code+source+fingerprint raised
#       from two projects opens TWO incidents, one each, not one with two.
#   C2  UNATTRIBUTED FAULTS STILL DEDUPE. The index is over
#       COALESCE(project_id, 0), not the bare column, because SQLite treats
#       NULLs as distinct — a bare column would insert a fresh row per tmux
#       status-bar tick instead of bumping one.
#   C3  THE MIGRATION RUNS ON A REAL PRE-E-1960 DATABASE. schema.SQL survives
#       the connect that precedes it (the index keeps E-698's name so
#       IF NOT EXISTS short-circuits), the change file then adds the column and
#       swaps the index, existing rows keep project_id NULL, and re-running is a
#       no-op.
#   C4  `errors show` IS SCOPED, AND SAYS SO. Default = the project you are in
#       plus the unattributed ones, no PROJECT column. --all-projects widens and
#       adds the column. --project names another. Outside any project it falls
#       back to machine-wide.
#   C5  `errors clear` CLEARS WHAT `show` SHOWED. A no-id clear covers the same
#       scope, leaving other projects alone; a clear BY ID ignores the scope,
#       because the user named the row.
#   C6  THE DETAIL LOG CARRIES THE PROJECT BY NAME. That file is read without a
#       database and is shared by every project on the machine. An unattributed
#       fault omits the key rather than claiming a project.
#   C7  A FAULT IS NEVER LOST TO ITS OWN ATTRIBUTION. An id naming a missing
#       project is rejected by the foreign key; the report is still recorded,
#       unattributed. Proven by the unit tests in the fail-fast step below — no
#       CLI surface can hand Record a stale project id, so there is nothing to
#       drive it with from out here.
#   C8  UNREGISTERING A PROJECT DOES NOT DELETE ITS ERRORS. The foreign key is
#       ON DELETE SET NULL: the machine-local record of what went wrong while a
#       project existed outlives the project row.
#
# ISOLATION
#   Every database here is built under a mktemp dir and reached with an explicit
#   --config-dir / ENDLESS_CHANGE_DB. Nothing touches the real config, the main
#   database, or this worktree's sandbox.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot make a temp dir"
trap 'rm -rf "${TMP}"' EXIT

command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required to inspect the schema"

# ── Fail-fast: this task's own unit tests ─────────────────────────────────────
section "Unit tests (fail fast)"

if go test ./internal/faults/ ./internal/schema/ ./internal/projectstatuscmd/ \
        ./internal/sessionstatuscmd/ ./internal/faultbadge/ \
        >"${TMP}/go.log" 2>&1; then
    report_pass "go test — attribution, the migration's ordering trap, and both badges' scoping"
else
    report_fail "go test ./internal/{faults,schema,projectstatuscmd,sessionstatuscmd,faultbadge}" \
        "exit 0" "$(tail -40 "${TMP}/go.log")"
    summary
fi

# The Python half is argv construction: the Go side decides the scope, Python
# only has to build the flags — and the CLI inside a worktree is the GLOBAL
# install, so this is the only place those options can be exercised before the
# land.
if uv run pytest -q tests/test_errors_project_scope.py >"${TMP}/py.log" 2>&1; then
    report_pass "pytest — the --project/--all-projects flags reach endless-go, in that order"
else
    report_fail "pytest tests/test_errors_project_scope.py" \
        "exit 0" "$(tail -30 "${TMP}/py.log")"
    summary
fi

# ── The binary under test ─────────────────────────────────────────────────────
BIN="${TMP}/endless-go"
if ! go build -o "${BIN}" ./cmd/endless-go >"${TMP}/build.log" 2>&1; then
    setup_error "cannot build endless-go: $(tail -5 "${TMP}/build.log")"
fi

# ── C3: the migration, against a real pre-E-1960 database ─────────────────────
section "C3 — the migration runs on a pre-E-1960 database"

OLD_DB="${TMP}/pre.db"
sqlite3 "${OLD_DB}" <internal/schema/schema.sql >/dev/null 2>&1 \
    || setup_error "cannot build a fresh DB from schema.sql"

# Roll `errors` back to E-698's shape: no project_id, the old index.
sqlite3 "${OLD_DB}" "
    DROP INDEX IF EXISTS idx_errors_open_uniq;
    ALTER TABLE errors DROP COLUMN project_id;
    CREATE UNIQUE INDEX idx_errors_open_uniq
        ON errors(source, code, fingerprint) WHERE cleared_at IS NULL;
    INSERT INTO projects (id, name, path) VALUES (1,'alpha','~/a'), (2,'beta','~/b');
    INSERT INTO errors (code, severity, source, fingerprint, summary)
        VALUES ('ERR-0001','warning','s','f','a fault from before the migration');
" >/dev/null 2>&1 || setup_error "cannot build the pre-migration shape"

assert_eq "the pre-migration DB really has no project_id" "0" \
    "$(sqlite3 "${OLD_DB}" "SELECT count(*) FROM pragma_table_info('errors') WHERE name='project_id'")"

# The connect `endless db apply-change` performs BEFORE dispatching to the
# change script. A differently-named index over project_id would abort here and
# the migration could never run — the deadlock the index name exists to avoid.
if sqlite3 "${OLD_DB}" <internal/schema/schema.sql >"${TMP}/schema.log" 2>&1; then
    report_pass "schema.SQL applies to a DB predating errors.project_id"
else
    report_fail "schema.SQL applies to a DB predating errors.project_id" \
        "exit 0" "$(tail -5 "${TMP}/schema.log")"
fi
assert_not_contains "and left E-698's index definition alone (IF NOT EXISTS short-circuited)" \
    "project_id" "$(sqlite3 "${OLD_DB}" "SELECT sql FROM sqlite_master WHERE name='idx_errors_open_uniq'")"

CHANGE="internal/schema/changes/e-1960-add-errors-project-id.go"
if ENDLESS_CHANGE_DB="${OLD_DB}" go run "${CHANGE}" >"${TMP}/change.log" 2>&1; then
    report_pass "the change file applies"
else
    report_fail "the change file applies" "exit 0" "$(tail -10 "${TMP}/change.log")"
fi
assert_eq "errors.project_id now exists" "1" \
    "$(sqlite3 "${OLD_DB}" "SELECT count(*) FROM pragma_table_info('errors') WHERE name='project_id'")"
assert_contains "the open-incident index is project-qualified" "COALESCE(project_id, 0)" \
    "$(sqlite3 "${OLD_DB}" "SELECT sql FROM sqlite_master WHERE name='idx_errors_open_uniq'")"
assert_eq "the pre-migration row survives, unattributed (NULL, not backfilled)" "1|1" \
    "$(sqlite3 "${OLD_DB}" "SELECT count(*), sum(project_id IS NULL) FROM errors")"

# C1 at the storage layer: the index itself now separates projects.
sqlite3 "${OLD_DB}" "
    INSERT INTO errors (project_id,code,severity,source,fingerprint,summary)
        VALUES (1,'ERR-0002','error','x','y','same everywhere'),
               (2,'ERR-0002','error','x','y','same everywhere');
" >/dev/null 2>&1
assert_eq "two projects hold their own open incident for one fingerprint" "2" \
    "$(sqlite3 "${OLD_DB}" "SELECT count(*) FROM errors WHERE fingerprint='y'")"

# C2 at the storage layer: NULL-project rows still collide with each other.
sqlite3 "${OLD_DB}" "INSERT INTO errors (code,severity,source,fingerprint,summary)
                     VALUES ('ERR-0002','error','z','w','unattributed')" >/dev/null 2>&1
DUP="$(sqlite3 "${OLD_DB}" "INSERT INTO errors (code,severity,source,fingerprint,summary)
                            VALUES ('ERR-0002','error','z','w','unattributed')" 2>&1)"
assert_contains "a second unattributed row with the same fingerprint is refused" \
    "UNIQUE constraint failed" "${DUP}"

if ENDLESS_CHANGE_DB="${OLD_DB}" go run "${CHANGE}" >"${TMP}/change2.log" 2>&1; then
    assert_contains "re-running the change is a no-op" "already applied" "$(cat "${TMP}/change2.log")"
else
    report_fail "re-running the change is a no-op" "exit 0" "$(tail -5 "${TMP}/change2.log")"
fi

# ── The end-to-end fixture: two registered projects and one that is nowhere ───
CFG="${TMP}/cfg/endless"
mkdir -p "${CFG}" "${TMP}/tree/alpha" "${TMP}/tree/beta"
ALPHA="$(cd "${TMP}/tree/alpha" && pwd -P)"
BETA="$(cd "${TMP}/tree/beta" && pwd -P)"
NOWHERE="$(cd "${TMP}/tree" && pwd -P)"   # the PARENT: the walk up finds nothing

go_errors() { ( cd "$1" && shift && "${BIN}" --config-dir "${CFG}" errors "$@" ) 2>&1; }

go_errors "${NOWHERE}" show >/dev/null 2>&1   # creates the DB
sqlite3 "${CFG}/endless.db" \
    "INSERT INTO projects (name,path) VALUES ('alpha','${ALPHA}'),('beta','${BETA}')" \
    >/dev/null 2>&1 || setup_error "cannot register the fixture projects"

# One identical fault from each project, and one from outside any project.
go_errors "${ALPHA}"   raise --severity warning --summary "shared condition" --source "worktree:unsettled" >/dev/null
go_errors "${BETA}"    raise --severity warning --summary "shared condition" --source "worktree:unsettled" >/dev/null
go_errors "${NOWHERE}" raise --severity error   --summary "the machine's own failure" --source "jobs" >/dev/null

# ── C1: the collision, through the real recording path ────────────────────────
section "C1 — two projects raising one fault open two incidents"

assert_eq "two incidents, one occurrence each" "2|1" \
    "$(sqlite3 "${CFG}/endless.db" \
        "SELECT count(*), max(occurrences) FROM errors WHERE fingerprint =
           (SELECT fingerprint FROM errors WHERE summary='shared condition' LIMIT 1)" \
     | tr -d ' ')"
assert_eq "and they name their projects" "alpha|beta" \
    "$(sqlite3 "${CFG}/endless.db" \
        "SELECT group_concat(p.name,'|') FROM errors e JOIN projects p ON p.id=e.project_id
          WHERE e.summary='shared condition' ORDER BY p.name")"
assert_eq "the machine's own failure is attributed to no project" "1" \
    "$(sqlite3 "${CFG}/endless.db" \
        "SELECT count(*) FROM errors WHERE summary=\"the machine's own failure\" AND project_id IS NULL")"

# ── C4: `errors show` scoping ─────────────────────────────────────────────────
section "C4 — errors show is scoped to the project you are in"

IN_ALPHA="$(go_errors "${ALPHA}" show)"
assert_contains "inside alpha: alpha's own fault is listed" "worktree:unsettled" "${IN_ALPHA}"
assert_contains "inside alpha: the unattributed machine fault rides along" \
    "the machine's own failure" "${IN_ALPHA}"
assert_not_contains "inside alpha: no PROJECT column on a scoped listing" "PROJECT" "${IN_ALPHA}"
assert_eq "inside alpha: exactly two rows, not beta's as well" "2" \
    "$(printf '%s\n' "${IN_ALPHA}" | grep -c '^[0-9]')"

WIDE="$(go_errors "${ALPHA}" show --all-projects)"
assert_contains "--all-projects adds the PROJECT column" "PROJECT" "${WIDE}"
assert_contains "--all-projects names alpha" "alpha" "${WIDE}"
assert_contains "--all-projects names beta" "beta" "${WIDE}"
assert_contains "--all-projects marks the unattributed row with an em dash" "—" "${WIDE}"
assert_eq "--all-projects lists all three" "3" \
    "$(printf '%s\n' "${WIDE}" | grep -c '^[0-9]')"

OTHER="$(go_errors "${ALPHA}" show --project beta)"
assert_eq "--project beta, from alpha: beta's fault plus the unattributed one" "2" \
    "$(printf '%s\n' "${OTHER}" | grep -c '^[0-9]')"
BETA_ID="$(sqlite3 "${CFG}/endless.db" "SELECT e.id FROM errors e JOIN projects p ON p.id=e.project_id WHERE p.name='beta'")"
ALPHA_ID="$(sqlite3 "${CFG}/endless.db" "SELECT e.id FROM errors e JOIN projects p ON p.id=e.project_id WHERE p.name='alpha'")"
# Compare the id COLUMN, not a substring of the frame: grep '^2' would match row
# 23 just as happily, and this suite must not pass on a coincidence.
IDS_SHOWN=" $(printf '%s\n' "${OTHER}" | awk '/^[0-9]/ {print $1}' | tr '\n' ' ')"
assert_contains "--project beta really returned beta's row" " ${BETA_ID} " "${IDS_SHOWN}"
assert_not_contains "and left alpha's out" " ${ALPHA_ID} " "${IDS_SHOWN}"

OUTSIDE="$(go_errors "${NOWHERE}" show)"
assert_contains "outside any project it falls back to machine-wide" "PROJECT" "${OUTSIDE}"
assert_eq "and lists everything" "3" "$(printf '%s\n' "${OUTSIDE}" | grep -c '^[0-9]')"

assert_contains "an unknown --project is refused, not silently widened" "no such project" \
    "$(go_errors "${ALPHA}" show --project nope)"
assert_contains "--project and --all-projects together are refused" "opposites" \
    "$(go_errors "${ALPHA}" show --project beta --all-projects)"

# ── C6: the detail log ────────────────────────────────────────────────────────
section "C6 — the JSONL detail line carries the project by name"

LOG="${CFG}/log/errors.jsonl"
[[ -f "${LOG}" ]] || setup_error "no detail log at ${LOG}"
assert_contains "alpha's occurrence names alpha" '"project":"alpha"' "$(cat "${LOG}")"
assert_contains "beta's occurrence names beta" '"project":"beta"' "$(cat "${LOG}")"
assert_not_contains "the unattributed occurrence omits the key rather than claiming a project" \
    '"project"' "$(grep "the machine's own failure" "${LOG}")"

DETAIL="$(go_errors "${ALPHA}" show --id "${BETA_ID}" --detail)"
assert_contains "show --id renders the project" "Project:     beta" "${DETAIL}"
assert_contains "show --id reaches another project's row (an id beats the scope)" \
    "shared condition" "${DETAIL}"

# ── C5: `errors clear` scoping ────────────────────────────────────────────────
section "C5 — errors clear dismisses exactly what errors show listed"

assert_contains "a no-id clear inside alpha takes 2 (alpha's plus the unattributed one)" \
    "cleared 2 error(s)" "$(go_errors "${ALPHA}" clear)"
assert_eq "beta's incident is untouched" "1" \
    "$(sqlite3 "${CFG}/endless.db" \
        "SELECT count(*) FROM errors e JOIN projects p ON p.id=e.project_id
          WHERE p.name='beta' AND e.cleared_at IS NULL")"
assert_contains "clearing beta's id from inside alpha still works" "cleared 1 error(s)" \
    "$(go_errors "${ALPHA}" clear "${BETA_ID}")"
assert_eq "nothing is left open" "0" \
    "$(sqlite3 "${CFG}/endless.db" "SELECT count(*) FROM errors WHERE cleared_at IS NULL")"
assert_eq "and nothing was deleted — clearing is still history, not removal" "3" \
    "$(sqlite3 "${CFG}/endless.db" "SELECT count(*) FROM errors")"

# ── C2: dedup of unattributed faults, through the real recording path ────────
section "C2 — unattributed faults still dedupe"

for _ in 1 2 3 4 5; do
    go_errors "${NOWHERE}" raise --severity warning --summary "an unattributed repeat" --source "jobs" >/dev/null
done
assert_eq "five unattributed occurrences are ONE incident with five occurrences" "1|5" \
    "$(sqlite3 "${CFG}/endless.db" \
        "SELECT count(*), max(occurrences) FROM errors
          WHERE summary='an unattributed repeat' AND cleared_at IS NULL" | tr -d ' ')"

# ── C8: unregistering a project keeps its errors ──────────────────────────────
section "C8 — unregistering a project does not delete its errors"

BEFORE="$(sqlite3 "${CFG}/endless.db" "SELECT count(*) FROM errors")"
sqlite3 "${CFG}/endless.db" "PRAGMA foreign_keys=ON; DELETE FROM projects WHERE name='beta'" \
    >/dev/null 2>&1 || setup_error "cannot delete the beta project row"
assert_eq "beta's incidents survive their project" "${BEFORE}" \
    "$(sqlite3 "${CFG}/endless.db" "SELECT count(*) FROM errors")"
assert_eq "and are now unattributed rather than dangling" "0" \
    "$(sqlite3 "${CFG}/endless.db" \
        "SELECT count(*) FROM errors e WHERE e.project_id IS NOT NULL
            AND e.project_id NOT IN (SELECT id FROM projects)")"

summary
