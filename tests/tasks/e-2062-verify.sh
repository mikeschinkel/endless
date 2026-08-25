#!/usr/bin/env bash
#
# E-2062 verification — `rebuild-db --confirm` refuses deliberately.
#
# Before: `endless-go event rebuild-db --confirm` replayed the ledger and then
# replaced three tables (tasks, decisions, decision_relations) via
# DELETE + INSERT ... SELECT. On any database with a session bound to a task the
# DELETE aborted — `sessions.task_id` is write-once (ED-1560) and its FK is
# ON DELETE SET NULL, which is an implicit UPDATE. That abort was an ACCIDENT
# and it was the only thing standing between the command and four tables the
# copy-back never restores.
#
# After: --confirm is refused up front, before the projection is built and
# before any transaction opens, and the refusal counts what would have been lost
# from the live database. It does NOT repair the rebuild — that is E-799.
#
# Run from inside the worktree (esu puts you there):
#   esu && ./tests/tasks/e-2062-verify.sh
#
# What it proves:
#   1. FAIL-FAST unit gate: the Go suites this task owns pass. Everything below
#      is derived from the same binary, so a red gate makes it all noise.
#   2. --confirm refuses on a database with a bound session, names every count,
#      and leaves task_landings / session_gates / report_judgments /
#      report_labels / sessions.task_id byte-for-byte identical.
#   3. It refuses with NO bound session too — the case the accidental fuse
#      misses entirely, where the DELETE would have succeeded silently.
#   4. It refuses BEFORE reading the ledger: with no ledger at all the projector
#      would fail loudly, and that error never appears.
#   5. The dry run (no --confirm) is unchanged: it projects, prints counts, and
#      writes nothing.
#   6. The fuse is still declared in schema.sql — the write-once trigger and
#      every cascade edge the refusal counts — so a later sweep cannot "fix" the
#      abort by removing what the refusal is about.
#   7. WHY the guard is load-bearing, demonstrated rather than asserted: replay
#      rebuild-db's own DELETE by hand and watch it abort with a binding, then
#      watch it take four tables with it without one.
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

set -u

WT="$(git rev-parse --show-toplevel)" || { echo "SETUP ERROR: not in a git repo" >&2; exit 2; }
cd "${WT}" || exit 2

SCHEMA_SQL="${WT}/internal/schema/schema.sql"
GUARD_SRC="${WT}/internal/eventcmd/rebuild_guard.go"
EVENT_SRC="${WT}/internal/eventcmd/event.go"

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }

report_pass() {
    PASS_COUNT=$((PASS_COUNT + 1))
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
}

report_fail() {
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    [[ -n "${2:-}" ]] && printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    [[ -n "${3:-}" ]] && printf '      %sactual:  %s %s\n' "${DIM}" "${RESET}" "$3"
    return 0
}

setup_error() { printf '%sSETUP ERROR:%s %s\n' "${RED}${BOLD}" "${RESET}" "$1" >&2; exit 2; }

TMP_E2062=""
cleanup() { [[ -n "${TMP_E2062}" ]] && rm -rf "${TMP_E2062}"; }
trap cleanup EXIT

for f in "${SCHEMA_SQL}" "${GUARD_SRC}" "${EVENT_SRC}"; do
    [[ -f "${f}" ]] || setup_error "missing ${f}"
done
command -v sqlite3 >/dev/null || setup_error "sqlite3 is required"

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
section "1. Unit gate (fail-fast)"

# -count=1 on both: without it `go test` reported a stale PASS for this very
# package while the guard was patched out, which would make a fail-fast gate
# certify the exact regression it exists to catch.
if go test -count=1 ./internal/eventcmd/ -run 'TestEventRebuildDB' \
        >/tmp/e2062-go-eventcmd.log 2>&1; then
    report_pass "go test ./internal/eventcmd -run TestEventRebuildDB"
else
    report_fail "go test ./internal/eventcmd" "pass" \
        "failed — see /tmp/e2062-go-eventcmd.log"
    printf '\n%sFAIL-FAST: unit gate red; later assertions suppressed.%s\n' "${RED}" "${RESET}"
    exit 1
fi

if go test -count=1 ./internal/schema/ -run 'TestSchema_RebuildFuseIsStillDeclared' \
        >/tmp/e2062-go-schema.log 2>&1; then
    report_pass "go test ./internal/schema -run TestSchema_RebuildFuseIsStillDeclared"
else
    report_fail "go test ./internal/schema" "pass" \
        "failed — see /tmp/e2062-go-schema.log"
    printf '\n%sFAIL-FAST: unit gate red; later assertions suppressed.%s\n' "${RED}" "${RESET}"
    exit 1
fi

# ── setup: a binary built from THIS tree, and fixture helpers ───────────────
TMP_E2062=$(mktemp -d "${TMPDIR:-/tmp}/e2062.XXXXXX") || setup_error "mktemp failed"
BIN="${TMP_E2062}/endless-go"
if ! go build -o "${BIN}" ./cmd/endless-go >/tmp/e2062-build.log 2>&1; then
    setup_error "could not build endless-go (see /tmp/e2062-build.log)"
fi

# A throwaway config dir holding a schema-applied endless.db. Nothing here
# touches the real database: --config-dir is the E-1429 explicit DB context and
# it is the only DB this binary will open.
new_db() {
    local name="$1" cfg="${TMP_E2062}/$1"
    mkdir -p "${cfg}" || return 1
    sqlite3 "${cfg}/endless.db" < "${SCHEMA_SQL}" >/dev/null 2>&1 || return 1
    printf '%s' "${cfg}"
}

# One JSONL ledger segment holding a single task.created event, shaped exactly
# like a real one (fixed kairos ts; the projector does not require it to be
# fresh). Written by hand so the fixture needs no git repo and no emit path.
new_ledger() {
    local root="${TMP_E2062}/$1" project="$2" task_id="$3"
    mkdir -p "${root}/.endless/db-ledger" || return 1
    cat > "${root}/.endless/db-ledger/db-entries-a7f3-000001.jsonl" <<LEDGER
{"v":1,"ts":"5XSR24CP2QG00QF","kind":"task.created","project":"${project}","entity":{"type":"task","id":"${task_id}"},"actor":{"kind":"cli","id":"e2062-fixture"},"payload":{"title":"projected task","phase":"now","status":"ready","type":"todo","sort_order":10}}
LEDGER
    printf '%s' "${root}"
}

# The state a --confirm would destroy: a bound session, a landing, a gate on the
# task as its epic, and the gate's two children. The children are the point —
# they are reached on the SECOND cascade hop, through session_gates, so a guard
# that walked only the direct children of `tasks` would miss them.
seed_loss() {
    local db="$1" bind_session="$2"
    local task_id_val="NULL"
    [[ "${bind_session}" == "bound" ]] && task_id_val="500"
    sqlite3 "${db}" <<SEED
PRAGMA foreign_keys=ON;
INSERT INTO projects (id,name,path) VALUES (1,'e2062-proj','/tmp/e2062-proj');
INSERT INTO tasks (id,project_id,title,phase,status,type_id,sort_order)
    VALUES (500,1,'fixture task','now','ready',1,10);
INSERT INTO sessions (id,session_id,project_id,task_id)
    VALUES (1,'ES-E2062-1',1,${task_id_val});
INSERT INTO task_landings (id,task_id,session_id,merge_commit_sha)
    VALUES (1,500,1,'deadbeef');
INSERT INTO session_gates (id,session_id,kind_id,epic_id) VALUES (1,1,1,500);
INSERT INTO report_judgments (id,gate_id) VALUES (1,1);
INSERT INTO report_labels (id,gate_id,session_id,token) VALUES (1,1,1,'\$GOOD');
SEED
}

# Exactly the numbers the refusal claims are at risk, as one comparable blob.
loss_counts() {
    sqlite3 "$1" "
SELECT 'task_landings='    || (SELECT count(*) FROM task_landings)    || ' ' ||
       'session_gates='    || (SELECT count(*) FROM session_gates)    || ' ' ||
       'report_judgments=' || (SELECT count(*) FROM report_judgments) || ' ' ||
       'report_labels='    || (SELECT count(*) FROM report_labels)    || ' ' ||
       'bindings='         || (SELECT count(*) FROM sessions WHERE task_id IS NOT NULL) || ' ' ||
       'tasks='            || (SELECT count(*) FROM tasks);"
}

# ── 2. the refusal, with real loss to report ────────────────────────────────
section "2. \`--confirm\` refuses and destroys nothing"

cfg_bound=$(new_db bound) || setup_error "could not build the bound fixture DB"
db_bound="${cfg_bound}/endless.db"
seed_loss "${db_bound}" bound || setup_error "could not seed the bound fixture"
root_bound=$(new_ledger bound-root e2062-proj 501) \
    || setup_error "could not write the bound fixture ledger"

before=$(loss_counts "${db_bound}")

confirm_out=$("${BIN}" --config-dir "${cfg_bound}" event rebuild-db \
    --project-root "${root_bound}" --confirm 2>&1)
confirm_rc=$?

if (( confirm_rc != 0 )); then
    report_pass "exits non-zero (rc=${confirm_rc})"
else
    report_fail "exits non-zero" "a refusal" "exit 0"
fi

while IFS='|' read -r needle label; do
    if grep -qF -- "${needle}" <<<"${confirm_out}"; then
        report_pass "refusal ${label}"
    else
        report_fail "refusal ${label}" "'${needle}' in the output" "absent"
    fi
done <<'EOF'
rebuild-db --confirm is disabled|says it is disabled, not that it failed
1 task_landings rows|counts the landing history it would destroy
1 session_gates rows|counts the gates it would destroy
and 1 report_judgments, 1 report_labels|counts the SECOND-HOP loss through session_gates
1 sessions bindings|counts the bindings it would null
ED-1560|names the invariant nulling a binding violates
task_deps|names the table the rebuild never restores at all
E-1041|names the open task that makes the projection itself unreliable
E-799|names who owns the actual repair
Sequencing|routes to the order the repair has to happen in
EOF

# The guard runs before the projection: the dry run's banner must be absent.
if grep -qF -- "Projection:" <<<"${confirm_out}"; then
    report_fail "refuses before building the projection" \
        "no 'Projection:' line" "the projection was built first"
else
    report_pass "refuses before building the projection"
fi

after=$(loss_counts "${db_bound}")
if [[ "${before}" == "${after}" ]]; then
    report_pass "every at-risk count is unchanged: ${after}"
else
    report_fail "every at-risk count is unchanged" "${before}" "${after}"
fi

# The projected task (id 501) exists only in the ledger. If the guard ever
# leaked, the copy-back would have inserted it.
projected=$(sqlite3 "${db_bound}" "SELECT count(*) FROM tasks WHERE id = 501;")
if [[ "${projected}" == "0" ]]; then
    report_pass "the projected task was not inserted — the refusal wrote nothing"
else
    report_fail "the projected task was not inserted" "0 rows" "${projected} rows"
fi

bound_after=$(sqlite3 "${db_bound}" "SELECT ifnull(task_id,'NULL') FROM sessions WHERE id=1;")
if [[ "${bound_after}" == "500" ]]; then
    report_pass "sessions.task_id still 500 — ED-1560 says it is never cleared"
else
    report_fail "sessions.task_id survives" "500" "${bound_after}"
fi

# ── 3. no bound session — the case the accidental fuse misses ───────────────
section "3. \`--confirm\` refuses with NO bound session"

cfg_unbound=$(new_db unbound) || setup_error "could not build the unbound fixture DB"
db_unbound="${cfg_unbound}/endless.db"
seed_loss "${db_unbound}" unbound || setup_error "could not seed the unbound fixture"
root_unbound=$(new_ledger unbound-root e2062-proj 501) \
    || setup_error "could not write the unbound fixture ledger"

unbound_before=$(loss_counts "${db_unbound}")
unbound_out=$("${BIN}" --config-dir "${cfg_unbound}" event rebuild-db \
    --project-root "${root_unbound}" --confirm 2>&1)
unbound_rc=$?

if (( unbound_rc != 0 )); then
    report_pass "exits non-zero even with nothing for the trigger to catch"
else
    report_fail "exits non-zero with no binding" "a refusal" "exit 0"
fi

if grep -qF -- "rebuild-db --confirm is disabled" <<<"${unbound_out}"; then
    report_pass "the same refusal, not a trigger error"
else
    report_fail "the same refusal" "the disabled message" \
        "$(head -2 <<<"${unbound_out}")"
fi

if grep -qF -- "0 sessions bindings" <<<"${unbound_out}"; then
    report_pass "the binding count honestly reads 0"
else
    report_fail "the binding count reads 0" "'0 sessions bindings'" "absent"
fi

if grep -qF -- "1 task_landings rows" <<<"${unbound_out}"; then
    report_pass "the landing that would have been lost is still counted"
else
    report_fail "the landing is still counted" "'1 task_landings rows'" "absent"
fi

if [[ "${unbound_before}" == "$(loss_counts "${db_unbound}")" ]]; then
    report_pass "nothing written: ${unbound_before}"
else
    report_fail "nothing written" "${unbound_before}" "$(loss_counts "${db_unbound}")"
fi

# ── 4. the guard runs before anything else ──────────────────────────────────
section "4. \`--confirm\` refuses before reading the ledger"

cfg_noledger=$(new_db noledger) || setup_error "could not build the no-ledger fixture DB"
mkdir -p "${TMP_E2062}/noledger-root" || setup_error "mkdir failed"

noledger_out=$("${BIN}" --config-dir "${cfg_noledger}" event rebuild-db \
    --project-root "${TMP_E2062}/noledger-root" --confirm 2>&1)
noledger_rc=$?

if (( noledger_rc != 0 )) && grep -qF -- "rebuild-db --confirm is disabled" <<<"${noledger_out}"; then
    report_pass "refuses with no ledger present at all"
else
    report_fail "refuses with no ledger present" "the disabled message" \
        "rc=${noledger_rc}: $(head -2 <<<"${noledger_out}")"
fi

# Without the guard this exact invocation dies in the projector. Seeing that
# error would mean the guard runs too late to be worth anything.
if grep -qF -- "no events found" <<<"${noledger_out}"; then
    report_fail "the projector never ran" "no projector error" \
        "the projector ran first"
else
    report_pass "the projector never ran — the guard is genuinely first"
fi

# ── 5. the dry run, the useful half, untouched ──────────────────────────────
section "5. The dry run is unchanged"

cfg_dry=$(new_db dry) || setup_error "could not build the dry-run fixture DB"
db_dry="${cfg_dry}/endless.db"
seed_loss "${db_dry}" bound || setup_error "could not seed the dry-run fixture"
root_dry=$(new_ledger dry-root e2062-proj 501) \
    || setup_error "could not write the dry-run fixture ledger"

dry_before=$(loss_counts "${db_dry}")
dry_out=$("${BIN}" --config-dir "${cfg_dry}" event rebuild-db \
    --project-root "${root_dry}" 2>&1)
dry_rc=$?

if (( dry_rc == 0 )); then
    report_pass "the dry run still succeeds (rc=0)"
else
    report_fail "the dry run still succeeds" "exit 0" \
        "rc=${dry_rc}: $(head -3 <<<"${dry_out}")"
fi

if grep -qF -- "1 tasks created" <<<"${dry_out}"; then
    report_pass "it still builds the projection and reports its counts"
else
    report_fail "it builds the projection and reports counts" \
        "'1 tasks created'" "$(head -2 <<<"${dry_out}")"
fi

if grep -qF -- "Dry run" <<<"${dry_out}"; then
    report_pass "it still prints the dry-run banner"
else
    report_fail "it prints the dry-run banner" "'Dry run'" "absent"
fi

# The banner used to read "Use --confirm to replace the tasks table." A banner
# that still advertises a flag which refuses is a worse lie than no banner.
if grep -qF -- "Use --confirm to replace" <<<"${dry_out}"; then
    report_fail "the banner no longer advertises --confirm" \
        "no 'Use --confirm to replace'" "still advertised"
else
    report_pass "the banner no longer advertises --confirm as a thing that works"
fi

if [[ "${dry_before}" == "$(loss_counts "${db_dry}")" ]]; then
    report_pass "the dry run wrote nothing: ${dry_before}"
else
    report_fail "the dry run wrote nothing" "${dry_before}" "$(loss_counts "${db_dry}")"
fi

# ── 6. the fuse is still declared ───────────────────────────────────────────
# Asserted against a REAL database built from schema.sql, not by grepping the
# file: a FOREIGN KEY clause can be present in the text and absent from the
# table SQLite actually creates.
section "6. The fuse is still declared in schema.sql"

cfg_fuse=$(new_db fuse) || setup_error "could not build the fuse fixture DB"
db_fuse="${cfg_fuse}/endless.db"

trigger_sql=$(sqlite3 "${db_fuse}" \
    "SELECT sql FROM sqlite_master WHERE type='trigger' AND name='sessions_task_id_write_once';")
if grep -qF -- "UPDATE OF task_id ON sessions" <<<"${trigger_sql}"; then
    report_pass "sessions_task_id_write_once still fires on UPDATE OF task_id"
else
    report_fail "sessions_task_id_write_once still fires on UPDATE OF task_id" \
        "the trigger" "${trigger_sql:-missing}"
fi

while IFS='|' read -r table column ref want why; do
    got=$(sqlite3 "${db_fuse}" \
        "SELECT \"on_delete\" FROM pragma_foreign_key_list('${table}')
          WHERE \"from\"='${column}' AND \"table\"='${ref}';")
    if [[ "${got}" == "${want}" ]]; then
        report_pass "${table}.${column} -> ${ref} ON DELETE ${want} (${why})"
    else
        report_fail "${table}.${column} -> ${ref} ON DELETE ${want} (${why})" \
            "${want}" "${got:-no such FK}"
    fi
done <<'EOF'
sessions|task_id|tasks|SET NULL|the implicit UPDATE that trips the trigger
task_landings|task_id|tasks|CASCADE|landing history, never restored
session_gates|epic_id|tasks|CASCADE|gates, never restored
report_judgments|gate_id|session_gates|CASCADE|second hop
report_labels|gate_id|session_gates|CASCADE|second hop
EOF

# ── 7. why the guard is load-bearing, demonstrated ──────────────────────────
# Everything above tests the refusal. This tests the CLAIM behind it, by
# running rebuild-db's own DELETE statement by hand against throwaway copies.
section "7. What the refusal is protecting (rebuild-db's own DELETE, by hand)"

REBUILD_DELETE="DELETE FROM tasks WHERE project_id IN (SELECT id FROM projects WHERE name IN ('e2062-proj'));"

cfg_demo_bound=$(new_db demo-bound) || setup_error "could not build the demo DB"
db_demo_bound="${cfg_demo_bound}/endless.db"
seed_loss "${db_demo_bound}" bound || setup_error "could not seed the demo fixture"

demo_out=$(sqlite3 "${db_demo_bound}" "PRAGMA foreign_keys=ON; ${REBUILD_DELETE}" 2>&1)
demo_rc=$?
if (( demo_rc != 0 )) && grep -qF -- "sessions.task_id is write-once" <<<"${demo_out}"; then
    report_pass "with a binding, the DELETE aborts — the accidental fuse, reproduced"
else
    report_fail "with a binding, the DELETE aborts" \
        "'sessions.task_id is write-once'" "rc=${demo_rc}: ${demo_out:-succeeded}"
fi

cfg_demo_unbound=$(new_db demo-unbound) || setup_error "could not build the demo DB"
db_demo_unbound="${cfg_demo_unbound}/endless.db"
seed_loss "${db_demo_unbound}" unbound || setup_error "could not seed the demo fixture"

sqlite3 "${db_demo_unbound}" "PRAGMA foreign_keys=ON; ${REBUILD_DELETE}" >/dev/null 2>&1 \
    || setup_error "the unbound DELETE should have succeeded"

demo_after=$(loss_counts "${db_demo_unbound}")
want_after="task_landings=0 session_gates=0 report_judgments=0 report_labels=0 bindings=0 tasks=0"
if [[ "${demo_after}" == "${want_after}" ]]; then
    report_pass "without a binding it succeeds and takes all four tables with it"
else
    report_fail "without a binding it takes all four tables with it" \
        "${want_after}" "${demo_after}"
fi

# The copy-back that follows restores only three tables. Nothing in it would put
# any of the above back — which is the whole argument for refusing.
copyback=$(grep -c 'INSERT INTO \(tasks\|decisions\|decision_relations\) SELECT \* FROM proj\.' \
    "${EVENT_SRC}")
if [[ "${copyback}" == "3" ]]; then
    report_pass "the copy-back is still exactly 3 tables, restoring none of them"
else
    report_fail "the copy-back is still exactly 3 tables" "3 INSERT ... SELECT statements" \
        "${copyback}"
fi

# ── summary ─────────────────────────────────────────────────────────────────
section "Summary"
printf '  %s%d passed%s, %s%d failed%s\n' \
    "${GREEN}" "${PASS_COUNT}" "${RESET}" \
    "$([[ ${FAIL_COUNT} -gt 0 ]] && printf '%s' "${RED}")" "${FAIL_COUNT}" "${RESET}"

if (( FAIL_COUNT > 0 )); then
    printf '\n  Failed:\n'
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    exit 1
fi
exit 0
