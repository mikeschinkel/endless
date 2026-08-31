#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1942 and records what was true when E-1942
# landed. Edit it only if you ARE E-1942. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1942 verification — `endless db restore` recovers from a backup safely.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1942
#
# Background. `endless db backup` has existed for a long time and `just land`
# calls it, so the safety net was half built: there was no supported way to USE
# a backup. On 2026-08-10 recovery was an unguided `cp`, and it went wrong twice
# before it went right:
#
#   1. Copied over a database six processes still had open (two
#      `session-status --monitor`, four `endless task show -p` abandoned in
#      pagers for up to 22 days). That left a hot `endless.db-journal`; a
#      read-only connection cannot roll one back, so every reader returned
#      'database is locked'.
#   2. `db backup` uses VACUUM INTO, which writes a ROLLBACK-JOURNAL database,
#      while the live DB is WAL. After the copy every connection fought for an
#      exclusive lock trying to switch back.
#
# So this script does not check "does it copy a file". It checks the four things
# a `cp` gets wrong: holders are REPORTED and the restore REFUSES, sidecars move
# with the file, WAL comes back, and the pre-restore database is kept aside so
# the restore itself is reversible.
#
# Checks 2-6 drive the REAL `endless db restore` against a throwaway
# XDG_CONFIG_HOME from a cwd outside the worktree, so no real database, backup,
# or ledger is touched. Check 1 is the pytest layer.
#
# Fail-fast: each section aborts the run on its first failure, so the first
# broken invariant is the last thing printed.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 on any
# failure, 2 on environment/setup error.
#
# Model: .endless/tasks/e-1941/verify.sh (the E-1596 reference shape).

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
PYBIN=""
SANDBOX=""

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'
    RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi

UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() {
    printf '\n%s%s%s\n' "${BOLD}" "$1" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
}

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

report_skip() {
    printf '  %s-%s %s %s(skipped: %s)%s\n' \
        "${DIM}" "${RESET}" "$1" "${DIM}" "$2" "${RESET}"
}

bail_if_failed() {
    if [[ "${FAIL_COUNT}" -gt 0 ]]; then
        printf '\n  %sfail-fast: stopping at the first broken invariant%s\n' \
            "${DIM}" "${RESET}"
        summary
        exit 1
    fi
}

summary() {
    printf '\n%sSummary%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n' "${GREEN}" "${PASS_COUNT}" "${RESET}"
        printf '\n  %sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}"
    printf '\n  %sFAILED:%s\n' "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do
        printf '    - %s\n' "${t}"
    done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

assert_eq() {  # DESC EXPECTED ACTUAL
    if [[ "$2" == "$3" ]]; then report_pass "$1"; return; fi
    report_fail "$1" "$2" "$3"
}

assert_contains() {  # DESC HAYSTACK NEEDLE
    if [[ "$2" == *"$3"* ]]; then report_pass "$1"; return; fi
    report_fail "$1" "output containing '$3'" "${2:-<empty>}"
}

assert_not_contains() {  # DESC HAYSTACK NEEDLE
    if [[ "$2" != *"$3"* ]]; then report_pass "$1"; return; fi
    report_fail "$1" "output WITHOUT '$3'" "${2}"
}

assert_succeeds() {  # DESC CMD [ARGS...]
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | output=${output}"
}

assert_file() {  # DESC PATH
    if [[ -f "$2" ]]; then report_pass "$1"; return; fi
    report_fail "$1" "file exists: $2" "absent"
}

assert_no_file() {  # DESC PATH
    if [[ ! -e "$2" ]]; then report_pass "$1"; return; fi
    report_fail "$1" "no such path: $2" "present"
}

# ─── harness ────────────────────────────────────────────────────────────────

# Build a throwaway config dir with a live DB and one backup.
#   $1 = config root (XDG_CONFIG_HOME); the DB lands at $1/endless/endless.db
#   $2 = marker row in the live DB
#   $3 = marker row in the backup
make_fixture() {
    local root="$1" live_marker="$2" backup_marker="$3"
    mkdir -p "${root}/endless/backups" || return 1
    "${PYBIN}" - "${root}/endless" "${live_marker}" "${backup_marker}" <<'PY'
import sqlite3, sys
from pathlib import Path

cfg = Path(sys.argv[1])

def make(path, marker, wal):
    conn = sqlite3.connect(str(path))
    if wal:
        conn.execute("PRAGMA journal_mode=WAL")
    conn.execute("CREATE TABLE tasks (id INTEGER PRIMARY KEY, title TEXT)")
    conn.execute("CREATE TABLE projects (id INTEGER PRIMARY KEY, name TEXT)")
    conn.execute("INSERT INTO tasks (title) VALUES (?)", (marker,))
    conn.commit()
    conn.close()

# The live DB is WAL, as a real ledger is. The backup is rollback-journal, as
# VACUUM INTO writes it — the exact mismatch that bit on 2026-08-10.
make(cfg / "endless.db", sys.argv[2], wal=True)
make(cfg / "backups" / "endless-20260810-051500.db", sys.argv[3], wal=False)
PY
}

# READ-ONLY read. Deliberately strict: a hot journal beside the file makes this
# fail with "attempt to write a readonly database", which is the exact symptom
# every reader hit on 2026-08-10. So a passing marker_of on the restored file is
# itself the assertion that no hot journal came along for the ride.
marker_of() {  # $1 = db path
    "${PYBIN}" -c '
import sqlite3, sys
conn = sqlite3.connect("file:%s?mode=ro" % sys.argv[1], uri=True)
print(conn.execute("SELECT title FROM tasks").fetchone()[0])
conn.close()
' "$1" 2>/dev/null
}

# Read-write read, for the PARKED copy: it is parked together with whatever
# sidecar it had, so read-only is expected to fail there — that is the point of
# moving the sidecar rather than deleting it.
marker_of_rw() {  # $1 = db path
    "${PYBIN}" -c '
import sqlite3, sys
conn = sqlite3.connect(sys.argv[1])
print(conn.execute("SELECT title FROM tasks").fetchone()[0])
conn.close()
' "$1" 2>/dev/null
}

journal_of() {  # $1 = db path — read from the header, no lock
    "${PYBIN}" -c '
import sys
head = open(sys.argv[1], "rb").read(20)
print("wal" if head[18] == 2 else "rollback")
' "$1" 2>/dev/null
}

# Run the real CLI against a throwaway config dir, from a cwd OUTSIDE the
# worktree so the self-dev --db gate does not apply and XDG routing does.
# Echoes combined output; caller reads $? for the exit code.
run_restore() {  # $1 = config root, rest = args to `endless db restore`
    local root="$1"; shift
    (
        cd "${root}" || exit 2
        XDG_CONFIG_HOME="${root}" \
            uv run --project "${REPO_ROOT}" endless db restore "$@" 2>&1
    )
}

# ─── check 1: unit suite ────────────────────────────────────────────────────

test_unit_suite() {
    section "1. Automated suite"

    assert_succeeds "pytest tests/test_db_restore.py" \
        uv run pytest tests/test_db_restore.py -q
    bail_if_failed

    # The `db backup` message layer. Covered by pytest rather than end-to-end
    # because `db backup` pins main_config_dir() by design, so there is no
    # throwaway config dir to point it at.
    assert_succeeds "pytest tests/test_db_backup_message.py" \
        uv run pytest tests/test_db_backup_message.py -q
    bail_if_failed

    assert_succeeds "go test ./internal/eventcmd/ -run TestEventBackup" \
        go test ./internal/eventcmd/ -run TestEventBackup
    bail_if_failed
}

# ─── check 2: the command exists and is discoverable ────────────────────────

test_command_surface() {
    section "2. The verb exists, and the guide points at it"

    local out
    mkdir -p "${SANDBOX}/help/endless"
    out="$(run_restore "${SANDBOX}/help" --help)"
    assert_contains "endless db restore --help works" "${out}" "Restore the database"
    assert_contains "--dry-run is offered" "${out}" "--dry-run"
    assert_contains "--force is offered" "${out}" "--force"

    local guide
    guide="$(uv run endless guide reference 2>&1)"
    assert_contains "the guide documents restore" "${guide}" "endless db restore"
    assert_contains "the guide names the pre-restore directory" \
        "${guide}" "pre-restore"

    bail_if_failed
}

# ─── check 3: dry run reports, and changes nothing ──────────────────────────

test_dry_run() {
    section "3. Dry run reports holders and journal modes, and writes nothing"

    local root="${SANDBOX}/dry"
    make_fixture "${root}" LIVE BACKUP || { report_fail "fixture" ok setup-failed; bail_if_failed; }
    local db="${root}/endless/endless.db"
    local before; before="$(marker_of "${db}")"

    local out rc
    out="$(run_restore "${root}" --dry-run)"; rc=$?

    assert_eq "dry run exits 0" "0" "${rc}"
    assert_contains "names the target" "${out}" "endless.db"
    assert_contains "reports the live DB as WAL" "${out}" "journal: wal"
    assert_contains "reports the backup as rollback-journal (VACUUM INTO)" \
        "${out}" "journal: rollback"
    assert_contains "reports the backup's integrity" "${out}" "integrity: ok"
    assert_contains "names where the pre-restore copy would go" \
        "${out}" "pre-restore"
    assert_contains "says nothing was written" "${out}" "no files were changed"
    assert_eq "the live DB is untouched" "LIVE" "$(marker_of "${db}")"
    assert_eq "…really untouched" "${before}" "$(marker_of "${db}")"
    assert_no_file "no pre-restore directory was created" \
        "${root}/endless/pre-restore"

    bail_if_failed
}

# ─── check 4: open handles are reported, and the restore refuses ────────────

test_refuses_while_open() {
    section "4. An open database is REPORTED and the restore refuses"

    local root="${SANDBOX}/open"
    make_fixture "${root}" LIVE BACKUP || { report_fail "fixture" ok setup-failed; bail_if_failed; }
    local db="${root}/endless/endless.db"

    # A process holding the DB open — the incident's shape (an abandoned pager).
    "${PYBIN}" -c '
import sqlite3, sys, time
c = sqlite3.connect(sys.argv[1])
c.execute("SELECT count(*) FROM tasks").fetchone()
time.sleep(60)
' "${db}" &
    local holder_pid=$!
    sleep 1

    local out rc
    out="$(run_restore "${root}")"; rc=$?

    assert_eq "restore refuses (exit non-zero)" \
        "nonzero" "$([[ "${rc}" -ne 0 ]] && echo nonzero || echo "exit=${rc}")"
    assert_contains "says why" "${out}" "refusing to restore"
    assert_contains "points at the override" "${out}" "--force"
    assert_eq "the live DB is untouched" "LIVE" "$(marker_of "${db}")"
    assert_no_file "nothing was parked" "${root}/endless/pre-restore"

    if command -v lsof >/dev/null 2>&1 || [[ -d /proc ]]; then
        assert_contains "names the holder by pid" "${out}" "pid ${holder_pid}"
        assert_contains "names the holder's command, not just its pid" \
            "${out}" "sqlite3"
    else
        report_skip "names the holder by pid" "no lsof and no /proc"
    fi

    # --force overrides, and warns that the holder reads the parked copy.
    out="$(run_restore "${root}" --force)"; rc=$?
    assert_eq "--force restores anyway (exit 0)" "0" "${rc}"
    assert_eq "the backup is now live" "BACKUP" "$(marker_of "${db}")"
    assert_contains "--force warns about the still-open holders" \
        "${out}" "still do"

    kill "${holder_pid}" 2>/dev/null
    wait "${holder_pid}" 2>/dev/null

    bail_if_failed
}

# ─── check 5: the restore itself ────────────────────────────────────────────

test_restore() {
    section "5. Restore: WAL back, integrity checked, pre-restore kept aside"

    local root="${SANDBOX}/restore"
    make_fixture "${root}" LIVE BACKUP || { report_fail "fixture" ok setup-failed; bail_if_failed; }
    local db="${root}/endless/endless.db"

    # A hot rollback journal left behind by a killed writer — failure 1 in a
    # bottle if it survives beside the restored file.
    printf 'hot journal\n' > "${db}-journal"

    local out rc
    out="$(run_restore "${root}")"; rc=$?

    assert_eq "restore exits 0" "0" "${rc}"
    assert_eq "the backup's data is live, and a READ-ONLY reader can see it" \
        "BACKUP" "$(marker_of "${db}")"
    assert_eq "journal mode is WAL again (not the backup's rollback)" \
        "wal" "$(journal_of "${db}")"
    assert_contains "reports journal_mode" "${out}" "journal_mode: wal"
    assert_contains "reports integrity_check" "${out}" "integrity_check: ok"
    assert_no_file "the stale -journal did not survive beside the new DB" \
        "${db}-journal"

    local parked sidecar_moved
    parked="$(ls "${root}/endless/pre-restore/"*.db 2>/dev/null | head -1)"
    # Sample the sidecar BEFORE reading the parked copy: opening it read-write
    # rolls the journal back and removes it, which would erase the evidence.
    sidecar_moved="$(ls "${root}/endless/pre-restore/"*.db-journal >/dev/null 2>&1 \
        && echo yes || echo no)"

    assert_file "the pre-restore database was kept" "${parked}"
    assert_eq "the sidecar moved with it (not deleted, not left behind)" \
        "yes" "${sidecar_moved}"
    assert_eq "…and it holds the PRE-restore data" "LIVE" \
        "$(marker_of_rw "${parked}")"
    assert_contains "the report names the parked copy" "${out}" "pre-restore"

    # Rotation safety: `db backup` keeps the last 60 entries of backups/ by
    # name, so parking the pre-restore copy there would evict real backups.
    assert_eq "backups/ still holds exactly its backups" "1" \
        "$(ls "${root}/endless/backups" | wc -l | tr -d ' ')"

    bail_if_failed
}

# ─── check 6: refusals before anything is touched ───────────────────────────

test_refusals() {
    section "6. Refusals happen BEFORE anything is written"

    local root="${SANDBOX}/refuse"
    make_fixture "${root}" LIVE BACKUP || { report_fail "fixture" ok setup-failed; bail_if_failed; }
    local db="${root}/endless/endless.db"
    local out rc

    # A file in backups/ that is not a database at all.
    printf 'not sqlite\n' > "${root}/endless/backups/endless-20260811-000000.db"
    out="$(run_restore "${root}")"; rc=$?
    assert_eq "a corrupt backup is refused (exit non-zero)" \
        "nonzero" "$([[ "${rc}" -ne 0 ]] && echo nonzero || echo "exit=${rc}")"
    assert_contains "says the backup is the problem" "${out}" "refusing to restore"
    assert_eq "the live DB is untouched" "LIVE" "$(marker_of "${db}")"
    rm -f "${root}/endless/backups/endless-20260811-000000.db"

    # A named backup that does not exist.
    out="$(run_restore "${root}" endless-19700101-000000.db)"; rc=$?
    assert_eq "a missing named backup is refused" \
        "nonzero" "$([[ "${rc}" -ne 0 ]] && echo nonzero || echo "exit=${rc}")"
    assert_contains "names the directory it looked in" "${out}" "backups"
    assert_eq "the live DB is untouched" "LIVE" "$(marker_of "${db}")"

    # No backups at all.
    local bare="${SANDBOX}/bare"
    mkdir -p "${bare}/endless"
    out="$(run_restore "${bare}")"; rc=$?
    assert_eq "no backups at all is refused" \
        "nonzero" "$([[ "${rc}" -ne 0 ]] && echo nonzero || echo "exit=${rc}")"
    assert_contains "says where backups are expected" "${out}" "no backups found"

    # The self-dev --db gate: restore opens neither db.get_db() nor a Go
    # shellout, so it must enforce the gate itself. Run from INSIDE the worktree
    # with no --db; XDG still points at the throwaway dir, so a leak would
    # damage the fixture, not the real ledger.
    out=$(XDG_CONFIG_HOME="${root}" uv run endless db restore 2>&1); rc=$?
    assert_eq "no --db inside a self-dev worktree is refused" \
        "nonzero" "$([[ "${rc}" -ne 0 ]] && echo nonzero || echo "exit=${rc}")"
    assert_contains "the refusal names --db" "${out}" "--db"
    assert_eq "the live DB is untouched" "LIVE" "$(marker_of "${db}")"

    bail_if_failed
}

# ─── check 7: backup names the file it wrote ────────────────────────────────

test_backup_names_the_file() {
    section "7. Backing up names the file it wrote"

    # A restore starts by picking a backup, so "Database backed up." — with no
    # path — is unusable as the first half of one. Driven against the WORKTREE
    # binary: the global endless-go predates this change.
    local root="${SANDBOX}/backup"
    mkdir -p "${root}/endless"
    "${PYBIN}" -c '
import sqlite3, sys
c = sqlite3.connect(sys.argv[1])
c.execute("CREATE TABLE tasks (id INTEGER PRIMARY KEY, title TEXT)")
c.commit(); c.close()
' "${root}/endless/endless.db"

    local first second path
    first="$("${REPO_ROOT}/bin/endless-go" --config-dir "${root}/endless" \
        event backup 2>&1)"
    assert_contains "reports success" "${first}" '"status":"ok"'
    assert_contains "reports a path" "${first}" '"path":'

    path="$("${PYBIN}" -c '
import json, sys
print(json.loads(sys.argv[1])["path"])
' "${first}" 2>/dev/null)"
    assert_file "the reported path is a real file" "${path}"
    assert_contains "…and it is under the config dir it was told to use" \
        "${path}" "${root}/endless/backups/"

    # Inside the 60s throttle nothing is written; claiming otherwise would date
    # the user's backup wrong by up to a minute at exactly the wrong moment.
    second="$("${REPO_ROOT}/bin/endless-go" --config-dir "${root}/endless" \
        event backup 2>&1)"
    assert_contains "a throttled re-run says it wrote nothing" \
        "${second}" '"status":"skipped"'
    assert_contains "…and names the backup that already covers it" \
        "${second}" "${path}"

    # Nothing to back up is a failure, not a silent success.
    local bare rc out
    bare="${SANDBOX}/backup-bare"
    mkdir -p "${bare}/endless"
    out="$("${REPO_ROOT}/bin/endless-go" --config-dir "${bare}/endless" \
        event backup 2>&1)"; rc=$?
    assert_eq "no database to back up exits non-zero" \
        "nonzero" "$([[ "${rc}" -ne 0 ]] && echo nonzero || echo "exit=${rc}")"
    assert_contains "…and names the path it looked for" "${out}" "no database at"

    bail_if_failed
}

# ─── main ───────────────────────────────────────────────────────────────────

cleanup() {
    [[ -n "${SANDBOX}" && -d "${SANDBOX}" ]] && rm -rf "${SANDBOX}"
}

main() {
    REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null)"
    if [[ -z "${REPO_ROOT}" ]]; then
        printf 'ERROR: not inside a git worktree\n' >&2
        exit 2
    fi
    cd "${REPO_ROOT}" || exit 2

    if ! command -v uv >/dev/null 2>&1; then
        printf 'ERROR: uv not on PATH\n' >&2
        exit 2
    fi

    PYBIN="$(uv run python -c 'import sys; print(sys.executable)' 2>/dev/null)"
    if [[ -z "${PYBIN}" ]]; then
        printf 'ERROR: could not resolve the project interpreter\n' >&2
        exit 2
    fi

    SANDBOX="$(mktemp -d)"
    trap cleanup EXIT

    printf '%sE-1942 verification%s\n' "${BOLD}" "${RESET}"
    printf '%s\n' "${UNDERLINE}"
    printf '  cwd:     %s\n' "${REPO_ROOT}"
    printf '  db:      none — throwaway XDG_CONFIG_HOME under %s\n' "${SANDBOX}"

    test_unit_suite
    test_command_surface
    test_dry_run
    test_refuses_while_open
    test_restore
    test_refusals
    test_backup_names_the_file

    summary
}

main "$@"
