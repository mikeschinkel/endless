#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2157 and records what was true when E-2157
# landed. Edit it only if you ARE E-2157. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2157: cmd/endless-migrate speaks endless-go's --db vocabulary, and one
# package owns that vocabulary for both binaries.
#
# WHAT LANDED
#   E-1668 gave endless-go `--db main|sandbox` with a `--db-dir` escape and
#   retired `--config-dir` from it. ED-1571's cmd/endless-migrate kept
#   `--config-dir`. One product, one concept, two vocabularies — and the split
#   had already cost a near-miss: `worktree land` threaded endless-go's
#   spelling at endless-migrate, where `--db` survives the strip and lands in
#   argv[1] where the subcommand belongs. The binary's own test had stubbed the
#   wrong helper, so the suite stayed green while every real land would break.
#
#   The scope grew on the owner's call. The task row asked for the flag rename;
#   the duplication under it turned out to be six hand-rolled copies of
#   "$HOME/.config/endless" across internal/monitor and internal/sandboxcmd,
#   plus two parsers for one vocabulary. Both collapsed into internal/dbcontext.
#
# THE CLAIMS (this suite checks these, not the plumbing)
#   C1  ONE PARSER. internal/dbcontext parses --db / --db-dir for both
#       binaries. monitor.ConsumeDBFlags is a wrapper over it, not a second
#       implementation, and cmd/endless-migrate links the same parse.
#   C2  ONE RESOLVER. `$HOME/.config/endless` is spelled once, in
#       internal/dbcontext. No hand-rolled join survives in internal/ or cmd/.
#   C3  MAIN IS NOT DEFAULT. `--db main` follows $HOME and IGNORES
#       XDG_CONFIG_HOME — the injection endless still uses to route a child at
#       a worktree's sandbox. A resolver that honoured it would migrate a
#       sandbox during a land and report success. The two resolvers must
#       DISAGREE whenever XDG is set.
#   C4  SANDBOX IS REFUSED BY NAME. endless-migrate has no cwd routing
#       (ED-1571), so it cannot answer "which sandbox". It says so, and names
#       --db-dir and --db main as the remedies, rather than failing as an
#       unknown command.
#   C5  THE RETIRED FLAG NAMES ITS REPLACEMENT. `--config-dir` is refused with
#       "--db-dir" in the message: a stale invocation is muscle memory, not a
#       typo.
#   C6  THE MERGED PARSER IS STRICTER, BOTH WAYS. dbcontext's old parser
#       dropped a trailing bare flag and let a repeated flag win last-one-wins,
#       both silently. endless-go's refused. Merging gave endless-migrate the
#       refusals, and left endless-go's unchanged.
#   C7  THE LAND THREADS SOMETHING THE BINARY ACCEPTS, ALWAYS. Python's two
#       helpers now differ by one rewrite — `--db sandbox` becomes
#       `--db-dir <path>`, because this layer owns the cwd routing ED-1571
#       denies the executable. Every other value threads through unchanged.
#
# ISOLATION
#   Nothing here touches the main database or the real ledger. Every check is a
#   package test, a source-tree grep, or the migration binary run against a
#   throwaway HOME and a SQLite file under the runner's temp dir.
#
# Layers:
#   A. FAIL-FAST — this task's own unit tests, Go and Python. Nothing below is
#      meaningful if the behaviour they pin is broken.
#   B. The consolidation, in the source tree — C1, C2.
#   C. The live binary's flag surface — C3, C4, C5, C6.
#   D. What Python threads at it — C7.
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

# ─────────────────────────────────────────────────────────────────────
section "A. Fail-fast: this task's own unit tests"
# ─────────────────────────────────────────────────────────────────────
#
# First, and fatal. Everything below reads the source tree or runs the binary;
# if the packages these flags live in do not pass their own tests, a green
# grep downstream would be describing broken code.

if ! go_out="$(go test ./internal/dbcontext/ ./internal/monitor/ ./internal/schemachange/ 2>&1)"; then
    printf '%s\n' "${go_out}" >&2
    setup_error "the Go packages owning the flag vocabulary do not pass their own tests"
fi
report_pass "go test ./internal/{dbcontext,monitor,schemachange} passes"

if ! py_out="$(uv run pytest tests/test_provenance.py tests/test_worktree_land_migrate_exec.py -q 2>&1)"; then
    printf '%s\n' "${py_out}" >&2
    setup_error "the Python tests for the two helpers do not pass"
fi
report_pass "pytest test_provenance + test_worktree_land_migrate_exec passes"

# ─────────────────────────────────────────────────────────────────────
section "B. One parser, one resolver (C1, C2)"
# ─────────────────────────────────────────────────────────────────────

# C1: monitor must WRAP the shared parse rather than re-implement it. The
# tell is structural: it calls dbcontext.ConsumeFlags, and it no longer
# carries its own scan for the flag literals.
assert_contains "monitor.ConsumeDBFlags delegates to the shared parser" \
    "dbcontext.ConsumeFlags(os.Args)" \
    "$(sed -n '/^func ConsumeDBFlags/,/^}/p' internal/monitor/db.go)"

assert_not_contains "monitor no longer scans for the flag literals itself" \
    'a == "--db-dir"' \
    "$(cat internal/monitor/db.go)"

assert_contains "cmd/endless-migrate parses through the same package" \
    "dbcontext.ConsumeFlags(os.Args)" \
    "$(cat cmd/endless-migrate/main.go)"

# C2: the six copies. A hand-rolled join onto ".config" anywhere but the one
# resolver is a seventh copy waiting to drift — that is precisely how this
# started, with realDBPath's own comment admitting it "hardcodes the same
# location PinMainDB does".
copies="$(grep -rn '"\.config"' internal cmd --include='*.go' \
    | grep -v '_test\.go' \
    | grep -v '^internal/dbcontext/dbcontext\.go:' \
    | grep -v '"\.cache"' || true)"
assert_eq "no hand-rolled ~/.config/endless join survives outside internal/dbcontext" \
    "" "${copies}"

assert_contains "internal/dbcontext spells the config root once" \
    "configRootName" \
    "$(cat internal/dbcontext/dbcontext.go)"

# ─────────────────────────────────────────────────────────────────────
section "C. The binary's flag surface (C3, C4, C5, C6)"
# ─────────────────────────────────────────────────────────────────────

BIN="${WT}/bin/endless-migrate"
if ! go build -o "${BIN}" ./cmd/endless-migrate; then
    setup_error "cannot build cmd/endless-migrate"
fi

# A throwaway HOME with a real database inside it, and a DECOY database exactly
# where an injected XDG_CONFIG_HOME would send a resolver that honoured one.
# $HOME here is already the runner's temp home; this nests below it so the
# assertion does not depend on which.
FAKE_HOME="${HOME}/e2157-main"
FAKE_XDG="${HOME}/e2157-sandbox"
MAIN_DB="${FAKE_HOME}/.config/endless/endless.db"
DECOY_DB="${FAKE_XDG}/endless/endless.db"
mkdir -p "$(dirname "${MAIN_DB}")" "$(dirname "${DECOY_DB}")" \
    || setup_error "cannot create the throwaway config dirs"

if ! command -v sqlite3 >/dev/null 2>&1; then
    setup_error "sqlite3 is needed to create the throwaway databases"
fi
sqlite3 "${MAIN_DB}" "CREATE TABLE seed (id INTEGER);" \
    || setup_error "cannot create ${MAIN_DB}"
sqlite3 "${DECOY_DB}" "CREATE TABLE seed (id INTEGER);" \
    || setup_error "cannot create ${DECOY_DB}"

CHANGE="${HOME}/e-2157-verify-change.sql"
printf 'CREATE TABLE e2157_marker (id INTEGER);\n' > "${CHANGE}"

# C3: --db main follows $HOME and ignores XDG_CONFIG_HOME. Both are set, to
# different directories, each holding a database. Exactly one must move.
main_out="$(HOME="${FAKE_HOME}" XDG_CONFIG_HOME="${FAKE_XDG}" \
    "${BIN}" --db main apply "${CHANGE}" 2>&1)"
assert_contains "--db main opens the database under HOME" \
    "${MAIN_DB}" "${main_out}"
assert_not_contains "--db main does not resolve through XDG_CONFIG_HOME" \
    "${DECOY_DB}" "${main_out}"

decoy_tables="$(sqlite3 "${DECOY_DB}" \
    "SELECT name FROM sqlite_master WHERE name='e2157_marker';")"
assert_eq "the XDG-routed database was left untouched" "" "${decoy_tables}"

main_tables="$(sqlite3 "${MAIN_DB}" \
    "SELECT name FROM sqlite_master WHERE name='e2157_marker';")"
assert_eq "the change applied to the database under HOME" \
    "e2157_marker" "${main_tables}"

# C4: --db sandbox is refused, and the refusal names what to type instead.
sandbox_out="$(HOME="${FAKE_HOME}" "${BIN}" --db sandbox apply "${CHANGE}" 2>&1)"
sandbox_rc=$?
assert_eq "--db sandbox exits 2 (a usage refusal, not a migration failure)" \
    "2" "${sandbox_rc}"
assert_contains "the refusal says why sandbox cannot be answered here" \
    "sandbox" "${sandbox_out}"
assert_contains "the refusal names --db-dir as the remedy" \
    "--db-dir" "${sandbox_out}"
assert_contains "the refusal also names --db main" \
    "--db main" "${sandbox_out}"

# C5: the retired spelling names its replacement.
retired_out="$(HOME="${FAKE_HOME}" "${BIN}" --config-dir /tmp/x apply "${CHANGE}" 2>&1 || true)"
assert_contains "--config-dir is refused naming --db-dir" \
    "--db-dir" "${retired_out}"

# C6: the strictness endless-migrate INHERITED from the merge. Each of these
# was silently tolerated by dbcontext's own parser before it.
bare_db="$(HOME="${FAKE_HOME}" "${BIN}" apply --db 2>&1 || true)"
assert_contains "a trailing bare --db is refused, not dropped" \
    "requires a value" "${bare_db}"

bare_dir="$(HOME="${FAKE_HOME}" "${BIN}" apply --db-dir 2>&1 || true)"
assert_contains "a trailing bare --db-dir is refused, not dropped" \
    "requires a directory" "${bare_dir}"

conflict="$(HOME="${FAKE_HOME}" "${BIN}" --db main --db-dir /tmp/x apply "${CHANGE}" 2>&1 || true)"
assert_contains "--db and --db-dir together are refused as one choice twice" \
    "two spellings of one choice" "${conflict}"

# --db-dir still works: it is the escape its own tests depend on, and the
# value Python threads for a sandbox context.
DIR_DB="${HOME}/e2157-dir/endless.db"
mkdir -p "$(dirname "${DIR_DB}")" || setup_error "cannot create the --db-dir target"
sqlite3 "${DIR_DB}" "CREATE TABLE seed (id INTEGER);" \
    || setup_error "cannot create ${DIR_DB}"
DIR_CHANGE="${HOME}/e-2157-verify-dir.sql"
printf 'CREATE TABLE e2157_dir_marker (id INTEGER);\n' > "${DIR_CHANGE}"
dir_out="$(HOME="${FAKE_HOME}" "${BIN}" --db-dir "$(dirname "${DIR_DB}")" \
    apply "${DIR_CHANGE}" 2>&1)"
assert_contains "--db-dir still names a directory outright" \
    '"status":"applied"' "${dir_out}"

# And endless-go's own vocabulary is unchanged by the merge — the half of C6
# that says this took nothing away from the binary that already had it.
GO_BIN="${WT}/bin/endless-go"
if [[ -x "${GO_BIN}" ]]; then
    go_unknown="$("${GO_BIN}" --db worktree session-query list-live 2>&1 || true)"
    assert_contains "endless-go still refuses a word outside the vocabulary" \
        "expected" "${go_unknown}"
else
    report_skip "endless-go still refuses a word outside the vocabulary" \
        "bin/endless-go not built"
fi

# ─────────────────────────────────────────────────────────────────────
section "D. What the land threads at it (C7)"
# ─────────────────────────────────────────────────────────────────────
#
# The near-miss this task exists to close was a Python helper emitting a word
# the binary could not parse. These assert the helpers' agreement directly,
# through the real functions rather than a stub.

helper_out="$(uv run python - <<'PY' 2>&1
from pathlib import Path
from endless import config

config.RESOLVED_CONFIG_DIR = config.main_config_dir()
print("main:", config.go_db_context_args(), config.migrate_db_context_args())

elsewhere = Path("/somewhere/else/endless")
config.RESOLVED_CONFIG_DIR = elsewhere
print("dir:", config.go_db_context_args(), config.migrate_db_context_args())
PY
)"

assert_contains "for main, both helpers emit the same --db main" \
    "main: ['--db', 'main'] ['--db', 'main']" "${helper_out}"

assert_contains "for a named directory, both helpers emit the same --db-dir" \
    "dir: ['--db-dir', '/somewhere/else/endless'] ['--db-dir', '/somewhere/else/endless']" \
    "${helper_out}"

# The one deliberate divergence, stated as such: Python resolves the sandbox
# because ED-1571 denies the executable the cwd routing to resolve it itself.
assert_contains "the migrate helper rewrites sandbox rather than passing the word" \
    '["--db-dir", str(RESOLVED_CONFIG_DIR)]' \
    "$(sed -n '/^def migrate_db_context_args/,/^def /p' src/endless/config.py)"

summary
