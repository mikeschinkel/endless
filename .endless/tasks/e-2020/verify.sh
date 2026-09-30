#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2020 and records what was true when E-2020
# landed. Edit it only if you ARE E-2020. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2020: connect-time version checks replace apply-on-connect.
#
# THE RULES
#   R1  A binary built in a task worktree never opens the main database, at any
#       version (ED-1601). Refused BEFORE the file is opened.
#   R2  Otherwise the connect compares the database's goose version with the
#       binary's: behind → back up, migrate forward; ahead → halt; equal →
#       nothing to apply. Seed and the enum gates run on every connect that is
#       not refused — schema-passive is gone.
#   R3  Refusal is per-surface: hook, tmux and jobs are silent with ONE
#       deduplicated fault (ERR-0020); an interactive command refuses loudly,
#       naming both versions.
#   R4  `endless db upgrade` backs up, migrates, reseeds, and opens the FILE, so
#       it works while every ordinary connect refuses. It refuses a worktree
#       build aimed at main.
#   R5  A self_dev land records its landing with the installed binary, rebuilt
#       from the advanced main after the migration (Step 5.6).
#
# Two binaries play the parts: this worktree's bin/endless-go is the WORKTREE
# BUILD (its path contains /.endless/worktrees/), and a byte-identical copy
# under the scratch dir is the INSTALLED binary (its path does not). "Main" is
# $HOME/.config/endless/endless.db under the runner's temp HOME.
#
# Layers:
#   A. Fail-fast: build, and this task's Go and Python tests.
#   B. The durable tests by name, so deleting one cannot pass by absence.
#   C. End to end against real binaries and a real database.
#   D. A real land, control then fix (R5).
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required to stage databases"

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

GO_PKGS=(./internal/monitor/ ./internal/schema/... ./internal/schemachange/
         ./internal/faults/ ./internal/jobs/ ./internal/hookcmd/ ./internal/tmuxcmd/
         ./internal/eventcmd/)
if out=$(go test -count=1 "${GO_PKGS[@]}" 2>&1); then
    report_pass "go test: monitor, schema, schemachange, faults, jobs, hookcmd, tmuxcmd, eventcmd"
else
    report_fail "go test" "exit 0" "$(printf '%s' "${out}" | grep -v '^ok' | tail -30)"
    summary
fi

PY_TESTS=(tests/test_worktree_land_record_binary.py
          tests/test_db_upgrade_cmd.py
          tests/test_worktree_land_migrate_up.py
          tests/test_worktree_land_migrate_exec.py
          tests/test_worktree_land_schema_apply.py)
if out=$(uv run pytest -q "${PY_TESTS[@]}" 2>&1); then
    report_pass "pytest: land record binary, db upgrade, land migrate-up/exec/schema-apply"
else
    report_fail "pytest" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# ─── B. the durable tests, by name ──────────────────────────────────────────

section "B. Each rule's durable test runs and passes"

go_contract() {
    local pkg="$1" t="$2"
    if go test -count=1 "${pkg}" -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}"; then
        report_pass "go: ${t}"
    else
        report_fail "go: ${t}" "--- PASS: ${t}" "no PASS line"
    fi
}
go_contract ./internal/monitor/ TestConnectActionFor
go_contract ./internal/monitor/ TestRefusesMainDB
go_contract ./internal/monitor/ TestDB_WorktreeBuildRefusesMainAtEveryVersion
go_contract ./internal/monitor/ TestDB_WorktreeBuildMigratesItsOwnBehindDatabase
go_contract ./internal/monitor/ TestDB_BehindIsBackedUpThenMigrated
go_contract ./internal/monitor/ TestDB_CurrentAppliesNothing
go_contract ./internal/monitor/ TestDB_AheadHalts
go_contract ./internal/monitor/ TestDB_SeedAndGatesRunOnEveryConnect

# ─── C. end to end ──────────────────────────────────────────────────────────

INST_DIR="${TMP}/installed"
mkdir -p "${INST_DIR}"
cp "${WT}/bin/endless-go" "${INST_DIR}/endless-go"
INST="${INST_DIR}/endless-go"
CAND="${WT}/bin/endless-go"
LATEST="$(ls internal/schema/migrations | grep -E '^[0-9]+_' | sed -E 's/^0*([0-9]+)_.*/\1/' | sort -n | tail -1)"
PREV=$((LATEST - 1))

# fresh_home <name> — a temp HOME whose main database is built at the latest
# version by the INSTALLED binary; sets H and DB.
fresh_home() {
    H="${TMP}/home-$1"
    DB="${H}/.config/endless/endless.db"
    mkdir -p "${H}/.config/endless"
    printf '{}\n' >"${H}/.config/endless/config.json"
    (cd "${TMP}" && HOME="${H}" XDG_CONFIG_HOME= "${INST}" --db main event migrate >/dev/null) \
        || setup_error "could not build the scratch main database"
}

# hold_behind — un-stamp the newest migration. The newest one is replay-safe
# (00010 only UPDATEs, DROPs IF EXISTS and DELETEs), so re-applying it is exact.
hold_behind() {
    sqlite3 "${DB}" "DELETE FROM goose_db_version WHERE version_id = ${LATEST}" \
        || setup_error "could not hold the database behind"
}
push_ahead() {
    sqlite3 "${DB}" "INSERT INTO goose_db_version (version_id, is_applied) VALUES ($((LATEST + 1)), 1)" \
        || setup_error "could not push the database ahead"
}
version_of() { sqlite3 "$1" 'SELECT max(version_id) FROM goose_db_version'; }
schema_of() { sqlite3 "$1" 'SELECT type, name, sql FROM sqlite_master ORDER BY type, name'; }

run_as() { # run_as <binary> <args...> ; sets RC, OUT, ERR
    local bin="$1"; shift
    OUT=$(cd "${TMP}" && HOME="${H}" XDG_CONFIG_HOME= "${bin}" "$@" 2>"${TMP}/stderr")
    RC=$?
    ERR=$(cat "${TMP}/stderr")
}

section "C1. A worktree build never opens main, and changes nothing (R1)"

fresh_home r1
hold_behind
before="$(schema_of "${DB}")"
run_as "${CAND}" --db main event migrate
assert_eq "the worktree build's connect to main fails" "1" "${RC}"
assert_contains "it says why, naming the rule" "a worktree build never opens main" "${ERR}"
assert_eq "sqlite_master is byte-identical afterwards" "${before}" "$(schema_of "${DB}")"
assert_eq "main is still one version behind" "${PREV}" "$(version_of "${DB}")"
assert_eq "no backup was taken" "0" "$(ls "${H}/.config/endless/backups" 2>/dev/null | wc -l | tr -d ' ')"

push_ahead
run_as "${CAND}" --db main event migrate
assert_contains "refused at a version AHEAD too — the refusal is not about versions" \
    "a worktree build never opens main" "${ERR}"
sqlite3 "${DB}" "DELETE FROM goose_db_version WHERE version_id > ${PREV}"
sqlite3 "${DB}" "INSERT INTO goose_db_version (version_id, is_applied) VALUES (${LATEST}, 1)"
run_as "${CAND}" --db main event migrate
assert_contains "refused at the SAME version" "a worktree build never opens main" "${ERR}"

section "C2. The installed binary: behind migrates after a backup, ahead halts (R2)"

fresh_home r2
hold_behind
run_as "${INST}" --db main event migrate
assert_eq "an installed connect to a behind database succeeds" "0" "${RC}"
assert_eq "it migrated forward to the latest version" "${LATEST}" "$(version_of "${DB}")"
backup="$(ls "${H}/.config/endless/backups"/endless-*.db 2>/dev/null | head -1)"
assert_eq "it backed up first, at the version it found" "${PREV}" "$( [[ -n "${backup}" ]] && version_of "${backup}")"

run_as "${INST}" --db main event migrate
assert_eq "a current database: nothing new is backed up" "1" \
    "$(ls "${H}/.config/endless/backups" | wc -l | tr -d ' ')"

section "C3. The hook is silent on a mismatch, with exactly one fault (R3)"

fresh_home r3
push_ahead
hook() {
    printf '%s' '{"hook_event_name":"PostToolUse","session_id":"s-e2020","cwd":"'"${TMP}"'","tool_name":"Bash","tool_input":{"command":"ls"}}' \
        | (cd "${TMP}" && HOME="${H}" XDG_CONFIG_HOME= "${INST}" hook claude >"${TMP}/hook.out" 2>"${TMP}/hook.err")
}
hook
assert_eq "the hook exits 0" "0" "$?"
assert_eq "the hook writes nothing to stdout" "0" "$(wc -c <"${TMP}/hook.out" | tr -d ' ')"
assert_eq "the hook writes nothing to stderr" "0" "$(wc -c <"${TMP}/hook.err" | tr -d ' ')"
assert_eq "exactly one incident is recorded, as ERR-0020" "ERR-0020|1" \
    "$(sqlite3 "${DB}" "SELECT code || '|' || occurrences FROM errors")"
assert_contains "the incident names both versions" \
    "database is at schema v$((LATEST + 1)), endless-go carries v${LATEST}" \
    "$(sqlite3 "${DB}" 'SELECT summary FROM errors')"

section "C4. Fifty hook events are one incident with a count of fifty (R3)"

for _ in $(seq 49); do hook; done
assert_eq "one incident, occurrence 50" "1|50" \
    "$(sqlite3 "${DB}" "SELECT count(*) || '|' || max(occurrences) FROM errors")"

(cd "${TMP}" && HOME="${H}" XDG_CONFIG_HOME= TMUX_PANE=%1 "${INST}" tmux status-line >"${TMP}/tmux.out" 2>"${TMP}/tmux.err")
assert_eq "the tmux status line exits 0" "0" "$?"
assert_eq "the tmux status line writes nothing to stderr" "0" "$(wc -c <"${TMP}/tmux.err" | tr -d ' ')"
assert_eq "…and joins the same incident rather than opening its own" "1|51" \
    "$(sqlite3 "${DB}" "SELECT count(*) || '|' || max(occurrences) FROM errors")"

section "C5. An interactive command refuses loudly on the same database (R3)"

run_as "${INST}" --db main event migrate
assert_eq "the command fails" "1" "${RC}"
assert_contains "it names the database's version" "schema version $((LATEST + 1))" "${ERR}"
assert_contains "it names the binary's version" "carries version ${LATEST}" "${ERR}"
assert_contains "it says what to do" "Upgrade endless" "${ERR}"

section "C6. endless db upgrade backs up first, and the backup restores (R4)"

endless_cli() { # endless_cli <args...> ; the Python CLI with the installed endless-go on PATH
    OUT=$(cd "${TMP}" && HOME="${H}" XDG_CONFIG_HOME= PATH="${INST_DIR}:${PATH}" \
        uv run --project "${WT}" endless "$@" 2>&1)
    RC=$?
}
fresh_home r6
hold_behind
pre_schema="$(schema_of "${DB}")"
endless_cli db upgrade
assert_eq "db upgrade succeeds" "0" "${RC}"
assert_contains "it reports the versions it moved between" \
    "from schema version ${PREV} to ${LATEST}" "${OUT}"
assert_contains "it names the backup" "Backed up to" "${OUT}"
assert_eq "the database is at the latest version" "${LATEST}" "$(version_of "${DB}")"
backup="$(ls "${H}/.config/endless/backups"/endless-*.db | head -1)"
assert_eq "the backup holds the pre-upgrade version" "${PREV}" "$(version_of "${backup}")"
endless_cli db restore "${backup}" --force
assert_eq "db restore from that backup succeeds" "0" "${RC}"
assert_eq "the restored database is at the pre-upgrade version" "${PREV}" "$(version_of "${DB}")"
assert_eq "the restored database has the pre-upgrade schema" "${pre_schema}" "$(schema_of "${DB}")"

section "C7. db upgrade refuses a worktree build aimed at main (R4)"

fresh_home r7
hold_behind
before="$(schema_of "${DB}")"
run_as "${CAND}" --db main event upgrade
assert_eq "the worktree build's upgrade fails" "1" "${RC}"
assert_contains "it says why" "a worktree build never opens main" "${ERR}"
assert_eq "main is untouched" "${before}" "$(schema_of "${DB}")"
assert_eq "main is still behind" "${PREV}" "$(version_of "${DB}")"

section "C7a. db upgrade works while every ordinary connect is refusing (R4)"

# An ahead database is refused by the connect AND by upgrade (nothing forward
# to apply), so the refusing connect here is a fail-closed enum gate:
# gate_kinds is seeded INSERT OR IGNORE, so a drifted row survives every
# reseed and fail-closes every connect.
fresh_home r7a
sqlite3 "${DB}" "UPDATE gate_kinds SET slug='drifted' WHERE id=1; UPDATE task_types SET label='Drifted' WHERE id=1;"
run_as "${INST}" --db main event migrate
assert_eq "an ordinary command is refused by the gate" "1" "${RC}"
assert_contains "…on the enum integrity check" "gate_kinds integrity check" "${ERR}"
endless_cli db upgrade
assert_eq "db upgrade still runs to completion" "0" "${RC}"
assert_contains "and reports the version it found" "already at schema version ${LATEST}" "${OUT}"
assert_eq "it reseeded the mirrors it can reconcile" "Todo" \
    "$(sqlite3 "${DB}" 'SELECT label FROM task_types WHERE id=1')"

section "C8. Schema-passive is gone from the source (R2)"

hits="$(grep -rn --include='*.go' -E 'pinnedToForeignRealDB|foreignRealDB|PinnedToRealDB' internal cmd || true)"
assert_eq "no pinnedToForeignRealDB / foreignRealDB / PinnedToRealDB remains" "" "${hits}"

section "C9. The messages this task adds point at db upgrade, not at change files"

new_msgs="$(cat internal/monitor/schema_version.go; sed -n '/^def db_upgrade/,/^@db_cmd.command("restore")/p' src/endless/cli.py; sed -n '/^func runUpgrade/,/^}/p' internal/eventcmd/event.go)"
assert_not_contains "no new message names apply-change" "apply-change" "${new_msgs}"
assert_not_contains "no new message names a change file" "schema/changes" "${new_msgs}"
assert_contains "the failed-upgrade refusal names endless db upgrade" \
    "Run \`endless db upgrade\` to retry" "${new_msgs}"

# ─── D. a real land ─────────────────────────────────────────────────────────

section "D. A land records itself with the installed binary (R5)"

land() {
    fresh_home "land-$1"
    hold_behind
    # The installed copy first on PATH, so the unpinned pre-migrate backup
    # resolves it rather than whatever endless-go this machine has installed.
    OUT=$(cd "${TMP}" && HOME="${H}" XDG_CONFIG_HOME= PATH="${INST_DIR}:${PATH}" \
        uv run --project "${WT}" python "${ENDLESS_VERIFY_DIR}/land_e2e.py" \
            "${WT}" "${TMP}/repo-$1" "$1" 2>&1)
}

land control
assert_contains "control (record with the worktree build, as before): main advances" \
    "main_advanced=yes" "${OUT}"
assert_contains "control: the worktree build is refused the main database" \
    "a worktree build never opens main" "${OUT}"
assert_contains "control: no landing is recorded" "landings=0" "${OUT}"

land fixed
assert_contains "fixed: the land completes" "outcome=landed" "${OUT}"
assert_contains "fixed: main advances" "main_advanced=yes" "${OUT}"
assert_contains "fixed: the database was migrated to the branch's latest" \
    "db_version=${LATEST}" "${OUT}"
assert_contains "fixed: migrate, then rebuild the installed binary, then record with it" \
    "order=migrate,rebuild,record:main" "${OUT}"
assert_contains "fixed: the landing is recorded in the same run" "landings=1" "${OUT}"

summary
