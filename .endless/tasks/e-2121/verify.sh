#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2121 and records what was true when E-2121
# landed. Edit it only if you ARE E-2121. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2121 verification — database backups run on a SCHEDULE with tiered
# retention, not only before a schema migration.
#
# Before: `monitor.BackupDB` had two callers, the pre-apply step of a land and
# an explicit `endless db backup`. Neither is a schedule, so the newest backup
# was as old as the last schema change — nine days, on the day one was needed.
# Rotation kept the last SIXTY files by list position, which at any real cadence
# is a fortnight of history and, on a burst of migrations, evicts the older
# copies first.
#
# After: `internal/backupjob` registers `db-backup` with the fire-once runner at
# an hourly cadence, and retention is by AGE — hourly for 24 hours, daily for 30
# days, weekly for a year — evaluated per calendar bucket rather than per list
# position, never emptying the shelf and never touching a file that is not one
# of BackupDB's own.
#
# Everything below section 1 drives the real binary as a subprocess against a
# real database in a temp config dir. Nothing is stubbed: the backups are files
# on disk, the sweep is the shipped policy, and the schedule is read out of
# `jobs list`.
#
#   endless task verify E-2121
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
trap 'rm -rf "${TMP}"' EXIT

# ── 1. unit gate (fail fast) ────────────────────────────────────────────────
# The durable coverage lives in the project's own suites, per
# .endless/tasks/CLAUDE.md: internal/monitor/backup_retention_test.go owns the
# policy (tier boundaries, per-bucket survivors, the steady-state census, the
# never-empty rule, foreign files), internal/backupjob owns the schedule and the
# registration, internal/eventcmd drives `event backup` end to end, and
# tests/test_db_backup_message.py owns the CLI's retention warning. A failure
# there makes every drive below meaningless, so the suite stops rather than
# reporting a cascade.
section "1. Unit gate (fail fast)"

if go test ./internal/monitor/ ./internal/backupjob/ ./internal/eventcmd/ \
        >"${TMP}/go.log" 2>&1; then
    report_pass "go test — retention policy, job wiring, event backup"
else
    report_fail "go test ./internal/{monitor,backupjob,eventcmd}" "exit 0" \
        "$(tail -25 "${TMP}/go.log")"
    summary
fi

if uv run pytest -q tests/test_db_backup_message.py >"${TMP}/py.log" 2>&1; then
    report_pass "pytest — the CLI's backup message and retention warning"
else
    report_fail "pytest tests/test_db_backup_message.py" "exit 0" \
        "$(tail -25 "${TMP}/py.log")"
    summary
fi

# Built from THIS worktree rather than taken off PATH: every section below
# drives the runner and the retention sweep, and an installed binary would prove
# something about a different tree.
mkdir -p "${TMP}/bin"
if go build -o "${TMP}/bin/endless-go" ./cmd/endless-go >"${TMP}/build.log" 2>&1; then
    report_pass "go build ./cmd/endless-go (the binary the sections below drive)"
else
    report_fail "go build ./cmd/endless-go" "exit 0" "$(tail -25 "${TMP}/build.log")"
    summary
fi

BIN="${TMP}/bin/endless-go"

# go — one endless-go invocation against a fixture config dir. --config-dir is
# the explicit DB context (E-1429), so nothing here can reach the real database.
CFG="${TMP}/cfg"
mkdir -p "${CFG}"
go_() { "${BIN}" --config-dir "${CFG}" "$@" 2>&1; }

BACKUPS="${CFG}/backups"

# seed <days-ago> <hour> — an empty file wearing a backup's name, stamped that
# many days back at that hour. Retention reads the timestamp out of the NAME, so
# the contents are irrelevant and a year of history costs nothing to build.
seed() {
    local days="$1" hour="$2" stamp
    stamp="$(date -v-"${days}"d "+%Y%m%d-${hour}0000" 2>/dev/null)" \
        || stamp="$(date -d "${days} days ago" "+%Y%m%d-${hour}0000")"
    : >"${BACKUPS}/endless-${stamp}.db"
    printf '%s' "endless-${stamp}.db"
}

count_backups() { find "${BACKUPS}" -name 'endless-*.db' -type f | wc -l | tr -d ' '; }
oldest_backup() { find "${BACKUPS}" -name 'endless-*.db' -type f -exec basename {} \; | sort | head -1; }

# ── 2. the schedule exists, and it is a cadence ─────────────────────────────
# The heart of the report: nothing CAUSED a backup. A throttle can only prevent
# one. `db-backup` in the registry with an hourly interval is the fix.
section "2. The backup runs on a schedule"

out="$(go_ jobs list)"
assert_contains "db-backup is registered with the fire-once runner" "db-backup" "${out}"
assert_contains "its cadence is hourly, matching the finest retention tier" \
    "db-backup           1h0m0s" "${out}"

out="$(go_ jobs run --job db-backup)"
assert_contains "the runner fires it and it succeeds" "db-backup: ok" "${out}"
assert_eq "firing the job put a backup on disk" "1" "$(count_backups)"

out="$(go_ jobs list)"
assert_contains "the run is recorded against its scheduling row" "db-backup" "${out}"
assert_not_contains "and it did not fail" "backoff" "${out}"

# ── 3. retention is by age, not by count ────────────────────────────────────
# A year of hourly backups is the case the count rotation got wrong: it kept the
# newest 60 files, which is two and a half days, and nothing older survived.
section "3. A year of hourly backups prunes to about a hundred, spanning a year"

rm -f "${BACKUPS}"/endless-*.db
for days in $(seq 0 365); do
    seed "${days}" 03 >/dev/null
    seed "${days}" 15 >/dev/null
done
seeded="$(count_backups)"
assert_eq "seeded two backups a day for a year" "732" "${seeded}"

out="$(go_ jobs run --job db-backup)"
assert_contains "the sweep ran without error" "db-backup: ok" "${out}"

# The fixture seeds twice a day, so the hourly tier holds only what it can see;
# the daily and weekly tiers carry the rest. The number is governed by AGE, and
# the point is that it is nowhere near the 732 seeded and nowhere near a fixed 60.
kept="$(count_backups)"
if (( kept >= 60 && kept <= 130 )); then
    report_pass "retention kept ${kept} of 732 files, by age tier"
else
    report_fail "retention census" "between 60 and 130 files" "${kept}"
fi

# The claim the whole tiering exists for: reach back a YEAR. Under keep-last-60
# the oldest survivor of this fixture would be a month at most.
oldest="$(oldest_backup)"
oldest_stamp="${oldest#endless-}"
oldest_day="${oldest_stamp%%-*}"
year_ago="$(date -v-360d "+%Y%m%d" 2>/dev/null || date -d '360 days ago' "+%Y%m%d")"
if [[ "${oldest_day}" -le "${year_ago}" ]]; then
    report_pass "the oldest surviving backup reaches back a year (${oldest_day})"
else
    report_fail "oldest surviving backup" "on or before ${year_ago}" "${oldest_day}"
fi

# ── 4. what the sweep must never do ─────────────────────────────────────────
# The old rotation deleted by LIST POSITION, so anything sorting early went
# first — a parked pre-restore copy, an operator's own file, a README.
section "4. The sweep touches only its own files, and never empties the shelf"

: >"${BACKUPS}/operators-copy.sqlite"
: >"${BACKUPS}/README.md"
: >"${BACKUPS}/endless-20240101-030000.db-wal"
mkdir -p "${BACKUPS}/nested"

go_ jobs run --job db-backup >/dev/null
for foreign in operators-copy.sqlite README.md endless-20240101-030000.db-wal nested; do
    if [[ -e "${BACKUPS}/${foreign}" ]]; then
        report_pass "left ${foreign} alone — it is not one of BackupDB's names"
    else
        report_fail "foreign file survives the sweep" "${foreign} present" "removed"
    fi
done

# A shelf on which EVERY file has aged out of every tier — the one case that
# could prune to zero. Live, the run writes a fresh copy before it sweeps, so
# what this proves is that the sweep clears the expired files and still leaves
# the shelf occupied. The policy's own never-drop-the-newest rule, on a shelf
# with no fresh copy at all, is the unit gate's case
# (TestBackupsToPruneNeverEmptiesTheShelf): it cannot be reached through the CLI
# precisely because a backup always precedes a sweep.
rm -f "${BACKUPS}"/endless-*.db
ancient="$(seed 900 04)"
older="$(seed 1200 04)"
go_ jobs run --job db-backup >/dev/null
assert_eq "both aged-out backups are swept" "removed removed" \
    "$([[ -e "${BACKUPS}/${ancient}" ]] && echo present || echo removed) $([[ -e "${BACKUPS}/${older}" ]] && echo present || echo removed)"
if (( $(count_backups) >= 1 )); then
    report_pass "and the shelf is not empty afterwards"
else
    report_fail "shelf after sweeping a wholly aged-out directory" "at least one backup" "0"
fi

# ── 5. the throttle is a throttle, not a cadence ────────────────────────────
# The misreading that let the newest backup go nine days stale. It stops two
# writes seconds apart; it has never made a backup happen — and it must not stop
# retention either, or a machine mid-throttle stops being pruned.
section "5. The 60s window throttles writes, not the sweep"

rm -f "${BACKUPS}"/endless-*.db
first="$(go_ event backup)"
assert_contains "the first call writes" '"status":"ok"' "${first}"

stale="$(seed 400 06)"
second="$(go_ event backup)"
assert_contains "a call inside the window writes nothing" '"status":"skipped"' "${second}"
assert_contains "and names the backup that already covers this moment" \
    '"path":"' "${second}"
assert_eq "but the sweep still ran: the aged-out file is gone" \
    "removed" "$([[ -e "${BACKUPS}/${stale}" ]] && echo present || echo removed)"
assert_contains "and it reports what it pruned" '"pruned":1' "${second}"

summary
