#!/usr/bin/env bash
#
# E-1898 verification — liveness is OBSERVED, never inferred; the dead-pane
# reaper is gone.
#
# Run from anywhere inside the worktree:
#   esu
#   ./tests/tasks/e-1898-verify.sh
#
# WHAT LANDED
#   `monitor.ReapDeadTmuxPanes` looked at whatever tmux server $TMUX happened to
#   name, decided which sessions were dead, and WROTE that conclusion:
#       UPDATE sessions SET state='ended', process=NULL, ... WHERE ...
#   On 2026-08-05 it looked at a one-pane server while the real one was alive and
#   destroyed 59 of 61 live pane bindings. The rows then read alive with no pane,
#   permanently, and every tmux status line went blank.
#
#   The fix is not a better-guarded sweep. There is no sweep.
#
#     identity   `processes` rows keyed (kind, server_uuid, address). A tmux pane
#                id alone is reused by the next server; the PAIR is not. sessions
#                point at one via process_id. Rows are facts and are never
#                deleted or rewritten by an observation.
#     observation a per-invocation TEMP snapshot (live_processes) plus the set of
#                servers actually reached (observed_servers).
#     liveness   a JOIN of the two, exposed as the session_liveness view:
#                  live     address present on a server we reached
#                  dead     we reached its server; address absent
#                  unknown  we did NOT reach its server -- no opinion
#                  unbound  no binding at all (background agents)
#
#   Deleted: the reaper, `endless tmux reset` (Go verb + Python wrapper + the
#   remedy line that advertised it), `session-query reap-dead-panes`,
#   TouchSession's collision invalidation, E-1530's two
#   `sessions_null_process_on_end_*` triggers, and `runInit`'s reset call — the
#   documented trigger for the incident's 37-row batch.
#
# THE INVARIANTS (this suite exists to check these, not the plumbing)
#   I1  No code path writes 'ended' or clears a binding based on an OBSERVATION
#       of tmux. Only reported facts (SessionEnd) and explicit operator action.
#   I2  A failed or incomplete observation affects at most the rows it observed.
#       `unknown` is NEVER converted to `dead` by any consumer.
#   I3  Identity is unique across tmux server lifetimes: a reissued pane id
#       cannot resolve to a binding made against the previous server.
#   I4  The liveness tests need no tmux server. Liveness is a JOIN over tables,
#       so its truth table is enumerated as fixtures.
#
# ISOLATION
#   Layers A-C touch NO tmux server and NO real database: the Go tests pin the
#   tmux seam (monitor.SetTestTmuxObservation) and run against throwaway DBs.
#   Layer D drives the built binary against a `mktemp -d` --config-dir.
#   Layer F re-counts the REAL ledger before and after to prove it was untouched.
#
# Layers:
#   A. FAIL-FAST unit — the liveness truth table, the four states, and identity.
#      If these are broken, nothing below is worth running.
#   B. Invariants as tests — I1 and I2 driven against the worst possible
#      observation (an observer that sees nothing), asserting ZERO rows change.
#   C. Removal — the reaper and every verb/wrapper that fronted it are gone, and
#      the destructive triggers are absent from a freshly created schema.
#   D. E2E — a real binary against a real (throwaway) DB: schema shape, and the
#      status line recording a fault instead of blanking silently.
#   E0. The migration — the change file against a synthetic pre-E-1898 database.
#      Nothing else exercises it, and it runs exactly once, on the real ledger.
#   E. Project-wide regression — go build, go vet, go test ./..., just test.
#   F. Ledger safety — the real DB's bound-session count is unchanged.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 setup error.

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

WT=""         # worktree root
BIN=""        # the worktree's endless-go (rebuilt in layer A)
TMPDIR_D=""   # layer D scratch (throwaway --config-dir)
REAL_DB=""    # the user's real ledger, for the layer F guard
LEDGER_BEFORE=""

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'
    BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
note()    { printf '  %s%s%s\n' "${DIM}" "$1" "${RESET}"; }

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

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" \
        "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

assert_succeeds() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | $(printf '%s' "${output}" | tail -20)"
}

assert_fails() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit != 0" "exit=0 | $(printf '%s' "${output}" | tail -5)"
}

assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"
    else report_fail "$1" "$2" "$3"; fi
}

assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "contains: $2" "$(printf '%s' "$3" | head -5)"; fi
}

assert_absent() {
    # assert_absent DESC PATTERN -- files/dirs to grep
    local desc="$1" pattern="$2"; shift 2
    local hits
    hits=$(grep -rn --include="*.go" --include="*.py" -- "${pattern}" "$@" 2>/dev/null \
             | grep -v '/vendor/' \
             | grep -v '^[^:]*:[0-9]*:\s*//' \
             | grep -v '^[^:]*:[0-9]*:\s*#' \
             | grep -v 'internal/schema/changes/' \
             | grep -v 'tests/tasks/')
    if [[ -z "${hits}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "no live references to ${pattern}" "$(printf '%s' "${hits}" | head -5)"
}

# ─── setup / teardown ───────────────────────────────────────────────────────

cleanup() {
    [[ -n "${TMPDIR_D}" ]] && rm -rf "${TMPDIR_D}"
    return 0
}

# ─── layer A — liveness truth table (FAIL-FAST) ─────────────────────────────

run_unit_layer() {
    section "A — liveness truth table + identity (FAIL-FAST)"
    note "no tmux server is involved in any of this (invariant I4); the tmux"
    note "seam is pinned so the suite cannot touch your real server"

    local before="${FAIL_COUNT}"

    assert_succeeds "just go (builds bin/endless-go)" just go

    assert_succeeds "liveness: four-state truth table over seeded observations" \
        go test ./internal/monitor/ -count=1 -run 'TestLiveness_TruthTable'
    assert_succeeds "liveness: a background agent is 'unbound', never 'dead'" \
        go test ./internal/monitor/ -count=1 -run 'TestLiveness_BackgroundAgentIsUnboundNotDead'
    assert_succeeds "liveness: a shell in the pane (Ctrl+Z) stays 'live'" \
        go test ./internal/monitor/ -count=1 -run 'TestLiveness_ShellPaneStaysLive'
    # Regression, 2026-08-13: a LINKED tmux window's panes are listed once per
    # session the window is linked into, and the observation table had no
    # uniqueness constraint, so session_liveness's LEFT JOIN multiplied every
    # session row. `esu` could not resolve a sibling pane at all.
    assert_succeeds "liveness: a pane observed twice still yields ONE session row" \
        go test ./internal/monitor/ -count=1 -run 'TestLiveness_DuplicatePaneYieldsOneRow'
    assert_succeeds "liveness: a server observed twice still yields ONE session row" \
        go test ./internal/monitor/ -count=1 -run 'TestLiveness_DuplicateServerYieldsOneRow'
    assert_succeeds "liveness: the snapshot parser keeps command-less panes present" \
        go test ./internal/monitor/ -count=1 -run 'TestParsePaneList'
    assert_succeeds "shell predicate: true for zsh/bash/sh/fish, FALSE for '2.1.220'" \
        go test ./internal/monitor/ -count=1 -run 'TestIsShellCommand'

    # I3 — the structural fix. Everything else is downstream of this.
    assert_succeeds "identity: one pane id on two servers is two identities (I3)" \
        go test ./internal/monitor/ -count=1 -run 'TestEnsureProcess_IdentityIsServerScoped'
    assert_succeeds "identity: a tmux binding without a server_uuid is refused" \
        go test ./internal/monitor/ -count=1 -run 'TestEnsureProcess_RefusesTmuxBindingWithoutServer'

    # The 2026-08-10 regression: the migration left every pre-existing binding
    # server-less, so nothing resolved until each session next fired a hook —
    # which idle windows never do. Adoption attributes what is observable.
    assert_succeeds "migration adoption: live panes are attributed to their server" \
        go test ./internal/monitor/ -count=1 -run 'TestAdoptPaneBindings_AttributesLivePanes'
    assert_succeeds "migration adoption: an address claimed twice is refused" \
        go test ./internal/monitor/ -count=1 -run 'TestAdoptPaneBindings_RefusesAmbiguousAddress'
    assert_succeeds "migration adoption: the shared history row is never stamped" \
        go test ./internal/monitor/ -count=1 -run 'TestAdoptPaneBindings_PreservesSharedHistoryRow'
    assert_succeeds "migration adoption: no reachable tmux is a clean no-op" \
        go test ./internal/monitor/ -count=1 -run 'TestAdoptPaneBindings_NoServerIsNoOp'
    assert_succeeds "identity: a reissued pane cannot resolve to the old server's session" \
        go test ./internal/monitor/ -count=1 \
            -run 'TestGetActiveTaskForPane_ReusedPaneOnNewServerIsDifferentIdentity'
    assert_succeeds "processkind: enum/table integrity and legacy-shape parsing" \
        go test ./internal/processkind/ -count=1

    if [[ "${FAIL_COUNT}" -ne "${before}" ]]; then
        printf '\n  %sFail-fast: the liveness contract is broken; skipping the rest.%s\n' \
            "${RED}" "${RESET}"
        summary
        exit 1
    fi
}

# ─── layer B — the invariants ───────────────────────────────────────────────

run_invariant_layer() {
    section "B — invariants I1 (no inferred write) and I2 (bounded blast radius)"
    note "these drive the EXACT 2026-08-05 shape: an observer that sees nothing,"
    note "against a project full of bound sessions"

    # I2 + I1 together: nothing reads dead, and nothing is written.
    assert_succeeds "I2: an unreachable tmux condemns nothing, and writes nothing" \
        go test ./internal/monitor/ -count=1 -run 'TestLiveness_UnreachableTmuxCondemnsNothing'
    assert_succeeds "I2: no tmux binary at all means 'unknown', not 'dead'" \
        go test ./internal/monitor/ -count=1 -run 'TestLiveness_NoTmuxBinaryIsUnknownNotDead'

    # The consumer that can actually lose work.
    assert_succeeds "I2: an owner we cannot reach KEEPS its task (spawn/claim guard)" \
        go test ./internal/monitor/ -count=1 -run 'TestListLiveSessions_KeepsUnknownOwners'
    assert_succeeds "I2: a background agent keeps its task (no pane != no owner)" \
        go test ./internal/monitor/ -count=1 -run 'TestListLiveSessions_KeepsBackgroundAgents'
    assert_succeeds "I1: an observably-dead ghost drops from the READ, unrewritten" \
        go test ./internal/monitor/ -count=1 -run 'TestListLiveSessions_OmitsObservablyDead'
    assert_succeeds "I2: the Python ownership guard refuses on 'unknown'" \
        uv run pytest tests/test_check_task_ownership.py -q

    # I1 at the write paths that used to destroy bindings.
    assert_succeeds "I1: ending a session PRESERVES its binding (history is a fact)" \
        go test ./internal/monitor/ -count=1 -run 'TestEndSession_PreservesBinding'
    assert_succeeds "I1: a same-server collision leaves the prior occupant alone" \
        go test ./internal/monitor/ -count=1 \
            -run 'TestTouchSession_SameServerCollisionLeavesPriorRowAlone'
    assert_succeeds "the E-1530 family still holds (ended rows never win a lookup)" \
        go test ./internal/monitor/ -count=1 -run 'TestGetActiveTaskForPane'

    # The TEMP-table assumption. A silent regression here breaks liveness
    # intermittently, which is the hardest kind of failure to attribute.
    assert_contains "monitor.DB pins the pool at one conn (TEMP tables depend on it)" \
        "SetMaxOpenConns(1)" "$(cat "${WT}/internal/monitor/db.go")"
}

# ─── layer C — removal ──────────────────────────────────────────────────────

run_removal_layer() {
    section "C — the reaper and its whole surface are gone"
    note "a guarded reaper would still be a reaper; these assert deletion"

    assert_eq "internal/monitor/reap.go no longer exists" "absent" \
        "$([[ -f "${WT}/internal/monitor/reap.go" ]] && echo present || echo absent)"
    assert_eq "internal/tmuxcmd/reset.go no longer exists" "absent" \
        "$([[ -f "${WT}/internal/tmuxcmd/reset.go" ]] && echo present || echo absent)"

    assert_absent "no live references to ReapDeadTmuxPanes" \
        "ReapDeadTmuxPanes" "${WT}/internal" "${WT}/cmd" "${WT}/src"
    assert_absent "no live references to reap-dead-panes / _reap_dead_panes" \
        "reap.dead.panes" "${WT}/internal" "${WT}/cmd" "${WT}/src"
    assert_absent "no live references to the removed \`tmux reset\` verb" \
        "tmux reset" "${WT}/internal" "${WT}/cmd" "${WT}/src"

    # `runInit` calling `runReset` is the documented trigger for the 37-row batch.
    assert_absent "runInit no longer invokes a reset/reap" \
        "runReset" "${WT}/internal"

    # The destructive triggers must be absent from a FRESHLY created schema.
    # Matched on the CREATE, not the name: schema.sql deliberately still MENTIONS
    # them, in the comment explaining why they were removed and why re-adding one
    # would now destroy history rather than protect a lookup.
    local trig
    trig=$(grep -c "CREATE TRIGGER.*sessions_null_process" "${WT}/internal/schema/schema.sql" 2>/dev/null | head -1)
    assert_eq "schema.sql creates no process-nulling triggers" "0" "${trig:-0}"

    # ...and the migration must DROP them from existing databases.
    assert_contains "the change file drops them from existing DBs" \
        "DROP TRIGGER IF EXISTS sessions_null_process_on_end_update" \
        "$(cat "${WT}/internal/schema/changes/e-1898-processes-identity.go")"
    assert_contains "the change file drops sessions.process" \
        "ALTER TABLE sessions DROP COLUMN process" \
        "$(cat "${WT}/internal/schema/changes/e-1898-processes-identity.go")"
}

# ─── layer D — E2E against the built binary ─────────────────────────────────

run_e2e_layer() {
    section "D — E2E: the real binary against a throwaway database"
    note "a fresh --config-dir DB; the real ledger is never opened"

    TMPDIR_D=$(mktemp -d) || { report_fail "layer D scratch dir" "mktemp -d ok" "failed"; return; }

    "${BIN}" --config-dir "${TMPDIR_D}" session-status --task 999999 >/dev/null 2>&1
    if [[ ! -f "${TMPDIR_D}/endless.db" ]]; then
        report_fail "throwaway DB is created" "endless.db in --config-dir" "missing"
        return
    fi
    report_pass "throwaway DB is created"

    local cols
    cols=$(sqlite3 "${TMPDIR_D}/endless.db" \
             "SELECT name FROM pragma_table_info('sessions') WHERE name LIKE 'process%'" 2>/dev/null)
    assert_eq "sessions carries process_id and no longer carries process" "process_id" "${cols}"

    assert_contains "processes table exists with the identity columns" "server_uuid" \
        "$(sqlite3 "${TMPDIR_D}/endless.db" ".schema processes" 2>/dev/null)"

    # The identity constraint must use ifnull(): a plain UNIQUE does not bind
    # when server_uuid is NULL, which is exactly the migration-backfill and
    # kind=pid cases.
    assert_contains "the identity index collapses NULL server_uuid via ifnull()" \
        "ifnull(server_uuid, '')" \
        "$(sqlite3 "${TMPDIR_D}/endless.db" ".schema processes" 2>/dev/null)"

    local trigs
    trigs=$(sqlite3 "${TMPDIR_D}/endless.db" \
              "SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'sessions_null_process%'" 2>/dev/null)
    assert_eq "a fresh DB has no process-nulling triggers" "0" "${trigs:-x}"

    assert_eq "process_kinds is seeded from the Go enum" "pid|tmux" \
        "$(sqlite3 "${TMPDIR_D}/endless.db" "SELECT group_concat(slug, '|') FROM (SELECT slug FROM process_kinds ORDER BY slug)" 2>/dev/null)"

    # `tmux reset` must be gone from the CLI surface, both languages.
    assert_fails "\`endless-go tmux reset\` is no longer a verb" \
        "${BIN}" --config-dir "${TMPDIR_D}" tmux reset
    local usage
    usage=$("${BIN}" --config-dir "${TMPDIR_D}" tmux --help 2>&1)
    if [[ "${usage}" != *"reset"* ]]; then
        report_pass "\`tmux --help\` no longer advertises reset"
    else
        report_fail "\`tmux --help\` no longer advertises reset" "no 'reset' line" "${usage}"
    fi

    # D8 (absorbing E-1895): the status line must RECORD rather than silently
    # render a placeholder. Point it at a config dir whose DB is a corrupt file
    # so monitor.DB()'s gates fail, then read the fault back.
    local faultdir="${TMPDIR_D}/faulty"
    mkdir -p "${faultdir}"
    printf 'this is not a sqlite database' > "${faultdir}/endless.db"
    local bar
    bar=$("${BIN}" --config-dir "${faultdir}" tmux status-line --pane '%1' 2>/dev/null)
    if [[ -n "${bar}" ]]; then
        report_pass "the bar still renders (never a non-zero exit into tmux)"
    else
        report_fail "the bar still renders" "non-empty output" "empty"
    fi
    # ERR-0008 must be in the catalog and documented; the fault ITSELF cannot be
    # recorded when the DB is the thing that failed (faults.Record's accessor is
    # monitor.DB, and it swallows its own failures by contract) — that coverage
    # limit is stated in docs/errors.md rather than pretended away here.
    assert_contains "ERR-0008 status-line-unavailable is in the catalog" \
        "ERR-0008" "$("${BIN}" --config-dir "${TMPDIR_D}" errors codes 2>&1)"
}

# ─── layer E0 — the migration ───────────────────────────────────────────────

# The change file runs ONCE, at land time, against the real populated ledger.
# Nothing else in this suite exercises it — the schema.sql path builds the
# post-migration shape directly — so without this layer the single riskiest
# statement in the change (ALTER TABLE sessions DROP COLUMN process, against
# real data) would first execute on the user's own database.
run_migration_layer() {
    section "E0 — the migration, against a synthetic pre-E-1898 database"
    note "builds a literal pre-E-1898 fixture DB, seeds the shapes that matter,"
    note "applies the change file, and checks what it did with each of them"

    local mdir mdb
    mdir=$(mktemp -d) || { report_fail "migration scratch dir" "mktemp -d ok" "failed"; return; }
    mdb="${mdir}/old.db"

    # The fixture is written out HERE rather than read from git history.
    #
    # It used to be `git show HEAD:internal/schema/schema.sql`, which worked
    # exactly until this work was committed — after that HEAD *is* the
    # post-E-1898 schema, so the "pre-change" database came up already migrated
    # and every assertion below collapsed (`duplicate column name: process_id`).
    # Any git-relative reference has the same defect on a different day:
    # merge-base breaks once the branch lands.
    #
    # So the pre-change shape is stated literally. It is the subset the change
    # file actually touches — `sessions.process` plus E-1530's two nulling
    # triggers — which is also a readable statement of what is being migrated
    # away from.
    sqlite3 "${mdb}" >/dev/null 2>&1 <<'SQL'
CREATE TABLE projects (
    id   INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    path TEXT NOT NULL
);
CREATE TABLE sessions (
    id             INTEGER PRIMARY KEY,
    session_id     TEXT,
    project_id     INTEGER,
    platform       TEXT NOT NULL DEFAULT 'claude',
    state          TEXT NOT NULL DEFAULT 'working',
    active_task_id INTEGER,
    kind_id        INTEGER NOT NULL DEFAULT 1,
    process        TEXT,
    started_at     TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    last_activity  TEXT,
    UNIQUE (session_id)
);
CREATE TRIGGER sessions_null_process_on_end_update
AFTER UPDATE OF state ON sessions
WHEN NEW.state = 'ended' AND NEW.process IS NOT NULL
BEGIN
    UPDATE sessions SET process = NULL WHERE id = NEW.id;
END;
CREATE TRIGGER sessions_null_process_on_end_insert
AFTER INSERT ON sessions
WHEN NEW.state = 'ended' AND NEW.process IS NOT NULL
BEGIN
    UPDATE sessions SET process = NULL WHERE id = NEW.id;
END;
SQL
    if [[ ! -s "${mdb}" ]]; then
        report_fail "build a pre-E-1898 database" "fixture schema applies" "failed"
        rm -rf "${mdir}"; return
    fi
    # Prove the fixture is genuinely pre-migration before trusting anything it
    # reports — a fixture that came up already migrated is how this layer
    # silently stopped testing the migration in the first place.
    assert_eq "the fixture DB is pre-migration (has sessions.process)" "1" \
        "$(sqlite3 "${mdb}" "SELECT count(*) FROM pragma_table_info('sessions') WHERE name='process'" 2>/dev/null)"
    assert_eq "the fixture DB is pre-migration (no process_id yet)" "0" \
        "$(sqlite3 "${mdb}" "SELECT count(*) FROM pragma_table_info('sessions') WHERE name='process_id'" 2>/dev/null)"

    # The shapes that decide whether the backfill is right:
    #   two sessions on ONE pane   -> must collapse to ONE identity
    #   pid:1234                   -> kind=pid, address stripped of the prefix
    #   NULL / unparsable          -> left UNBOUND, never guessed at
    sqlite3 "${mdb}" >/dev/null 2>&1 <<'SQL'
INSERT INTO projects (id,name,path) VALUES (1,'acme','/tmp/acme');
INSERT INTO sessions (session_id,project_id,platform,state,process,last_activity) VALUES
 ('s-live-1',1,'claude','working','%999901','2026-08-09T00:00:00'),
 ('s-live-2',1,'claude','needs_input','%999902','2026-08-09T00:00:00'),
 ('s-dup',   1,'claude','idle','%999901','2026-08-09T00:00:00'),
 ('s-pid',   1,'claude','working','pid:1234','2026-08-09T00:00:00'),
 ('s-none',  1,'claude','working',NULL,'2026-08-09T00:00:00'),
 ('s-weird', 1,'claude','working','garbage','2026-08-09T00:00:00');
SQL

    assert_eq "the pre-change DB really has the old triggers" "2" \
        "$(sqlite3 "${mdb}" "SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'sessions_null%'" 2>/dev/null)"

    assert_succeeds "the change file applies cleanly" \
        env ENDLESS_CHANGE_DB="${mdb}" go run ./internal/schema/changes/e-1898-processes-identity.go

    # Two sessions, one pane, one identity. If the ifnull() index were a plain
    # UNIQUE this would be 2 rows and every NULL-server binding would duplicate.
    assert_eq "two sessions sharing a pane collapse to ONE processes row" "3" \
        "$(sqlite3 "${mdb}" "SELECT count(*) FROM processes" 2>/dev/null)"
    assert_eq "both sessions on %414 point at the same identity" "1" \
        "$(sqlite3 "${mdb}" "SELECT count(DISTINCT process_id) FROM sessions WHERE session_id IN ('s-live-1','s-dup')" 2>/dev/null)"

    assert_eq "a tmux binding backfills as kind=tmux with the pane as address" "tmux|%999902" \
        "$(sqlite3 "${mdb}" "SELECT pk.slug || '|' || p.address FROM sessions s JOIN processes p ON p.id=s.process_id JOIN process_kinds pk ON pk.id=p.kind_id WHERE s.session_id='s-live-2'" 2>/dev/null)"
    assert_eq "a pid: binding backfills as kind=pid with the prefix stripped" "pid|1234" \
        "$(sqlite3 "${mdb}" "SELECT pk.slug || '|' || p.address FROM sessions s JOIN processes p ON p.id=s.process_id JOIN process_kinds pk ON pk.id=p.kind_id WHERE s.session_id='s-pid'" 2>/dev/null)"

    # Backfilled bindings carry no server UNLESS their pane is live on the
    # running server, in which case step two adopts them (monitor.AdoptPaneBindings,
    # unit-tested exhaustively in internal/monitor). The fixture deliberately uses
    # %9999xx addresses that cannot be real panes, so adoption is a no-op here and
    # these assertions stay deterministic on any machine, with or without tmux.
    assert_eq "backfilled bindings carry no server_uuid (it is not knowable)" "0" \
        "$(sqlite3 "${mdb}" "SELECT count(*) FROM processes WHERE server_uuid IS NOT NULL" 2>/dev/null)"

    # The refusal to guess. An unparsable value must NOT mint an identity that
    # could never match an observation.
    assert_eq "NULL and unparsable process values are left UNBOUND, not guessed" "2" \
        "$(sqlite3 "${mdb}" "SELECT count(*) FROM sessions WHERE process_id IS NULL" 2>/dev/null)"

    assert_eq "no session row was ended or otherwise touched by the migration" "0" \
        "$(sqlite3 "${mdb}" "SELECT count(*) FROM sessions WHERE state = 'ended'" 2>/dev/null)"

    assert_eq "the destructive triggers are dropped" "0" \
        "$(sqlite3 "${mdb}" "SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'sessions_null%'" 2>/dev/null)"
    assert_eq "the old sessions.process column is dropped" "0" \
        "$(sqlite3 "${mdb}" "SELECT count(*) FROM pragma_table_info('sessions') WHERE name='process'" 2>/dev/null)"

    # Re-running must be a recorded no-op, not a second backfill.
    local rerun
    rerun=$(ENDLESS_CHANGE_DB="${mdb}" go run ./internal/schema/changes/e-1898-processes-identity.go 2>&1)
    assert_contains "re-applying is a recorded no-op" "already applied" "${rerun}"
    assert_eq "re-applying did not duplicate any identity" "3" \
        "$(sqlite3 "${mdb}" "SELECT count(*) FROM processes" 2>/dev/null)"

    rm -rf "${mdir}"
}

# ─── layer E — project-wide regression ──────────────────────────────────────

run_regression_layer() {
    section "E — project-wide regression"
    note "nothing in E-1898 may regress the build, the vet, or either suite"
    assert_succeeds "go build ./..." go build ./...
    assert_succeeds "go vet ./..." go vet ./...
    assert_succeeds "go test ./..." go test ./...
    assert_succeeds "just test (full Python suite)" just test
}

# ─── layer F — ledger safety ────────────────────────────────────────────────

count_real_bindings() {
    [[ -f "${REAL_DB}" ]] || { echo "no-db"; return; }
    sqlite3 "${REAL_DB}" \
        "SELECT count(*) FROM sessions WHERE state != 'ended' AND process_id IS NOT NULL" \
        2>/dev/null || sqlite3 "${REAL_DB}" \
        "SELECT count(*) FROM sessions WHERE state != 'ended' AND process IS NOT NULL" \
        2>/dev/null || echo "unreadable"
}

run_ledger_guard() {
    section "F — ledger safety"
    note "this suite exercises the code path that destroyed 59 live bindings;"
    note "the count of bound, non-ended sessions in the REAL ledger must not move"
    assert_eq "real ledger's bound-session count is unchanged" \
        "${LEDGER_BEFORE}" "$(count_real_bindings)"

    # A count alone would not have caught the real hazard. The tmux/hook/channel
    # subcommands call PinMainDB, so ANY invocation of the worktree binary that
    # omits --config-dir opens the REAL ledger and applies this branch's
    # schema.sql to it — creating tables there that main's code has never heard
    # of. That happened for real on 2026-08-10 (an ad-hoc diagnostic, not this
    # suite), and a bound-session count would have read clean straight through
    # it. This greps the suite's own source so the rule is enforced, not trusted.
    local unguarded
    # The `grep -v e-1898-verify.sh` drops this check's OWN grep line, which
    # necessarily contains the pattern it searches for. Without it the tripwire
    # reports itself forever, which is worse than not having one — a check that
    # always fails gets ignored, and then stops checking anything.
    unguarded=$(grep -n '\${BIN}"' "${WT}/tests/tasks/e-1898-verify.sh" \
                  | grep -v -- '--config-dir' \
                  | grep -v 'e-1898-verify\.sh' \
                  | grep -v '^[0-9]*: *#' || true)
    if [[ -z "${unguarded}" ]]; then
        report_pass "every worktree-binary call in this suite passes --config-dir"
    else
        report_fail "every worktree-binary call in this suite passes --config-dir" \
            "no unguarded \${BIN} invocations" "$(printf '%s' "${unguarded}" | head -3)"
    fi
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    WT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${WT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    cd "${WT}" || exit 2

    local tool
    for tool in just go uv sqlite3; do
        command -v "${tool}" >/dev/null 2>&1 || {
            printf 'ERROR: %s not on PATH\n' "${tool}" >&2; exit 2; }
    done

    BIN="${WT}/bin/endless-go"
    REAL_DB="${HOME}/.config/endless/endless.db"
    trap cleanup EXIT

    # Read the guard's baseline BEFORE anything runs.
    LEDGER_BEFORE=$(count_real_bindings)

    printf '%sE-1898 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"
    printf '  tmux:     none started; the observation seam is pinned in tests\n'
    printf '  db:       throwaway --config-dir; real ledger read-only (%s bound rows)\n' \
        "${LEDGER_BEFORE}"

    run_unit_layer
    run_invariant_layer
    run_removal_layer
    run_e2e_layer
    run_migration_layer
    run_regression_layer
    run_ledger_guard

    summary
}

main "$@"
