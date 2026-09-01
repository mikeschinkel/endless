#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2074 and records what was true when E-2074
# landed. Edit it only if you ARE E-2074. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2074 verification — background-agent support is gone from Endless, and the
# four `sessions` columns that outlived their reasons went with it.
#
# WHAT WAS REMOVED, and why each thing earned it:
#
#   Background agents.  `endless task spawn --bg` dispatched a headless agent
#   under Anthropic's supervisor; `task spawn --attach`, `task attach` and
#   `endless agents` were how you found and watched one. They never became as
#   reliable as a tmux-hosted session — E-1695 routed the epic handoff template
#   away from them on 2026-06-30 as a stated TEMPORARY measure, and nothing
#   reversed it in the two months since. The decision is to delete the code
#   rather than keep carrying it.
#
#   sessions.kind_id + the session_kinds table + internal/sessionkind.  The
#   tmux/background discriminator. With one kind left there is nothing to
#   discriminate.
#
#   sessions.short_id.  The `claude --bg` dispatch handle. Written only by the
#   dispatch path, read only by the attach paths.
#
#   sessions.summary.  A 200-character slice of the first assistant response.
#   E-1925 replaces it with an on-demand recap. NOTE the auto-hide it also
#   performed — a session whose first response is a "Not logged in"/"Error:"
#   greeting is hidden from the listings — is NOT part of the removal; it
#   survives as monitor.hideIfErrorGreeting. Check 6 is that guarantee.
#
#   sessions.plan_file_path.  Written at PostToolUse/Write, read only by
#   ExitPlanMode, which already carried an mtime scan of ~/.claude/plans as the
#   fallback for every session that reached it without writing through the Write
#   tool. E-1338, which proposed storing a home-relative prefix in it, is
#   declined.
#
# Run from anywhere inside the worktree:
#   esu
#   endless task verify E-2074
#
# Requires `just build` first — checks 4/5 drive the CANDIDATE bin/endless-go,
# not the global install. (Setup builds it if it is missing.)
#
# What it checks:
#   0. Fail-fast fold-in regression: `go build ./...`, the whole Go test suite
#      for the packages this touched, the Python suite, and `just guide-check`.
#      A failure here short-circuits the rest — the remaining checks would be
#      reporting on rubble.
#   1. No LIVE reference survives anywhere in the source tree.
#   2. The dead files and packages are deleted, not merely unreferenced.
#   3. schema.sql declares none of the four columns and no session_kinds table,
#      and a DB built from it agrees — while the columns that merely sat NEXT to
#      them are untouched. The overreach guard.
#   4. The change file really migrates: a POPULATED old-shape DB loses all four
#      columns and the session_kinds table, keeps every row and every surviving
#      value, KEEPS its write-once trigger, and is idempotent on re-apply.
#   5. Every CLI surface is gone, and the two retired spawn flags refuse with an
#      explanation rather than a parse error.
#   6. The overreach guard with teeth: the error-greeting auto-hide that rode
#      along with `summary` still fires, driven end-to-end through the real
#      transcript parser.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 setup error.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'
    BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }

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

# assert_eq DESC WANT GOT
assert_eq() {
    local desc="$1" want="$2" got="$3"
    if [[ "${want}" == "${got}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${want}" "${got}"
}

# assert_contains DESC HAYSTACK NEEDLE
assert_contains() {
    local desc="$1" hay="$2" needle="$3"
    if [[ "${hay}" == *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "contains: ${needle}" "${hay}"
}

# assert_not_contains DESC HAYSTACK NEEDLE
assert_not_contains() {
    local desc="$1" hay="$2" needle="$3"
    if [[ "${hay}" != *"${needle}"* ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "does NOT contain: ${needle}" "${hay}"
}

# assert_file_lacks DESC FILE PATTERN
assert_file_lacks() {
    local desc="$1" file="$2" pat="$3"
    if ! grep -qF -- "${pat}" "${file}" 2>/dev/null; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "${file} lacks: ${pat}" "still present"
}

# assert_absent DESC PATH
assert_absent() {
    local desc="$1" path="$2"
    if [[ ! -e "${path}" ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "deleted" "still on disk"
}

# assert_succeeds DESC CMD [ARGS...]
assert_succeeds() {
    local desc="$1"; shift
    local out; out=$("$@" 2>&1); local rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit 0" "exit ${rc} | $(tail -25 <<<"${out}")"
}

# ─── globals ────────────────────────────────────────────────────────────────

REPO_ROOT=""
GO=""
EN=""
WORK=""
CHANGE="internal/schema/changes/e-2074-drop-sessions-bg-columns.go"

cleanup() { [[ -n "${WORK}" && -d "${WORK}" ]] && rm -rf "${WORK}"; }

setup() {
    REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${REPO_ROOT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    cd "${REPO_ROOT}" || exit 2

    GO="${REPO_ROOT}/bin/endless-go"
    EN="${REPO_ROOT}/.venv/bin/endless"
    command -v sqlite3 >/dev/null 2>&1 || {
        printf 'ERROR: sqlite3 not on PATH\n' >&2; exit 2; }
    if [[ ! -x "${GO}" ]]; then
        printf 'building bin/endless-go …\n'
        ( cd "${REPO_ROOT}" && just build >/dev/null 2>&1 ) || {
            printf 'ERROR: just build failed\n' >&2; exit 2; }
    fi
    if [[ ! -x "${EN}" ]]; then
        ( cd "${REPO_ROOT}" && uv run endless --version >/dev/null 2>&1 ) || {
            printf 'ERROR: could not materialize .venv (uv run endless failed)\n' >&2; exit 2; }
    fi

    WORK=$(mktemp -d)
    trap cleanup EXIT
}

# ─── 0 — the fail-fast fold-in regression front ─────────────────────────────
#
# The project-wide regression, folded in rather than handed to the user as a
# separate checklist item. `go test ./internal/...` as a whole is NOT run:
# TestDestroyForceOverridesLiveWriterCheck in internal/sandboxcmd hangs on main
# (E-1908) and would swallow this suite in a timeout. The packages below are
# exactly the ones this change touched.
check_regression_front() {
    section "0 — fail-fast fold-in regression"

    assert_succeeds "go build ./... is clean" go build ./...
    assert_succeeds "go vet ./... is clean (catches orphaned test references)" \
        go vet ./...
    assert_succeeds "Go tests pass for the packages this touched" \
        go test -timeout 300s \
            ./internal/events/... ./internal/monitor/... \
            ./internal/hookcmd/... ./internal/schema/... \
            ./internal/sessionquerycmd/... ./internal/templatecmd/... \
            ./internal/spawnlaunchcmd/... ./internal/eventcmd/...
    assert_succeeds "the Python suite passes" uv run pytest -q
    assert_succeeds "just guide-check is green (command→section map intact)" \
        just guide-check
}

# ─── 1 — no live reference survives ─────────────────────────────────────────
#
# Two categories of mention are legitimate and are filtered out rather than
# hunted down, following the pattern e-1906-verify.sh established:
#
#   a. Comments. A `#`/`--`/`//` line naming `short_id` to explain why something
#      is absent is the removal documenting itself. Deleting those comments
#      would make the change less legible, not more complete.
#   b. Whole files that must name what they remove or reproduce:
#      - .endless/     the ledger, plans and outcomes are an append-only record;
#      - e-1568-*, e-1571-*  those change files rebuild a PRE-E-1568 sessions
#                      table that HAD these columns, in a column list rather
#                      than a DDL declaration. They reproduce history against a
#                      DB that predates this change, and e-2074 then drops it;
#      - e-1905-*, e-1906-*  their verify scripts build historical `sessions`
#                      shapes inline to prove their own migrations;
#      - e-2074-*      this change's own DDL and this script's assertion list;
#      - sessions_removed_columns_test.go  the Go half of that assertion list —
#                      a guard that must name what it forbids, kept in a file of
#                      its own precisely so this exemption stays that narrow;
#      - docs/research-2026-06-*  dated external research about Claude Code's
#                      background-agent feature. It describes ANTHROPIC's
#                      product, not Endless's use of it, and stays true.
live_hits() {
    grep -rnI --exclude-dir=vendor --exclude-dir=.git --exclude-dir=.endless \
        --exclude='e-1568-*' --exclude='e-1571-*' --exclude='e-1905-*' \
        --exclude='e-1906-*' --exclude='e-2074-*' \
        --exclude='sessions_removed_columns_test.go' \
        --exclude='research-2026-06-*' \
        -e "$1" cmd internal src tests docs justfile 2>/dev/null \
    | grep -vE ':[0-9]+:[[:space:]]*(#|--|//)'
}

check_no_references() {
    section "1 — no live reference survives in the source tree"

    local ident hits
    for ident in short_id ShortID plan_file_path PlanFilePath \
                 session_kinds sessionkind SessionKind \
                 RecordBgAgentSession DecorateBgSession CountActiveBgAgents \
                 ListBgAgentsForEpic ListBgAgentsForProject BgAgent \
                 FocusedBgAgent nearestEpicAncestor \
                 record-bg-agent count-bg-agents list-bg-agents \
                 _lookup_bg_short_id _spawn_bg_dispatch task_attach_impl \
                 _session_is_background bg_throttle_warn; do
        hits=$(live_hits "${ident}")
        if [[ -z "${hits}" ]]; then
            report_pass "no live \`${ident}\` anywhere in the source tree"
        else
            report_fail "no live \`${ident}\` anywhere in the source tree" \
                "zero hits" "${hits}"
        fi
    done

    # `summary` is too common a word to sweep for (session_statuses.summary and
    # faults.Summary are unrelated live columns), so it is checked precisely:
    # no SQL statement anywhere may still read or write the sessions column.
    hits=$(grep -rnI --exclude-dir=vendor --exclude-dir=.git --exclude-dir=.endless \
        --exclude='e-1568-*' --exclude='e-1571-*' --exclude='e-1905-*' \
        --exclude='e-1906-*' --exclude='e-2074-*' \
        --exclude='sessions_removed_columns_test.go' \
        -e '\bs\.summary' -e 'sessions SET summary' -e '\bsessions\.summary' \
        -e '\bfs\.summary' -e '\bts\.summary' \
        cmd internal src tests docs 2>/dev/null \
        | grep -vE ':[0-9]+:[[:space:]]*(#|--|//)')
    if [[ -z "${hits}" ]]; then
        report_pass "no live read or write of sessions.summary"
    else
        report_fail "no live read or write of sessions.summary" "zero hits" "${hits}"
    fi

    # The comment filter must not become a blanket amnesty. A canary planted in
    # the tree and removed immediately proves it is still narrow: a Go usage
    # line — not a comment — must be caught.
    local canary="internal/monitor/e2074_canary_check.go"
    printf 'package monitor\n\nvar e2074Canary = "short_id"\n' > "${canary}"
    hits=$(live_hits short_id)
    rm -f "${canary}"
    assert_contains "a planted Go usage is still caught (the filter is narrow)" \
        "${hits}" "e2074_canary_check.go"
}

# ─── 2 — the dead files are deleted, not merely unreferenced ────────────────

check_files_deleted() {
    section "2 — the dead files and packages are deleted"

    assert_absent "internal/sessionkind/ is gone" "internal/sessionkind"
    assert_absent "internal/monitor/bg_agents.go is gone" "internal/monitor/bg_agents.go"
    assert_absent "internal/monitor/session_focus.go is gone" "internal/monitor/session_focus.go"
    assert_absent "src/endless/agents_cmd.py is gone" "src/endless/agents_cmd.py"
    assert_absent "tests/test_agents_cmd.py is gone" "tests/test_agents_cmd.py"
    assert_absent "tests/test_task_attach.py is gone" "tests/test_task_attach.py"
    assert_absent "tests/test_spawn_bg.py is gone" "tests/test_spawn_bg.py"
    assert_absent "tests/test_spawn_bg_throttle.py is gone" "tests/test_spawn_bg_throttle.py"
    # The verify suites for the removed features go too — a suite that proves a
    # deleted feature works is worse than no suite.
    assert_absent "tests/tasks/e-1568-verify.sh is gone" "tests/tasks/e-1568-verify.sh"
    assert_absent "tests/tasks/e-1570-verify.sh is gone" "tests/tasks/e-1570-verify.sh"
    assert_absent "tests/tasks/e-1572-verify.sh is gone" "tests/tasks/e-1572-verify.sh"
    assert_absent "tests/tasks/e-1621-verify.sh is gone" "tests/tasks/e-1621-verify.sh"
    assert_absent "docs/guide/help/agents.md is gone" "docs/guide/help/agents.md"
    assert_absent "docs/guide/help/task-attach.md is gone" "docs/guide/help/task-attach.md"
}

# ─── 3 — the schema shape, and the overreach guard ──────────────────────────

check_schema_shape() {
    section "3 — schema.sql drops four columns + a table, and keeps the neighbors"

    assert_file_lacks "schema.sql declares no session_kinds table" \
        "internal/schema/schema.sql" "CREATE TABLE IF NOT EXISTS session_kinds"
    assert_file_lacks "schema.sql declares no short_id column" \
        "internal/schema/schema.sql" "short_id TEXT"
    assert_file_lacks "schema.sql declares no plan_file_path column" \
        "internal/schema/schema.sql" "plan_file_path TEXT"
    assert_file_lacks "schema.sql declares no sessions.kind_id column" \
        "internal/schema/schema.sql" "kind_id INTEGER NOT NULL DEFAULT 1,"
    assert_file_lacks "schema.sql declares no UNIQUE (short_id)" \
        "internal/schema/schema.sql" "UNIQUE (short_id)"

    local db cols tables
    db="${WORK}/fresh.db"
    if ! sqlite3 "${db}" < internal/schema/schema.sql >/dev/null 2>&1; then
        report_fail "schema.sql applies cleanly to an empty DB" "exit 0" "sqlite3 failed"
        return
    fi
    report_pass "schema.sql applies cleanly to an empty DB"

    cols=$(sqlite3 "${db}" "SELECT ','||group_concat(name)||',' FROM pragma_table_info('sessions');")
    assert_not_contains "a fresh DB's sessions has no summary" "${cols}" ",summary,"
    assert_not_contains "a fresh DB's sessions has no short_id" "${cols}" ",short_id,"
    assert_not_contains "a fresh DB's sessions has no kind_id" "${cols}" ",kind_id,"
    assert_not_contains "a fresh DB's sessions has no plan_file_path" "${cols}" ",plan_file_path,"

    # The overreach guard. A four-column drop must take exactly those four and
    # no neighbor — `hidden` in particular sat directly between summary and
    # short_id in the old declaration.
    local keep
    for keep in id session_id project_id platform state task_id epic_id \
                process_id started_at last_activity transcript_offset hidden \
                last_user_prompt report_bounces report_exempt report_runs; do
        assert_contains "a fresh DB's sessions KEEPS ${keep}" "${cols}" ",${keep},"
    done

    tables=$(sqlite3 "${db}" "SELECT ','||group_concat(name)||',' FROM sqlite_master WHERE type='table';")
    assert_not_contains "a fresh DB has no session_kinds table" "${tables}" ",session_kinds,"
    # process_kinds is the OTHER ED-1506 enum mirror and is untouched — the
    # guard against deleting the wrong `*_kinds` table.
    assert_contains "a fresh DB KEEPS process_kinds" "${tables}" ",process_kinds,"
    assert_contains "a fresh DB KEEPS session_gates" "${tables}" ",session_gates,"

    # session_gates.kind_id (gatekind) and processes.kind_id (processkind) are
    # different columns on different tables. Neither may have been swept up.
    assert_eq "session_gates KEEPS its own kind_id" "1" \
        "$(sqlite3 "${db}" "SELECT count(*) FROM pragma_table_info('session_gates') WHERE name='kind_id';")"
    assert_eq "processes KEEPS its own kind_id" "1" \
        "$(sqlite3 "${db}" "SELECT count(*) FROM pragma_table_info('processes') WHERE name='kind_id';")"
}

# ─── 4 — the change file migrates a populated old-shape DB ──────────────────
#
# The change file is what runs against the real populated DB at land time, so
# prove it on a populated old-shape DB rather than trusting the DDL. Shape built
# inline for the same reason e-1906-verify.sh does it: no git archaeology, fully
# deterministic.
check_change_file_migrates() {
    section "4 — the change file migrates a populated old-shape DB"

    if [[ ! -f "${CHANGE}" ]]; then
        report_fail "the change file exists" "${CHANGE}" "absent"
        return
    fi
    report_pass "the change file exists"

    local mdir="${WORK}/migrate"; mkdir -p "${mdir}"
    sqlite3 "${mdir}/endless.db" <<'SQL'
CREATE TABLE session_kinds (
    id    INTEGER PRIMARY KEY,
    slug  TEXT UNIQUE NOT NULL,
    label TEXT NOT NULL
);
INSERT INTO session_kinds (id, slug, label) VALUES (1,'tmux','Tmux'), (2,'background','Background');
CREATE TABLE sessions (
    id INTEGER PRIMARY KEY,
    session_id TEXT,
    project_id INTEGER,
    platform TEXT NOT NULL DEFAULT 'claude',
    state TEXT NOT NULL DEFAULT 'working',
    task_id INTEGER,
    epic_id INTEGER,
    kind_id INTEGER NOT NULL DEFAULT 1,
    plan_file_path TEXT,
    process_id INTEGER,
    started_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    last_activity TEXT,
    transcript_offset INTEGER NOT NULL DEFAULT 0,
    summary TEXT,
    hidden INTEGER NOT NULL DEFAULT 0,
    short_id TEXT,
    last_user_prompt TEXT,
    report_bounces INTEGER NOT NULL DEFAULT 0,
    report_exempt INTEGER NOT NULL DEFAULT 0,
    report_runs INTEGER NOT NULL DEFAULT 0,
    UNIQUE (session_id),
    UNIQUE (short_id),
    FOREIGN KEY (kind_id) REFERENCES session_kinds(id)
);
CREATE TRIGGER sessions_task_id_write_once
BEFORE UPDATE OF task_id ON sessions
WHEN OLD.task_id IS NOT NULL AND NEW.task_id IS NOT OLD.task_id
BEGIN
    SELECT RAISE(ABORT, 'sessions.task_id is write-once');
END;
-- A tmux row and a background row, so the copy is proven on both.
INSERT INTO sessions
    (id, session_id, state, kind_id, short_id, plan_file_path, summary,
     hidden, transcript_offset, started_at, last_activity, task_id, report_runs)
    VALUES (11, 'uuid-A', 'working', 1, NULL, '/home/u/plan.md',
            'a summary that is going away', 0, 4096,
            '2026-01-01T00:00:00', '2026-01-02T00:00:00', 1337, 2),
           (12, NULL, 'working', 2, 'feed1234', NULL, NULL,
            1, 0, '2026-01-03T00:00:00', '2026-01-04T00:00:00', 1338, 0);
-- A child row keyed on sessions(id): the rebuild must not orphan it.
CREATE TABLE session_tasks (
    session_id INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    task_id    INTEGER NOT NULL
);
INSERT INTO session_tasks (session_id, task_id) VALUES (11, 1337), (12, 1338);
SQL

    local pre applied post rows kinds trig child err
    pre=$(sqlite3 "${mdir}/endless.db" \
        "SELECT count(*) FROM pragma_table_info('sessions') WHERE name IN ('summary','short_id','kind_id','plan_file_path');")
    assert_eq "PRE: the old DB really has all four columns" "4" "${pre}"

    applied=$("${GO}" --config-dir "${mdir}" event apply-change "${CHANGE}" 2>&1)
    assert_contains "apply-change succeeds on the populated old DB" \
        "${applied}" '"status":"applied"'

    post=$(sqlite3 "${mdir}/endless.db" \
        "SELECT count(*) FROM pragma_table_info('sessions') WHERE name IN ('summary','short_id','kind_id','plan_file_path');")
    assert_eq "POST: all four columns are gone" "0" "${post}"

    kinds=$(sqlite3 "${mdir}/endless.db" \
        "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='session_kinds';")
    assert_eq "POST: the session_kinds table is dropped" "0" "${kinds}"

    # A DROP COLUMN (or a rebuild) that took data with it would be the expensive
    # way to fail this change. Every surviving value, on both rows.
    rows=$(sqlite3 "${mdir}/endless.db" \
        "SELECT group_concat(id||':'||IFNULL(session_id,'NULL')||':'||state||':'||hidden||':'||transcript_offset||':'||task_id||':'||report_runs, ' ') FROM sessions ORDER BY id;")
    assert_eq "POST: both rows survive with every surviving value intact" \
        "11:uuid-A:working:0:4096:1337:2 12:NULL:working:1:0:1338:0" "${rows}"

    child=$(sqlite3 "${mdir}/endless.db" "SELECT count(*) FROM session_tasks;")
    assert_eq "POST: the child rows keyed on sessions(id) are not orphaned" "2" "${child}"

    # The rebuild DROPs the table, which takes the trigger with it. Without an
    # explicit recreate, sessions.task_id silently stops being write-once
    # (ED-1560 / E-1969) on every already-populated DB.
    trig=$(sqlite3 "${mdir}/endless.db" \
        "SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name='sessions_task_id_write_once';")
    assert_eq "POST: the write-once trigger is recreated" "1" "${trig}"

    # And it must actually fire, not merely exist.
    err=$(sqlite3 "${mdir}/endless.db" "UPDATE sessions SET task_id = 9999 WHERE id = 11;" 2>&1)
    assert_contains "POST: the recreated trigger actually refuses a repoint" \
        "${err}" "write-once"

    # Idempotence: `just land` may re-run a change after a partial failure.
    applied=$("${GO}" --config-dir "${mdir}" event apply-change "${CHANGE}" 2>&1)
    assert_not_contains "re-applying is a no-op, not an error" "${applied}" '"status":"applied"'
}

# ─── 5 — every CLI surface is gone ──────────────────────────────────────────

check_cli_surfaces_gone() {
    section "5 — every CLI surface is gone (Go + Python)"

    local out rc

    out=$("${GO}" session-query 2>&1)
    assert_not_contains "session-query usage drops record-bg-agent" "${out}" "record-bg-agent"
    assert_not_contains "session-query usage drops count-bg-agents" "${out}" "count-bg-agents"
    assert_not_contains "session-query usage drops list-bg-agents" "${out}" "list-bg-agents"
    assert_contains "session-query still lists its real subcommands" "${out}" "list-live"

    out=$("${GO}" session-query record-bg-agent --task-id 1 --short-id x 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]]; then report_pass "\`session-query record-bg-agent\` exits non-zero"
    else report_fail "\`session-query record-bg-agent\` exits non-zero" "non-zero" "exit 0"; fi

    out=$("${GO}" spawn-window --window-name w --attach --short-id x 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]]; then report_pass "\`spawn-window --attach\` is refused"
    else report_fail "\`spawn-window --attach\` is refused" "non-zero" "exit 0"; fi

    out=$(cd "${REPO_ROOT}" && "${EN}" --help 2>&1)
    assert_not_contains "\`endless --help\` no longer lists \`agents\`" "${out}" "agents"

    out=$(cd "${REPO_ROOT}" && "${EN}" agents 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]]; then report_pass "\`endless agents\` no longer exists"
    else report_fail "\`endless agents\` no longer exists" "non-zero exit" "exit 0"; fi

    out=$(cd "${REPO_ROOT}" && "${EN}" task --help 2>&1)
    assert_not_contains "\`endless task --help\` no longer lists \`attach\`" "${out}" "attach"
    assert_contains "\`endless task --help\` still lists \`spawn\`" "${out}" "spawn"

    out=$(cd "${REPO_ROOT}" && "${EN}" task attach E-1 2>&1); rc=$?
    if [[ "${rc}" -ne 0 ]]; then report_pass "\`endless task attach\` no longer exists"
    else report_fail "\`endless task attach\` no longer exists" "non-zero exit" "exit 0"; fi

    # The two retired spawn flags stay ACCEPTED so muscle memory gets an answer,
    # and refuse with an explanation rather than a click parse error.
    out=$(cd "${REPO_ROOT}" && "${EN}" task spawn --help 2>&1)
    assert_not_contains "\`task spawn --help\` no longer advertises --bg" "${out}" "--bg"

    out=$(cd "${REPO_ROOT}" && "${EN}" task spawn E-1 --bg 2>&1); rc=$?
    assert_contains "\`task spawn --bg\` refuses with an explanation" \
        "${out}" "no longer supports background agents"
    assert_not_contains "the refusal is not a click parse error" "${out}" "no such option"
    if [[ "${rc}" -ne 0 ]]; then report_pass "\`task spawn --bg\` exits non-zero"
    else report_fail "\`task spawn --bg\` exits non-zero" "non-zero" "exit 0"; fi

    out=$(cd "${REPO_ROOT}" && "${EN}" task spawn E-1 --attach 2>&1)
    assert_contains "\`task spawn --attach\` refuses with an explanation" \
        "${out}" "no longer supports background agents"

    # The docs must not advertise what the CLI no longer has.
    assert_file_lacks "the orchestration guide drops \`task attach\`" \
        "docs/guide/orchestration.md" "endless task attach"
    assert_file_lacks "the orchestration guide drops \`spawn --bg\`" \
        "docs/guide/orchestration.md" "spawn <id> --bg"
}

# ─── 6 — the overreach guard with teeth ─────────────────────────────────────
#
# `summary` did TWO things: it stored a 200-character slice of the first
# assistant response, and — for a response opening "Not logged in" or "Error:" —
# it hid the session from the listings (E-867). Only the first was dead.
#
# The end-to-end proof runs in Go, because ParseTranscript has no CLI surface of
# its own (the hook is its only caller) and inventing one to test with would be
# a worse outcome than the check. Named explicitly here rather than left to the
# `go test` in check 0 so that a failure of THIS guarantee is legible as itself.
check_autohide_survives() {
    section "6 — the error-greeting auto-hide survives the column it rode on"

    assert_succeeds "ParseTranscript still hides an error-greeting session" \
        go test ./internal/monitor/ -count=1 \
            -run 'TestParseTranscript_HidesErrorGreetingSession'
    assert_succeeds "the four columns are gone and their namesakes are not" \
        go test ./internal/monitor/ -count=1 \
            -run 'TestSessionsHasNoneOfTheE2074Columns'

    # The Go-side shape those tests rest on: the summary writer is gone and the
    # hide it carried has a home of its own.
    assert_file_lacks "monitor no longer defines setSummaryIfEmpty" \
        "internal/monitor/transcript.go" "func setSummaryIfEmpty"
    if grep -q "func hideIfErrorGreeting" internal/monitor/transcript.go 2>/dev/null; then
        report_pass "monitor defines hideIfErrorGreeting in its place"
    else
        report_fail "monitor defines hideIfErrorGreeting in its place" "present" "absent"
    fi
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    setup

    printf '%sE-2074 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  repo:   %s\n' "${REPO_ROOT}"
    printf '  binary: %s\n' "${GO}"

    check_regression_front
    # Fail fast: if the build or the suites are broken, the rest of this script
    # is reporting on rubble.
    if [[ "${FAIL_COUNT}" -gt 0 ]]; then
        printf '\n  %sregression front failed — skipping the E-2074 checks%s\n' \
            "${RED}${BOLD}" "${RESET}"
        summary
        return 1
    fi

    check_no_references
    check_files_deleted
    check_schema_shape
    check_change_file_migrates
    check_cli_surfaces_gone
    check_autohide_survives

    summary
}

main "$@"
