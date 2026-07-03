#!/usr/bin/env bash
# E-1719 verify: the record-only landing slice (nullable branch + historical --ts
# + explicit-value task.landed emit) and its schema migration, exercised fully
# isolated — a throwaway git repo for the ledger and a temp DB for the rows, so
# nothing touches the real endless DB or main's ledger.
#
# It drives the exact Go emit the Python `worktree land --record-only` wrapper
# invokes (endless-go event emit --kind task.landed --ts … --actor-kind system).
# The Python land command itself is deliberately pinned to main (default_db_to_main)
# and so cannot be run in isolation; its wiring is covered by the pytest
# tests/test_worktree_land_record_only.py.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GO="$ROOT/bin/endless-go"
[[ -x "$GO" ]] || { echo "FAIL: build first (just build): $GO missing"; exit 1; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
REPO="$TMP/repo"          # throwaway project root: holds the isolated ledger
CFG="$TMP/xdg"            # isolated XDG; Python DB at $CFG/endless/endless.db
GODIR="$CFG/endless"      # endless-go --config-dir points here (holds endless.db)
mkdir -p "$REPO/.endless/db-ledger" "$GODIR"

git -C "$REPO" init -q
# %cI (committer date) is what the record-only path reads for --at, so pin it to
# a fixed historical instant via GIT_COMMITTER_DATE.
GIT_COMMITTER_DATE="2026-05-09T18:30:00Z" \
    git -C "$REPO" -c user.email=t@t -c user.name=t \
    commit -q --allow-empty --date="2026-05-09T18:30:00Z" -m "probe landing commit"
SHA="$(git -C "$REPO" rev-parse HEAD)"
CDATE="$(git -C "$REPO" show -s --format=%cI "$SHA")"
echo "• repo=$REPO sha=${SHA:0:8} commit-date=$CDATE"

pass=0; fail=0
ok()   { pass=$((pass+1)); echo "  ✓ $1"; }
bad()  { fail=$((fail+1)); echo "  ✗ $1"; }

# Read-only query (TSV, no header) against the isolated Python DB.
q()  { ( cd "$REPO" && XDG_CONFIG_HOME="$CFG" endless sql "$1" --tsv 2>/dev/null ); }
# Write query against the isolated Python DB.
qw() { ( cd "$REPO" && XDG_CONFIG_HOME="$CFG" endless sql "$1" --write >/dev/null 2>&1 ); }

echo "== seed isolated DB (schema auto-applies on first connect) =="
qw "INSERT INTO projects (id,name,path,status,created_at,updated_at) VALUES (1,'probe','$REPO','active','2026-01-01T00:00:00','2026-01-01T00:00:00')"
qw "INSERT INTO tasks (id,project_id,title,phase,status,type_id) VALUES (1209,1,'probe task','now','underway',1)"
seeded="$(q "SELECT count(*) FROM tasks WHERE id=1209")"
[[ "$seeded" == "1" ]] && ok "project + task 1209 seeded" || bad "seed failed (got '$seeded')"

# The global `endless` CLI is an editable install from the MAIN checkout, so the
# DB it just created carries main's OLD schema (task_landings.branch NOT NULL) —
# exactly the real starting state Phase 2 runs against. Seed one old-shape row so
# the migration has data to preserve.
echo "== pre-migration: old schema rejects a NULL branch (as expected) =="
qw "INSERT INTO task_landings (task_id,branch,merge_commit_sha,landed_at) VALUES (1209,'task/1209-old','abc123','2026-05-09T00:00:00')"
if qw "INSERT INTO task_landings (task_id,branch,merge_commit_sha,landed_at) VALUES (1209,NULL,'zzz','2026-05-09T00:00:00')"; then
  bad "pre-migration NULL insert unexpectedly accepted (schema already nullable?)"
else
  ok "pre-migration: NULL branch rejected by NOT NULL constraint"
fi
before="$(q "SELECT count(*) FROM task_landings")"

echo "== apply the e-1719 migration (rebuild task_landings, drop NOT NULL) =="
( cd "$REPO" && "$GO" --config-dir "$GODIR" event apply-change \
    "$ROOT/internal/schema/changes/e-1719-nullable-task-landings-branch.sql" >/dev/null )
after="$(q "SELECT count(*) FROM task_landings")"
[[ "$before" == "$after" && "$after" == "1" ]] && ok "migration preserved existing rows ($after)" || bad "row count changed ($before -> $after)"

echo "== emit a record-only task.landed (empty branch, historical --ts) =="
( cd "$REPO" && "$GO" --config-dir "$GODIR" event emit \
    --kind task.landed --project probe --entity-type task --entity-id 1209 \
    --actor-kind system --actor-id backfill --node-id ba15 \
    --project-root "$REPO" --ts "$CDATE" \
    --payload "{\"branch\":\"\",\"merge_commit_sha\":\"$SHA\"}" >/dev/null )

# IS NULL predicates disambiguate NULL from empty-string in TSV output.
branch_null="$(q "SELECT branch IS NULL FROM task_landings WHERE merge_commit_sha='$SHA'")"
sess_null="$(q "SELECT session_id IS NULL FROM task_landings WHERE merge_commit_sha='$SHA'")"
got_at="$(q "SELECT landed_at FROM task_landings WHERE merge_commit_sha='$SHA'")"
echo "  branch_is_null=$branch_null session_is_null=$sess_null sha=${SHA:0:8} landed_at=$got_at"
[[ "$branch_null" == "1" ]]                  && ok "branch recorded NULL (constraint dropped, emit accepted NULL)" || bad "branch not NULL"
[[ "$sess_null" == "1" ]]                    && ok "session_id NULL (system actor, no session)" || bad "session_id not NULL"
[[ "$got_at" == "2026-05-09T18:30:00" ]]     && ok "landed_at = commit date, not now() (--ts honored end to end)" || bad "landed_at wrong: $got_at"

echo
echo "== result: $pass passed, $fail failed =="
[[ "$fail" == "0" ]]
