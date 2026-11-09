#!/usr/bin/env bash
# E-2397 end to end: a real `endless-go event recover` against a database one
# schema version ahead, with a process holding the file so recover has to wait
# under the maintenance lock. While it waits, every Go opener is run against the
# same database and must refuse quietly without opening or creating anything,
# and the database path must never be missing or empty. Then the recovery must
# finish, land the LINKED pre-migration backup (not the newer one), and clear
# the lock.
#
# Prints "ok: <what>" lines; exits non-zero on the first failure.
set -euo pipefail

T=$(mktemp -d)
trap 'kill "${HOLDER:-}" "${RECOVER:-}" 2>/dev/null || true; rm -rf "$T"' EXIT
go build -o "$T/endless-go" ./cmd/endless-go
EG="$T/endless-go"
D="$T/cfg"
DB="$D/endless.db"
mkdir -p "$D/backups"
fail() { echo "FAIL: $*" >&2; exit 1; }

"$EG" --db-dir "$D" event migrate >/dev/null
latest=$(python3 -c 'import sqlite3,sys; print(sqlite3.connect(sys.argv[1]).execute("select max(version_id) from goose_db_version").fetchone()[0])' "$DB")

python3 - "$DB" "$latest" <<'PY'
import json, sqlite3, sys, time
db, latest = sys.argv[1], int(sys.argv[2])
backups = db.rsplit("/", 1)[0] + "/backups"
c = sqlite3.connect(db)
c.execute("VACUUM INTO ?", (backups + "/endless-20261018-051458.db",))
open(backups + "/pre-migration.jsonl", "w").write(json.dumps({
    "at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
    "backup": "endless-20261018-051458.db", "from": latest, "to": latest + 1}) + "\n")
c.execute("INSERT INTO goose_db_version (version_id, is_applied) VALUES (?, 1)", (latest + 1,))
c.commit()
c.execute("VACUUM INTO ?", (backups + "/endless-20261018-060000.db",))
c.close()
PY
echo "ok: database at v$((latest + 1)), endless-go carries v$latest"

# A holder recover must wait out: it keeps the file open for 4 seconds.
sh -c 'exec 3<"$0"; sleep 4' "$DB" &
HOLDER=$!
sleep 0.3

"$EG" --db-dir "$D" event recover --wait 15s >"$T/recover.json" 2>"$T/recover.err" &
RECOVER=$!

for _ in $(seq 1 50); do
  [ -e "$DB.maintenance" ] && break
  sleep 0.1
done
[ -e "$DB.maintenance" ] || fail "recover never took the maintenance lock"
echo "ok: recover holds the maintenance lock while a handle is open"

mv "$DB" "$T/parked-by-test.db"   # an empty path is the 2026-10-18 window
out=$("$EG" --db-dir "$D" event migrate 2>&1) && fail "event migrate opened the database under the lock: $out"
case "$out" in *"under maintenance"*) ;; *) fail "event migrate: $out" ;; esac
echo "ok: a command refuses under the lock"
out=$("$EG" --db-dir "$D" tmux status-line --pane %999 2>&1) || fail "status-line exited non-zero"
case "$out" in *maintenance*|*rror*) fail "status-line printed: $out" ;; esac
echo "ok: the tmux status line stays quiet"
out=$(echo '{}' | "$EG" --db-dir "$D" hook claude PreToolUse 2>&1) || fail "hook exited non-zero"
[ -z "$out" ] || fail "hook printed: $out"
echo "ok: the hook stays silent"
"$EG" --db-dir "$D" jobs run >/dev/null 2>&1 || fail "jobs run failed under the lock"
echo "ok: the job runner stays quiet"
out=$("$EG" --db-dir "$D" event backup 2>&1) && fail "event backup ran under the lock: $out"
case "$out" in *"under maintenance"*) ;; *) fail "event backup: $out" ;; esac
echo "ok: a backup refuses under the lock"
[ ! -e "$DB" ] || fail "an opener created $DB under the lock"
echo "ok: no opener created a database"
mv "$T/parked-by-test.db" "$DB"

empty=0
while kill -0 "$RECOVER" 2>/dev/null; do
  if [ ! -s "$DB" ]; then empty=1; fi
done
[ "$empty" = 0 ] || fail "the database path was missing or empty during the recovery"
wait "$RECOVER" || fail "recover failed: $(cat "$T/recover.err")"
echo "ok: the database path was never empty during the swap"

python3 - "$T/recover.json" "$DB" "$latest" <<'PY'
import json, sqlite3, sys
plan = json.load(open(sys.argv[1]))
assert plan["status"] == "recovered", plan
assert plan["backup"].endswith("endless-20261018-051458.db"), plan["backup"]
v = sqlite3.connect(f"file:{sys.argv[2]}?mode=ro", uri=True).execute(
    "select max(version_id) from goose_db_version").fetchone()[0]
assert v == int(sys.argv[3]), v
PY
echo "ok: recovered to v$latest from the linked pre-migration backup"
[ ! -e "$DB.maintenance" ] || fail "the lock survived the recovery"
echo "ok: the lock is clear"
