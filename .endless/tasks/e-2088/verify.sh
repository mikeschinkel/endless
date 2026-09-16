#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2088 and records what was true when E-2088
# landed. Edit it only if you ARE E-2088. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2088: a self_dev land migrates with a migration-only executable.
#
# WHAT LANDED
#   ED-1567 forbids a CANDIDATE binary — one built inside a task worktree, i.e.
#   unlanded code — from migrating the real ledger. That is the 2026-08-10
#   failure exactly: a candidate wrote a column the real database lacked and
#   session tracking went down machine-wide for thirty minutes.
#
#   But `_resolve_land_endless_go` hands a self_dev land the WORKTREE's own
#   endless-go, and E-1664 made that an invariant rather than a preference: it
#   is the only binary whose embedded schema and enums match the rows the land
#   just wrote. That binary is a candidate by definition. So the invariant and
#   the prohibition point opposite ways, and once connect-time version
#   verification lands, a self_dev land has NO binary permitted to apply its own
#   migration — the installed one does not carry it, the worktree one may not
#   run it.
#
#   ED-1571's answer is a third binary. `worktree land` now builds
#   <worktree>/bin/endless-migrate from the landing branch and applies the
#   branch's changes with that. It carries the migration set and no application
#   at all: no hook, no task command, no query, and no schema of its own to
#   apply or verify. Having no expectation of the database is the whole of its
#   safety — a tool that expects nothing cannot be broken by what it finds.
#
#   The applying logic itself did not fork. internal/schemachange is the one
#   definition of "apply one change file and record its marker", and the two
#   programs that apply changes differ only in HOW THEY OPENED THE DATABASE:
#   `endless-go event apply-change` through the application's connect,
#   endless-migrate through a direct file open. internal/dbcontext exists for
#   the same reason: the executable needed to know where the database lives, and
#   that answer lived inside internal/monitor, whose import would have put the
#   entire application back into the binary.
#
# THE CLAIMS (this suite checks these, not the plumbing)
#   C1  IT MIGRATES, IN FULL. The executable applies a change end to end against
#       a throwaway database: absent before, recorded after, and the DML the
#       change defines actually ran — the enum seed rows and the backfill, not
#       only the DDL. A DDL-only apply would leave a migrated database missing
#       the rows the fail-closed integrity gates read on the next connect.
#   C2  IT DOES NOTHING ELSE. Every application subcommand endless-go offers is
#       refused; the help text offers one command; and — the half no command line
#       can show — its BUILD links nothing but the migration machinery. One
#       import of internal/monitor would bring back the schema-applying connect
#       while leaving every command-line assertion green.
#   C3  IT RESOLVES ITS TARGET FROM WHAT IT WAS TOLD. No cwd-derived routing: run
#       from inside a worktree with a sandbox, it still migrates the database it
#       was pointed at. endless-go deliberately self-detects a sandbox from cwd;
#       a migration tool that did the same could rewrite a schema nobody named.
#   C4  THE LAND BUILDS IT BEFORE THE MERGE AND INVOKES IT AFTER. The build is at
#       Step 4.6, while base is untouched, so a tree that cannot compile its
#       migration tool aborts having merged and migrated nothing. The invoke
#       stays in E-1941's window, between the ff-merge and the record, and a
#       failure there still produces the post-merge message that says re-run
#       rather than restore.
#   C5  A LAND WITH NO MIGRATION BUILDS NOTHING. Nothing to apply, nothing to
#       build — and an unconditional build would make this untestable.
#   C6  SCOPE IS self_dev. A non-self_dev land builds nothing, resolves nothing
#       and applies nothing. Every other project has one installed binary, which
#       applies its own migrations through `endless-go event apply-change` — that
#       path is intact and this suite exercises it.
#   C7  THE PAIRING SURVIVES. The migration executable applies; the worktree's
#       endless-go records, against the ledger the executable just migrated. So
#       the application's connect — schema.sql plus all four integrity gates —
#       must accept that database, and must honour a marker the other program
#       wrote. This is the half of E-1664 that outlives E-2088 and nothing else
#       covers it.
#
# ISOLATION
#   Every database here is created under a scratch directory and reached by
#   pointing XDG_CONFIG_HOME at it — never `--config-dir`, which is the flag that
#   could name a real ledger. The runner's temp HOME and XDG_CONFIG_HOME are what
#   make running a real migration safe at all, and `_guard.sh` refuses a direct
#   run for that reason. The one endless-go invocation runs from the scratch
#   directory rather than the worktree, because endless-go DOES self-detect a
#   worktree sandbox from cwd and would otherwise ignore the scratch target
#   entirely — which is C3's point, read from the other side.
#
# Layers:
#   A. FAIL-FAST — the build and the unit suites of everything this touched.
#      Nothing below is meaningful if a package does not compile or its own
#      tests do not pass.
#   B. The executable migrates, in full — C1.
#   C. It has no other surface, on the command line and in the build — C2.
#   D. It resolves its target from its argument, not its location — C3.
#   E. The land's wiring: build before the merge, invoke after, failure
#      surfaced — C4, C5.
#   F. Scope, and the path every other project uses — C6.
#   G. Two binaries, one ledger — C7.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 on any
# failure, 2 on a setup problem.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

FIXTURES="${ENDLESS_VERIFY_DIR}/fixtures"
[[ -d "${FIXTURES}" ]] || setup_error "fixtures are missing: ${FIXTURES}"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

MIGRATE="${TMP}/endless-migrate"
GO_BIN="${WT}/bin/endless-go"

# ─── helpers ────────────────────────────────────────────────────────────────

# scratch_cfg <name> — set CFG to a fresh Endless config directory holding an
# empty database, under the scratch tree. A zero-byte file IS an empty SQLite
# database, which is what lets the migration executable be handed a real,
# existing, empty ledger without anything having to build a schema first.
#
# Sets a global instead of printing, because `setup_error` inside a command
# substitution would exit only the subshell and leave the caller running with an
# empty path — a setup failure that looks like a test failure two sections later.
CFG=""
scratch_cfg() {
    CFG="${TMP}/$1"
    mkdir -p "${CFG}/endless" || setup_error "cannot create ${CFG}/endless"
    : >"${CFG}/endless/endless.db" || setup_error "cannot create the database in ${CFG}"
}

# migrate <xdg-root> <change-file> — run the executable against the database
# under <xdg-root>, with NO --config-dir. The environment is what points it at a
# scratch database; the flag is the one that could name a real ledger, and this
# suite never passes it. Prints stdout; the exit code is the caller's to read.
migrate() {
    XDG_CONFIG_HOME="$1" "${MIGRATE}" apply "$2" 2>"${TMP}/migrate.stderr"
}

# query.py reads a scalar out of a database. Python's own sqlite3 rather than
# endless-go, because reading through endless-go would OPEN the database through
# the application's connect — which applies schema.sql and would create the very
# tables the "before" assertions are about to look for. A reader must not be able
# to change what it reads.
cat >"${TMP}/query.py" <<'PYEOF'
import sqlite3
import sys

con = sqlite3.connect(sys.argv[1])
try:
    row = con.execute(sys.argv[2]).fetchone()
    print("" if row is None else row[0])
finally:
    con.close()
PYEOF

# sql <xdg-root> <query> — the scalar, or the error text, so a broken query
# fails an assertion visibly instead of silently reading as empty.
sql() {
    local db="$1/endless/endless.db" out
    out=$(uv run python "${TMP}/query.py" "${db}" "$2" 2>&1)         || printf 'query failed: %s' "${out}"
    printf '%s' "${out}"
}

# ─── A. fail-fast ───────────────────────────────────────────────────────────

section "A. The tree builds and every touched package passes its own tests"

if out=$(go build ./... 2>&1); then
    report_pass "go build ./... — the new packages and the executable compile"
else
    report_fail "go build ./... — the new packages and the executable compile" \
        "exit 0" "$(printf '%s' "${out}" | tail -20)"
    summary
fi

if out=$(go test -count=1 \
        ./internal/dbcontext/ ./internal/schemachange/ \
        ./internal/monitor/ ./internal/eventcmd/ 2>&1); then
    report_pass "go test: dbcontext, schemachange, monitor, eventcmd"
else
    report_fail "go test: dbcontext, schemachange, monitor, eventcmd" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

if out=$(uv run pytest -q \
        tests/test_worktree_land_migrate_exec.py \
        tests/test_worktree_land_schema_apply.py 2>&1); then
    report_pass "pytest: the land's migrate-executable and schema-apply modules"
else
    report_fail "pytest: the land's migrate-executable and schema-apply modules" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# The land builds this binary by recipe rather than by an inlined `go build`, so
# the build command has one definition and the land runs the one a developer
# would. Assert the recipe, then use the same command to build what follows.
if out=$(just --show migrate-bin 2>&1); then
    assert_contains "the land's build has one definition: \`just migrate-bin\`" \
        "go build -o bin/endless-migrate ./cmd/endless-migrate" "${out}"
else
    report_fail "the land's build has one definition: \`just migrate-bin\`" \
        "a migrate-bin recipe" "$(printf '%s' "${out}" | tail -5)"
    summary
fi

if out=$(go build -o "${MIGRATE}" ./cmd/endless-migrate 2>&1); then
    report_pass "the migration executable builds from this tree"
else
    report_fail "the migration executable builds from this tree" \
        "exit 0" "$(printf '%s' "${out}" | tail -20)"
    summary
fi

[[ -x "${GO_BIN}" ]] || setup_error \
    "the worktree's endless-go is missing (${GO_BIN}); run \`just go\` first"

# ─── B. it migrates, in full (C1) ───────────────────────────────────────────

section "B. It applies a change end to end — DDL, seed DML and backfill (C1)"

scratch_cfg full
CFG_FULL="${CFG}"

# Before: the change is not recorded, and nothing it creates exists.
before="$(sql "${CFG_FULL}" \
    "SELECT count(*) FROM sqlite_master WHERE name='widgets'")"
assert_eq "before: the change's tables do not exist" "0" "${before}"

out="$(migrate "${CFG_FULL}" "${FIXTURES}/e-102-widgets.sql")"
rc=$?
if (( rc != 0 )); then
    report_fail "the executable applies the change" "exit 0" \
        "exit ${rc}: ${out} $(cat "${TMP}/migrate.stderr")"
    summary
fi

assert_contains "it reports the change applied" '"status":"applied"' "${out}"
assert_contains "it names the change it applied" '"name":"e-102-widgets"' "${out}"
assert_contains "it names the database it changed" \
    "${CFG_FULL}/endless/endless.db" "${out}"

# After: the marker is recorded — the version moved.
assert_eq "after: the change is recorded in _schema_version" "1" \
    "$(sql "${CFG_FULL}" \
        "SELECT count(*) FROM _schema_version WHERE name='e-102-widgets'")"

# The DDL ran.
assert_eq "the DDL ran: both tables exist" "2" \
    "$(sql "${CFG_FULL}" \
        "SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('widgets','widget_kinds')")"

# The SEED DML ran. This is the assertion that separates "applies migrations"
# from "applies DDL": without these rows a migrated database fails the
# integrity gates on the next connect.
assert_eq "the seed DML ran: the enum-mirror rows are present" "2" \
    "$(sql "${CFG_FULL}" "SELECT count(*) FROM widget_kinds")"

# And the BACKFILL ran.
assert_eq "the backfill ran: no row was left unassigned" "0" \
    "$(sql "${CFG_FULL}" "SELECT count(*) FROM widgets WHERE kind_id IS NULL")"

# Re-running is a no-op that says so, which is what makes a failed land
# re-runnable rather than a guess.
out="$(migrate "${CFG_FULL}" "${FIXTURES}/e-102-widgets.sql")"
assert_contains "re-applying skips rather than repeating" '"status":"skipped"' "${out}"
assert_eq "re-applying did not duplicate the marker" "1" \
    "$(sql "${CFG_FULL}" \
        "SELECT count(*) FROM _schema_version WHERE name='e-102-widgets'")"

# A half-valid change leaves nothing: neither effects nor marker.
scratch_cfg broken
CFG_BROKEN="${CFG}"
if migrate "${CFG_BROKEN}" "${FIXTURES}/e-103-broken.sql" >"${TMP}/broken.out" 2>&1; then
    report_fail "a failing change is reported as a failure" "non-zero exit" "exit 0"
else
    report_pass "a failing change is reported as a failure"
fi
assert_eq "a failing change leaves no DDL behind" "0" \
    "$(sql "${CFG_BROKEN}" \
        "SELECT count(*) FROM sqlite_master WHERE name='half_applied'")"
assert_eq "a failing change records no marker" "0" \
    "$(sql "${CFG_BROKEN}" \
        "SELECT count(*) FROM _schema_version WHERE name='e-103-broken'")"

# A database that does not exist is refused rather than created. sql.Open would
# have made one, and a migration that creates its target has migrated nothing.
mkdir -p "${TMP}/absent/endless"
if migrate "${TMP}/absent" "${FIXTURES}/e-102-widgets.sql" >"${TMP}/absent.out" 2>&1; then
    report_fail "a missing database is refused, not created" "non-zero exit" "exit 0"
else
    assert_contains "a missing database is refused, not created" \
        "no database at" "$(cat "${TMP}/absent.out")"
fi
if [[ -e "${TMP}/absent/endless/endless.db" ]]; then
    report_fail "the refusal did not create the database" "no file" "a database was created"
else
    report_pass "the refusal did not create the database"
fi

# ─── C. it does nothing else (C2) ───────────────────────────────────────────

section "C. It has no application surface — on the command line, and in the build (C2)"

for sub in hook event task session session-query session-state tmux worktree \
           project db verify jobs spawn; do
    if out=$(XDG_CONFIG_HOME="${TMP}/nowhere" "${MIGRATE}" "${sub}" 2>&1); then
        report_fail "\`${sub}\` does not exist on this binary" "non-zero exit" "exit 0"
        continue
    fi
    assert_contains "\`${sub}\` does not exist on this binary" "unknown command" "${out}"
done

help="$("${MIGRATE}" --help 2>&1)"
assert_contains "the help text offers apply" "apply <change-file>" "${help}"

# The half no command line can show. `go list -deps` is the build, and the
# allowlist is a statement about what the binary IS: one import of
# internal/monitor would return the schema-applying connect to it and leave
# every assertion above green.
deps="$(go list -deps ./cmd/endless-migrate 2>/dev/null \
    | grep '^github.com/mikeschinkel/endless/' | sort)"
assert_eq "the build links only the migration machinery" \
"github.com/mikeschinkel/endless/cmd/endless-migrate
github.com/mikeschinkel/endless/internal/dbcontext
github.com/mikeschinkel/endless/internal/schemachange" \
    "${deps}"

# ─── D. it resolves its target from what it was told (C3) ───────────────────

section "D. No cwd-derived routing: it migrates what it was pointed at (C3)"

# Run from inside the worktree — the location endless-go reads in order to
# self-detect a sandbox and silently retarget. The executable must ignore where
# it is standing and migrate the database its environment named.
scratch_cfg cwd
CFG_CWD="${CFG}"
out="$(cd "${WT}" && migrate "${CFG_CWD}" "${FIXTURES}/e-101-bootstrap.sql")"
assert_contains "run from inside a worktree, it migrates the named database" \
    "${CFG_CWD}/endless/endless.db" "${out}"
assert_eq "and the change landed there" "1" \
    "$(sql "${CFG_CWD}" \
        "SELECT count(*) FROM _schema_version WHERE name='e-101-bootstrap'")"

# ─── E. the land's wiring (C4, C5) ──────────────────────────────────────────

section "E. The land builds it before the merge and invokes it after (C4, C5)"

run_pytest_claim() {
    local label="$1" node="$2"
    local out
    if out=$(uv run pytest -q "${node}" 2>&1); then
        report_pass "${label}"
    else
        report_fail "${label}" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    fi
}

M="tests/test_worktree_land_migrate_exec.py"
S="tests/test_worktree_land_schema_apply.py"

run_pytest_claim "the build runs \`just migrate-bin\` in the worktree" \
    "${M}::test_build_runs_just_migrate_bin_in_the_worktree"
run_pytest_claim "a broken build aborts with base and the database untouched" \
    "${M}::test_build_failure_aborts_before_anything_advances"
run_pytest_claim "built while base is behind, invoked once base has advanced" \
    "${M}::test_land_builds_and_invokes_it_around_the_ff_merge"
run_pytest_claim "the whole land's order is rebuild, build-migrate, backup, apply, record" \
    "${S}::test_apply_runs_after_merge_and_before_record"
run_pytest_claim "an apply failure says main advanced and needs no restore" \
    "${S}::test_apply_failure_reports_main_advanced_and_keeps_the_merge"
run_pytest_claim "a missing executable fails loudly instead of substituting one" \
    "${M}::test_missing_migrate_binary_fails_loudly"
run_pytest_claim "the change path and config dir reach it as arguments, never as an export" \
    "${M}::test_invocation_threads_config_dir_and_the_change_path"
run_pytest_claim "a land carrying no schema change builds nothing and invokes nothing" \
    "${M}::test_a_land_with_no_schema_change_builds_nothing"

# ─── F. scope, and the path every other project uses (C6) ───────────────────

section "F. Scope is self_dev; every other project applies its own (C6)"

run_pytest_claim "a non-self_dev land builds nothing and applies nothing" \
    "${M}::test_a_non_self_dev_land_builds_nothing_and_applies_nothing"
run_pytest_claim "no migration executable is resolved outside self_dev" \
    "${M}::test_non_self_dev_resolves_no_migrate_binary"

# The installed-binary path is not merely left in place, it still works. This
# runs `endless-go event apply-change` from the scratch directory rather than the
# worktree, because endless-go self-detects a worktree sandbox from cwd — the
# behaviour the executable deliberately does not have.
scratch_cfg pair
CFG_PAIR="${CFG}"
if out=$(cd "${TMP}" && XDG_CONFIG_HOME="${CFG_PAIR}" \
        "${GO_BIN}" event apply-change "${FIXTURES}/e-101-bootstrap.sql" 2>&1); then
    assert_contains "the installed-binary path still applies a change itself" \
        '"status":"applied"' "${out}"
else
    report_fail "the installed-binary path still applies a change itself" \
        "exit 0" "$(printf '%s' "${out}" | tail -20)"
fi

# ─── G. two binaries, one ledger (C7) ───────────────────────────────────────

section "G. The executable migrates; endless-go records against what it migrated (C7)"

# The database above was built by endless-go's own connect, so it carries the
# real schema and its seed rows. The executable takes its turn on the same file.
out="$(migrate "${CFG_PAIR}" "${FIXTURES}/e-102-widgets.sql")"
rc=$?
if (( rc != 0 )); then
    report_fail "the executable migrates a real ledger-shaped database" "exit 0" \
        "exit ${rc}: ${out} $(cat "${TMP}/migrate.stderr")"
    summary
fi
assert_contains "the executable migrates a real ledger-shaped database" \
    '"status":"applied"' "${out}"

# Now the application's connect — schema.sql plus all four fail-closed integrity
# gates — must accept that database. This is the pairing: whatever the executable
# did, the binary that records the landing has to be able to open what it left.
if out=$(cd "${TMP}" && XDG_CONFIG_HOME="${CFG_PAIR}" \
        "${GO_BIN}" event apply-change "${FIXTURES}/e-102-widgets.sql" 2>&1); then
    report_pass "endless-go's connect accepts the ledger the executable migrated"
else
    report_fail "endless-go's connect accepts the ledger the executable migrated" \
        "exit 0" "$(printf '%s' "${out}" | tail -20)"
fi

# And it honours the marker the OTHER program wrote — the two agree about what
# has been applied, which is what makes a re-run safe across both.
assert_contains "endless-go honours the marker the executable wrote" \
    '"status":"skipped"' "${out}"

summary
