#!/usr/bin/env bash
#
# E-1929 verification script — task removal retains the row and filters it out
# of every read path.
#
# The bug (ED-1547): `task remove` DELETEd the tasks row, which FREED the id.
# The allocator is `SELECT COALESCE(MAX(id), 0) + 1 FROM tasks`, so removing the
# highest task handed its id straight to the next one filed — and every FK-free
# row that deliberately outlives its task (session_tasks, session_notices,
# task_landings) silently reattached to unrelated work. That is how a session
# came to report touching a task it had never seen.
#
# The fix: removal is now `UPDATE tasks SET removed = 1`. The row stays, so
# MAX(id) keeps counting past it and the id can never be re-minted. Reads go
# through a new `live_tasks` view; the allocator, the removal path itself, and
# `task show` / `task list --removed` are the only things that still read raw
# `tasks`.
#
# Retention is what makes the read-path audit the bulk of the work: a join to
# `tasks` that used to drop a dangling reference BY ACCIDENT now MATCHES the
# retained row, so any missed rewrite leaks a removed task into a listing.
#
# Absorbs E-1930 (policy scope), E-1931 (drop pending notices on removal) and
# E-1932 (one-shot repair of session_tasks rows that predate their task).
#
# Run from anywhere inside the worktree:
#   esu && ./tests/tasks/e-1929-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Section A is a FAIL-FAST gate: the Go executor/projector-parity tests, the
# schema-ordering guard, and the task-removal pytest suite run first, and the
# script stops there if they fail. Everything after them asserts end-to-end
# behavior that is meaningless if the unit level is already broken.
#
# Section K covers the land itself. `endless db apply-change` opens the DB
# through monitor.DB() — which applies schema.sql — BEFORE dispatching to the
# change script, so the new schema.sql always meets the old, column-less DB
# first. An index on tasks(removed) there aborted the first land attempt with
# "no such column: removed", because CREATE INDEX resolves its columns eagerly
# while CREATE VIEW does not. The guard in section A pins that; section K pins
# that the change still applies, and re-applies as a no-op so a failed land can
# be retried.
#
# Isolation: a throwaway git repo as project root under a temp dir, with its own
# XDG_CONFIG_HOME (own DB and ledger) and XDG_CACHE_HOME. No real DB, ledger or
# cache is touched. The Python CLI runs from the worktree source with
# <worktree>/bin prepended to PATH, so the event bridge execs the candidate
# endless-go rather than the global install; the Go renderer is invoked directly
# from <worktree>/bin with --config-dir pointed at the temp DB.

set -u

# ─── output ─────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section()     { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}
summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'; return 1
}

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"; else report_fail "$1" "$2" "$3"; fi
}
# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output contains: $2" "$3"; fi
}
# assert_not_contains DESC NEEDLE HAYSTACK
assert_not_contains() {
    if [[ "$3" != *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output must NOT contain: $2" "$3"; fi
}

# ─── shared fixture ──────────────────────────────────────────────────────────

WT=""; TMP=""; REPO=""; EGO=""; DBDIR=""; PID=""

# E: the worktree's Python CLI against the isolated DB, from the repo dir.
E() { ( cd "$REPO" && uv run --project "$WT" endless "$@" ); }
# Q SQL: one scalar/row out of the isolated DB.
Q() { E sql "$1" --tsv 2>/dev/null; }
# W SQL: a write against the isolated DB (fixture seeding).
W() { E sql "$1" --write >/dev/null 2>&1; }
# GO_STATUS ...: the candidate Go renderer — the SAME code path `session status`
# and the live monitor both render from — headless, against the isolated DB.
GO_STATUS() { NO_COLOR=1 "$EGO" --config-dir "$DBDIR" session-status --cols 200 "$@" 2>&1; }

# Ids are fixed so every assertion can name them literally.
FOCAL=9100       # the goal task S1 is on; the session-status focal
VICTIM=9101      # the plain removal target — pointed at, landed, noticed, touched
KEEPER=9102      # the control: same shape as VICTIM, never removed
PARENT=9200      # --cascade root
CHILD=9201
GRANDCHILD=9202
RELATED=9300     # holds a relation — the E-1915 guard's subject
PEER=9301
S1=9501          # session whose active_task_id points at VICTIM
S2=9502          # session whose active_task_id points at GRANDCHILD
S3=9503          # session pointing at KEEPER — must be left alone

setup_fixture() {
    TMP="$(mktemp -d)"
    REPO="$TMP/repo"
    DBDIR="$TMP/xdg/endless"
    export XDG_CONFIG_HOME="$TMP/xdg"
    export XDG_CACHE_HOME="$TMP/cache"
    # Prepend the freshly-built worktree binary so the Python event bridge's PATH
    # fallback (cwd is the /tmp repo, not a self-dev worktree) execs candidate
    # code, not the stale global endless-go.
    export PATH="$WT/bin:$PATH"
    mkdir -p "$REPO" "$DBDIR"

    git -C "$REPO" init -q
    git -C "$REPO" config user.email verify@test
    git -C "$REPO" config user.name verify
    git -C "$REPO" commit -q --allow-empty -m "initial commit"

    # Pin any project scan to the temp dir so nothing walks the developer's real
    # project roots.
    printf '{"roots": ["%s"]}\n' "$TMP" > "$DBDIR/config.json"

    E project register "$REPO" --name probe --label Probe --desc d --lang Go --status active >/dev/null 2>&1
    PID="$(Q "SELECT id FROM projects WHERE name='probe'")"
    [[ -n "$PID" ]] || return 1

    W "INSERT INTO tasks (id, project_id, title, status, phase) VALUES
         ($FOCAL,   $PID, 'Focal goal task',       'underway', 'now'),
         ($VICTIM,  $PID, 'Victim removal target', 'ready',    'now'),
         ($KEEPER,  $PID, 'Keeper control task',   'ready',    'now'),
         ($PARENT,  $PID, 'Cascade parent task',   'ready',    'now'),
         ($RELATED, $PID, 'Related task',          'ready',    'now'),
         ($PEER,    $PID, 'Peer task',             'ready',    'now')"
    W "INSERT INTO tasks (id, project_id, title, status, phase, parent_id) VALUES
         ($CHILD, $PID, 'Cascade child task', 'ready', 'now', $PARENT)"
    W "INSERT INTO tasks (id, project_id, title, status, phase, parent_id) VALUES
         ($GRANDCHILD, $PID, 'Cascade grandchild task', 'ready', 'now', $CHILD)"

    # Sessions. S1 is on the focal AND points at VICTIM; S2 points at the
    # grandchild (the cascade's deepest node); S3 points at KEEPER and must be
    # untouched by either removal.
    W "INSERT INTO sessions (id, session_id, project_id, state, kind_id, active_task_id) VALUES
         ($S1, 'uuid-$S1', $PID, 'working', 1, $VICTIM),
         ($S2, 'uuid-$S2', $PID, 'working', 1, $GRANDCHILD),
         ($S3, 'uuid-$S3', $PID, 'working', 1, $KEEPER)"

    # session_statuses carries its own active_task_id — the second pointer that
    # used to be nulled by ON DELETE SET NULL and now must be nulled explicitly.
    W "INSERT INTO session_statuses (session_id, active_task_id, recorded_at) VALUES
         ($S1, $VICTIM,     '2026-08-09T00:00:00'),
         ($S2, $GRANDCHILD, '2026-08-09T00:00:00'),
         ($S3, $KEEPER,     '2026-08-09T00:00:00')"

    # session_tasks: what makes VICTIM and KEEPER render in session status. The
    # rows have NO FK and deliberately outlive their task.
    W "INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at) VALUES
         ($S1, $VICTIM, 3, '2026-08-09T00:00:00', '2026-08-09T00:00:00'),
         ($S1, $KEEPER, 3, '2026-08-09T00:00:00', '2026-08-09T00:00:00')"
    # S1's active_task_id is VICTIM above, but session status resolves its row
    # set from sessions whose active_task_id is the FOCAL, so bind S1 there too
    # once the pointer assertions have been made (see test_pointers_nulled).

    # Landing history — audit data that must SURVIVE the removal (the FK used to
    # cascade it away).
    W "INSERT INTO task_landings (task_id, session_id, branch, merge_commit_sha, landed_at)
         VALUES ($VICTIM, $S1, 'task/9101-victim', 'deadbeef', '2026-08-09T00:00:00')"

    # Notices: one pending and one delivered for VICTIM, one pending for KEEPER.
    # Removal drops ONLY the pending ones for the removed task.
    W "INSERT INTO session_notices (session_id, task_id, changes, changed_at, notified) VALUES
         ($S1, $VICTIM, '{}', '2026-08-09T00:00:00', 0),
         ($S1, $VICTIM, '{}', '2026-08-09T00:00:00', 1),
         ($S1, $KEEPER, '{}', '2026-08-09T00:00:00', 0)"

    [[ "$(Q "SELECT count(*) FROM tasks")" == "8" ]] || return 1
    [[ "$(Q "SELECT count(*) FROM live_tasks")" == "8" ]] || return 1
    return 0
}

# ─── A: fail-fast — the unit level this whole change rests on ────────────────

test_unit_gate() {
    section "A. Fail-fast gate: executor/projector parity + removal unit suites"
    local out rc

    # The parity tests. If the projector still replayed removal as a DELETE while
    # the executor marked removed = 1, a rebuild would re-free every removed id
    # and this change would ship looking complete.
    out=$(cd "$WT" && go test ./internal/events/ \
        -run 'TestProjectToTempDB_TaskDeletedRetainsRowAndIDFloor|TestProjectToTempDB_TaskBulkClearedRetainsRowsAndIDFloor|TestSessionTasks_DeletedTaskRetainsRow' \
        -count=1 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go: executor/projector removal parity + id floor"
    else report_fail "go: executor/projector removal parity + id floor" "exit 0" \
        "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"; fi

    # The migration-ordering guard. `endless db apply-change` opens the DB
    # through monitor.DB(), which applies schema.sql, BEFORE dispatching to the
    # change script — so the new schema.sql always meets the old, column-less DB
    # first. Anything in it that resolves `removed` eagerly (an index, a CHECK, a
    # generated column) aborts the land before the column can ever be added. That
    # is not hypothetical: an index on tasks(removed) did exactly this on the
    # first land attempt.
    out=$(cd "$WT" && go test ./internal/schema/ \
        -run 'TestSchema_AppliesToDBPredatingItsNewestColumn' -count=1 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go: schema.sql applies to a DB that predates tasks.removed"
    else report_fail "go: schema.sql applies to a DB that predates tasks.removed" "exit 0" \
        "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"; fi

    out=$(cd "$WT" && uv run pytest tests/test_task_remove_relations.py -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "pytest test_task_remove_relations passes"
    else report_fail "pytest test_task_remove_relations" "exit 0" \
        "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"; fi

    [[ "${FAIL_COUNT}" -eq 0 ]]
}

# ─── K: the migration, through the dispatcher that runs it at land ───────────

test_migration_applies() {
    section "K. The schema change applies through the land-time dispatcher"

    # Its OWN config dir: this asserts on _schema_version, and the main fixture
    # DB must not carry a marker it never earned.
    local migdir="$TMP/migration/endless" out rc
    mkdir -p "$migdir"

    # First touch builds the DB from schema.sql — the fresh-DB shape, which
    # already HAS the column. Applying the change here is the idempotence case
    # the sandbox and every test DB hit, and the one a plain `.sql` ALTER would
    # hard-error on.
    "$EGO" --config-dir "$migdir" session-status --task 1 >/dev/null 2>&1

    out=$("$EGO" --config-dir "$migdir" event apply-change \
        "$WT/internal/schema/changes/e-1929-add-tasks-removed.go" 2>&1); rc=$?
    assert_eq "the change applies through the dispatcher" "0" "$rc"
    assert_contains "...reporting applied" '"status":"applied"' "$out"

    out=$("$EGO" --config-dir "$migdir" event apply-change \
        "$WT/internal/schema/changes/e-1929-add-tasks-removed.go" 2>&1); rc=$?
    assert_eq "re-applying is a no-op, so a failed land can be retried" "0" "$rc"
    assert_contains "...reporting skipped" '"status":"skipped"' "$out"
}

# ─── B: the row survives, flagged ────────────────────────────────────────────

test_row_retained() {
    section "B. Removal retains the row and marks it"

    local out rc
    out="$(E task remove "$VICTIM" 2>&1)"; rc=$?
    assert_eq "the removal succeeds" "0" "$rc"
    assert_contains "...and says 'Removed', not 'Deleted'" "Removed" "$out"

    assert_eq "the tasks row is still there" \
        "1" "$(Q "SELECT count(*) FROM tasks WHERE id=$VICTIM")"
    assert_eq "...flagged removed = 1" \
        "1" "$(Q "SELECT removed FROM tasks WHERE id=$VICTIM")"
    assert_eq "...and invisible through live_tasks" \
        "0" "$(Q "SELECT count(*) FROM live_tasks WHERE id=$VICTIM")"
    assert_eq "the control task is untouched" \
        "0" "$(Q "SELECT removed FROM tasks WHERE id=$KEEPER")"
}

# ─── C: absent from every read path ──────────────────────────────────────────

test_absent_from_reads() {
    section "C. The removed task is gone from every listing"

    local out
    out="$(E task list 2>&1)"
    assert_not_contains "task list omits it" "E-$VICTIM" "$out"
    assert_contains "...while still listing the control" "E-$KEEPER" "$out"

    out="$(E task next 2>&1)"
    assert_not_contains "task next omits it" "E-$VICTIM" "$out"

    out="$(E task recent 2>&1)"
    assert_not_contains "task recent omits it" "E-$VICTIM" "$out"

    out="$(E task search Victim 2>&1)"
    assert_not_contains "task search omits it" "E-$VICTIM" "$out"

    # session status and the live monitor render from the SAME row builder
    # (monitor.SessionStatusRows), which is precisely where a retained row starts
    # matching a join that used to drop it. --session lists the viewing session's
    # own surfaced/revisited rows, which is how VICTIM got there.
    out="$(GO_STATUS --session "$S1" --all)"
    assert_not_contains "session status / monitor omits it" "E-$VICTIM" "$out"
    assert_contains "...while still rendering the control" "E-$KEEPER" "$out"

    out="$(E session list 2>&1)"
    assert_not_contains "session list omits it as an active task" "E-$VICTIM" "$out"
}

# ─── D: task show is the one place it stays visible ──────────────────────────

test_show_marks_removed() {
    section "D. task show renders it, marked removed"

    local out rc
    out="$(E task show "$VICTIM" --no-color 2>&1)"; rc=$?
    assert_eq "task show on a removed id still succeeds" "0" "$rc"
    assert_contains "...marked REMOVED" "REMOVED" "$out"
    assert_contains "...and says why the id is retained" "retained" "$out"
    assert_contains "...while still showing the task it was" "Victim removal target" "$out"

    out="$(E task show "$VICTIM" --llm 2>&1)"
    assert_contains "--llm carries removed=true" "removed=true" "$out"

    out="$(E task show "$KEEPER" --llm 2>&1)"
    assert_not_contains "...and a live task does not" "removed=true" "$out"

    out="$(E task show "$VICTIM" --json 2>&1)"
    assert_contains "--json carries \"removed\": true" '"removed": true' "$out"
}

# ─── E: task list --removed ──────────────────────────────────────────────────

test_list_removed() {
    section "E. task list --removed lists them, and only them"

    local out
    out="$(E task list --removed 2>&1)"
    assert_contains "the removed task is listed" "E-$VICTIM" "$out"
    assert_not_contains "...and live tasks are NOT interleaved" "E-$KEEPER" "$out"
    assert_contains "...under a header that says so" "Removed tasks" "$out"

    out="$(E task list --removed --llm 2>&1)"
    assert_contains "--llm marks the listing as the removed set" "(removed)" "$out"
}

# ─── F: the regression the whole change exists to prevent ────────────────────

test_id_monotonicity() {
    section "F. A removed id is never re-minted"

    local doomed next_id out
    out="$(E task add 'Probe the id allocator floor' 2>&1)"
    doomed="$(Q "SELECT MAX(id) FROM tasks")"
    if [[ -z "$doomed" ]]; then
        report_fail "a fresh task is allocated the highest id" "an id" "$out"
        return
    fi
    report_pass "a fresh task takes the highest id (E-$doomed)"

    E task remove "$doomed" >/dev/null 2>&1
    assert_eq "removing the HIGHEST task retains its row" \
        "1" "$(Q "SELECT removed FROM tasks WHERE id=$doomed")"

    E task add 'Probe the id allocator floor again' >/dev/null 2>&1
    next_id="$(Q "SELECT MAX(id) FROM tasks WHERE removed = 0")"

    if [[ -n "$next_id" && "$next_id" -gt "$doomed" ]]; then
        report_pass "the next task allocated gets E-$next_id, above the removed E-$doomed"
    else
        report_fail "the next task allocated is above the removed id" \
            "> $doomed" "${next_id:-<none>}"
    fi
}

# ─── G: what the FK actions used to do ───────────────────────────────────────

test_pointers_nulled() {
    section "G. The pointers the FKs used to clear, and the history they must not"

    assert_eq "sessions.active_task_id is nulled" \
        "" "$(Q "SELECT COALESCE(active_task_id, '') FROM sessions WHERE id=$S1")"
    assert_eq "session_statuses.active_task_id is nulled" \
        "" "$(Q "SELECT COALESCE(active_task_id, '') FROM session_statuses WHERE session_id=$S1")"
    assert_eq "...and an unrelated session's pointer is untouched" \
        "$KEEPER" "$(Q "SELECT active_task_id FROM sessions WHERE id=$S3")"

    # task_landings was ON DELETE CASCADE and now simply does not fire. That is
    # the intended outcome: landing history is audit data, and the retained task
    # row is what explains it.
    assert_eq "task_landings history SURVIVES the removal" \
        "1" "$(Q "SELECT count(*) FROM task_landings WHERE task_id=$VICTIM")"

    # session_tasks likewise: no FK, and now safe, because the id it names can
    # never be handed to a different task. Scoped to the SEEDED session — the
    # removal itself records its own touch row from the emitting session, so an
    # unscoped count would be asserting that unrelated behavior instead.
    assert_eq "the session_tasks touch row survives too" \
        "1" "$(Q "SELECT count(*) FROM session_tasks WHERE task_id=$VICTIM AND session_id=$S1")"
}

# ─── H: dead mail (absorbed E-1931) ──────────────────────────────────────────

test_pending_notices_dropped() {
    section "H. Undelivered notices about a removed task are dropped"

    assert_eq "the pending notice is gone" \
        "0" "$(Q "SELECT count(*) FROM session_notices WHERE task_id=$VICTIM AND notified=0")"
    assert_eq "...but the DELIVERED one remains — it records what was shown" \
        "1" "$(Q "SELECT count(*) FROM session_notices WHERE task_id=$VICTIM AND notified=1")"
    assert_eq "...and another task's pending notice is untouched" \
        "1" "$(Q "SELECT count(*) FROM session_notices WHERE task_id=$KEEPER AND notified=0")"
}

# ─── I: cascade ──────────────────────────────────────────────────────────────

test_cascade() {
    section "I. --cascade marks every descendant, and clears every pointer"

    local out rc
    out="$(E task remove "$PARENT" --cascade 2>&1)"; rc=$?
    assert_eq "the cascade removal succeeds" "0" "$rc"
    assert_contains "...reporting the descendants it covered" "2 descendant(s)" "$out"

    assert_eq "all three rows are retained and flagged" "3" \
        "$(Q "SELECT count(*) FROM tasks WHERE id IN ($PARENT,$CHILD,$GRANDCHILD) AND removed=1")"
    assert_eq "...and none is visible through live_tasks" "0" \
        "$(Q "SELECT count(*) FROM live_tasks WHERE id IN ($PARENT,$CHILD,$GRANDCHILD)")"

    # The pointer nulling must run for EVERY id in the tree, not just the root.
    assert_eq "the DESCENDANT's sessions.active_task_id is nulled" \
        "" "$(Q "SELECT COALESCE(active_task_id, '') FROM sessions WHERE id=$S2")"
    assert_eq "...and its session_statuses pointer too" \
        "" "$(Q "SELECT COALESCE(active_task_id, '') FROM session_statuses WHERE session_id=$S2")"

    out="$(E task list 2>&1)"
    assert_not_contains "the whole subtree is gone from task list" "E-$GRANDCHILD" "$out"
}

# ─── J: the guards that must still refuse ────────────────────────────────────

test_guards_still_refuse() {
    section "J. The E-1915 and E-1927 guards are unchanged"

    E task link "$RELATED" --to "E-$PEER" --type blocks >/dev/null 2>&1
    local out rc
    out="$(E task remove "$RELATED" 2>&1)"; rc=$?
    assert_eq "removing a task with a relation is still refused" "1" "$rc"
    assert_contains "...still naming the unlink that clears it" \
        "endless task unlink E-$RELATED --to E-$PEER --type blocks" "$out"
    assert_eq "...and the task is neither deleted nor flagged" \
        "0" "$(Q "SELECT removed FROM tasks WHERE id=$RELATED")"

    # E-1927: the bulk-clear path (task import --replace) is guarded the same way,
    # and it retains rather than deletes for the same reason.
    local imported
    printf '# Plan\n\n## Now\n\n- Ship the imported probe task\n' > "$TMP/plan.md"
    E task import "$TMP/plan.md" >/dev/null 2>&1
    imported="$(Q "SELECT id FROM live_tasks WHERE source_file LIKE '%plan.md' ORDER BY id LIMIT 1")"
    if [[ -z "$imported" ]]; then
        report_fail "a task imports from a plan file" "an imported task id" "<none>"
        return
    fi
    report_pass "a task imports from a plan file (E-$imported)"

    E task link "$imported" --to "E-$PEER" --type blocks >/dev/null 2>&1
    out="$(E task import "$TMP/plan.md" --replace 2>&1)"; rc=$?
    assert_eq "a bulk clear over a related task is still refused" "1" "$rc"
    assert_eq "...and the imported task is untouched" \
        "0" "$(Q "SELECT removed FROM tasks WHERE id=$imported")"

    E task unlink "$imported" --to "$PEER" --type blocks >/dev/null 2>&1
    out="$(E task import "$TMP/plan.md" --replace 2>&1)"; rc=$?
    assert_eq "...and once unlinked, the bulk clear runs" "0" "$rc"
    assert_eq "the bulk-cleared task is RETAINED, not deleted" \
        "1" "$(Q "SELECT removed FROM tasks WHERE id=$imported")"
}

# ─── L: broader suites ───────────────────────────────────────────────────────

test_suites() {
    section "L. Regression suites"
    local out rc

    out=$(cd "$WT" && go test ./internal/events/ ./internal/monitor/ ./internal/web/ -count=1 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go: events + monitor + web suites pass"
    else report_fail "go: events + monitor + web suites" "exit 0" \
        "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -30)"; fi

    out=$(cd "$WT" && uv run pytest tests/ -q -x 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "pytest: the full Python suite passes"
    else report_fail "pytest: the full Python suite" "exit 0" \
        "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -30)"; fi
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    WT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -n "${WT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    EGO="${WT}/bin/endless-go"

    command -v uv >/dev/null  || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    command -v git >/dev/null || { printf 'ERROR: git not on PATH\n' >&2; exit 2; }
    command -v go >/dev/null  || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }
    [[ -x "${EGO}" ]] || {
        printf 'ERROR: %s missing — run `just build`\n' "${EGO}" >&2; exit 2; }

    printf '%sE-1929 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"

    # The fail-fast gate runs before the fixture: it needs no isolated DB, and a
    # failure here makes every end-to-end assertion below uninterpretable.
    if ! test_unit_gate; then
        printf '\n  %sfail-fast: the unit level is broken; skipping the end-to-end checks%s\n' \
            "${RED}${BOLD}" "${RESET}"
        summary
        exit 1
    fi

    if ! setup_fixture; then
        printf 'ERROR: fixture setup failed (isolated DB seeding)\n' >&2
        [[ -n "$TMP" ]] && rm -rf "$TMP"
        exit 2
    fi

    test_row_retained
    test_absent_from_reads
    test_show_marks_removed
    test_list_removed
    test_id_monotonicity
    test_pointers_nulled
    test_pending_notices_dropped
    test_cascade
    test_guards_still_refuse
    test_migration_applies
    test_suites

    [[ -n "$TMP" ]] && rm -rf "$TMP"
    summary
}

main "$@"
