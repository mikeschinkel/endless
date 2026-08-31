#!/usr/bin/env bash
#
# E-1969 verification script — `sessions.active_task_id` is now
# `sessions.task_id`, `sessions.active_epic_id` is `sessions.epic_id`,
# `session_statuses.active_task_id` is `session_statuses.task_id`, and the
# ED-1560 write-once rule is enforced in the database by a trigger.
#
# What each section proves:
#
#   0. This task's unit suites, FAIL-FAST. The rename touches nearly every Go
#      and Python path that reads a session, so if `go test ./internal/...` or
#      pytest is red, nothing below is measuring what it claims to.
#   A. The old spellings are gone from live code — both the column names and
#      the `active_`-qualified Go identifiers and JSON keys that mirrored them
#      (ActiveTaskInfo, ErrNoActiveTask, GetActiveTaskForPane, SessionActiveEpic,
#      the `active_task` key in two --json payloads). The only files allowed to
#      still say `active_task_id` are the ones that MUST: historical schema
#      changes (they reproduce the schema as it stood at their point in
#      history), db.py's E-743 migration step (one link of a chain), and prose
#      that names the old name to explain the new one.
#
#      Two `active` names survive on purpose and are pinned as such below:
#      `monitor.GetActiveTasks` (a project's OPEN tasks — a different sense of
#      the word) and `PaneStatusActive` (the status bar having something live to
#      show). `tmux active-id` also stays: it is a subcommand string baked into
#      tmux config that `tmux apply` already generated.
#   B. A FRESH database — built from schema.sql, the way every new install, the
#      sandbox and every test DB is built — has the new columns and the
#      write-once trigger. Migrated and fresh must not drift (ED-1472).
#   C. The change file carries a REAL pre-rename database across, in the exact
#      order `endless db apply-change` performs it: schema.sql runs against the
#      OLD database first. That order is not incidental — it is what makes the
#      DROP TRIGGER at the top of the change file load-bearing, because SQLite
#      resolves a trigger body lazily (so the new trigger is created against the
#      old column names without complaint) and then re-parses every trigger
#      during ALTER TABLE ... RENAME COLUMN (so the rename aborts). Also checks
#      that data survives, that a second run is a no-op, and that the change is
#      a no-op on a database that was born with the new names.
#   D. The trigger's semantics, all four cases: NULL -> value is allowed, the
#      SAME value again is allowed (re-claims are idempotent), a DIFFERENT value
#      aborts, and clearing to NULL aborts.
#   E. `task remove` no longer clears `sessions.task_id` — that write was never
#      asked for, it collides head-on with write-once, and retention (ED-1547)
#      leaves the task row in place so the pointer still resolves.
#      `session_statuses.task_id` is NOT write-once and IS still cleared.
#   F. The readers still resolve a session's task through the renamed column:
#      `session show`, `session list`, and `task show`'s session block.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1969
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Isolation: a throwaway git repo as project root under a temp dir, a temp
# XDG_CONFIG_HOME (its own DB) and XDG_CACHE_HOME, and the freshly-built worktree
# binary prepended to PATH. Sections B/C/D build their own standalone SQLite
# files under the same temp dir. No real DB, ledger, cache or worktree is
# touched, and nothing here launches Claude.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

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

# ─── shared fixture ──────────────────────────────────────────────────────────

WT=""; TMP=""; REPO=""; DBDIR=""; LAUNCH_MARKER=""; PROJ_ID=""

# E: the worktree's Python CLI against the isolated DB, from the repo dir.
E() { ( cd "$REPO" && uv run --project "$WT" endless "$@" 2>&1 ); }
# Q SQL: one scalar/row out of the isolated DB.
Q() { E sql "$1" --tsv 2>/dev/null; }

# PY FILE SQL...: run SQL against a standalone SQLite file, printing either the
# scalar result or "ERROR: <message>". Used by the schema sections, which are
# about SQLite's own behavior and want nothing between them and the engine.
PY() {
    local dbfile="$1"; shift
    python3 - "$dbfile" "$@" <<'PYEOF'
import sqlite3, sys
con = sqlite3.connect(sys.argv[1])
con.execute("PRAGMA foreign_keys=ON")
out = []
try:
    for stmt in sys.argv[2:]:
        cur = con.execute(stmt)
        rows = cur.fetchall()
        if rows:
            out.append("|".join("" if c is None else str(c) for c in rows[0]))
    con.commit()
except sqlite3.Error as e:
    print(f"ERROR: {e}")
    sys.exit(0)
print("\n".join(out))
PYEOF
}

# Ids are fixed so every assertion can name them literally.
T_HELD=9201       # the task the bound session holds
T_GONE=9202       # the task section E removes out from under that session
S_HELD=9801       # the session holding T_HELD

setup_fixture() {
    # -P: macOS's mktemp hands back /var/..., a symlink to /private/var/....
    TMP="$(cd "$(mktemp -d)" && pwd -P)"
    REPO="$TMP/repo"
    DBDIR="$TMP/xdg/endless"
    LAUNCH_MARKER="$TMP/claude-was-launched"
    export XDG_CONFIG_HOME="$TMP/xdg"
    export XDG_CACHE_HOME="$TMP/cache"

    mkdir -p "$TMP/stub" "$REPO" "$DBDIR" "$TMP/schema"
    cat > "$TMP/stub/claude" <<EOF
#!/bin/sh
echo "STUB CLAUDE LAUNCHED: \$*" >> "$LAUNCH_MARKER"
exit 0
EOF
    chmod +x "$TMP/stub/claude"
    export PATH="$TMP/stub:$WT/bin:$PATH"

    git -C "$REPO" init -q
    git -C "$REPO" symbolic-ref HEAD refs/heads/main
    git -C "$REPO" config user.email verify@test
    git -C "$REPO" config user.name verify
    git -C "$REPO" commit -q --allow-empty -m "initial commit"

    E project register "$REPO" --name probe --label Probe --desc d --lang Go --status active >/dev/null 2>&1

    PROJ_ID="$(Q "SELECT id FROM projects WHERE name='probe'")"
    [[ -n "$PROJ_ID" ]] || return 1

    E sql "INSERT INTO tasks (id, project_id, title, status, phase) VALUES
             ($T_HELD, $PROJ_ID, 'The held task',   'underway', 'now'),
             ($T_GONE, $PROJ_ID, 'The doomed task', 'ready',    'now')" --write >/dev/null 2>&1

    E sql "INSERT INTO sessions (id, session_id, project_id, state, kind_id, task_id) VALUES
             ($S_HELD, 'uuid-$S_HELD', $PROJ_ID, 'idle', 1, $T_HELD)" --write >/dev/null 2>&1

    [[ "$(Q "SELECT count(*) FROM tasks")" == "2" ]] || return 1
    [[ "$(Q "SELECT task_id FROM sessions WHERE id=$S_HELD")" == "$T_HELD" ]] || return 1
    return 0
}

# ─── section 0: the unit suites, fail-fast ───────────────────────────────────

test_suites_fail_fast() {
    section "0. Unit suites (fail-fast — nothing below means anything if these are red)"

    local out rc
    out=$(cd "$WT" && go test ./internal/... 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go test ./internal/... passes"
    else
        report_fail "go test ./internal/..." "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | grep -E '^(---|FAIL|\s+---)' | head -20)"
        return 1
    fi

    # The WHOLE Python suite, not a hand-picked subset: this task renamed a
    # column that the session and task command paths read everywhere, so any
    # subset would under-prove it.
    out=$(cd "$WT" && uv run pytest -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "uv run pytest (full suite) passes"
    else
        report_fail "uv run pytest (full suite)" "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -20)"
        return 1
    fi
    return 0
}

# ─── section A: the old names are gone from live code ────────────────────────

test_old_names_gone() {
    section "A. \`active_task_id\` / \`active_epic_id\` are gone from live code"

    local hits
    # Every remaining hit, minus the files that are supposed to keep the old
    # name. Anything else is a missed rename.
    hits="$(cd "$WT" && grep -rn 'active_task_id\|active_epic_id\|ActiveTaskID\|ActiveEpicID' \
              --include='*.go' --include='*.py' --include='*.sql' \
              internal/ src/ tests/ cmd/ 2>/dev/null \
            | grep -v '^internal/schema/changes/e-1568-' \
            | grep -v '^internal/schema/changes/e-1571-' \
            | grep -v '^internal/schema/changes/e-1929-' \
            | grep -v '^internal/schema/changes/e-1969-' \
            | grep -v '^internal/schema/schema.sql' \
            | grep -v '^src/endless/db.py' \
            | grep -v '^internal/events/claim_epic_test.go' \
            || true)"
    assert_eq "no stray old spellings in Go/Python/SQL" "" "$hits"

    # The identifiers that mirrored the column, swept in the same change.
    local ids
    ids="$(cd "$WT" && grep -rn 'ActiveTaskInfo\|ErrNoActiveTask\|GetActiveTaskForPane\|queryActiveTaskForPanes\|SessionActiveEpic\|activeTaskID\|activeEpicID\|"active_task"' \
              --include='*.go' --include='*.py' internal/ src/ tests/ cmd/ 2>/dev/null || true)"
    assert_eq "no stray active_-qualified identifiers or JSON keys" "" "$ids"

    # The two senses of `active` that are NOT this column, pinned so a future
    # sweep does not "finish the job" and take them with it.
    local kept
    kept="$(cd "$WT" && grep -c 'func GetActiveTasks' internal/monitor/task.go)"
    assert_eq "monitor.GetActiveTasks (a project's OPEN tasks) is untouched" "1" "$kept"
    kept="$(cd "$WT" && grep -c 'PaneStatusActive' internal/monitor/tmux_lookup.go)"
    if [[ "$kept" -gt 0 ]]; then
        report_pass "PaneStatusActive (the bar has something to show) is untouched"
    else
        report_fail "PaneStatusActive (the bar has something to show) is untouched" \
            "at least one PaneStatusActive" "$kept"
    fi

    # And the historical files that DO keep it are keeping it on purpose — a
    # future rename sweep that "cleans them up" would break the migration chain,
    # so pin that they still say the old name.
    local hist
    hist="$(cd "$WT" && grep -c 'active_task_id' internal/schema/changes/e-1568-sessions-short-id-and-nullable-session-id.go)"
    if [[ "$hist" -gt 0 ]]; then
        report_pass "e-1568's historical change still reproduces the OLD column name"
    else
        report_fail "e-1568's historical change still reproduces the OLD column name" \
            "at least one active_task_id" "$hist"
    fi
}

# ─── section B: a fresh database has the new shape ───────────────────────────

FRESH_DB=""

test_fresh_schema() {
    section "B. A fresh database (schema.sql) has the new columns and the trigger"

    FRESH_DB="$TMP/schema/fresh.db"
    python3 - "$FRESH_DB" "$WT/internal/schema/schema.sql" <<'PYEOF'
import sqlite3, sys, pathlib
con = sqlite3.connect(sys.argv[1])
con.executescript(pathlib.Path(sys.argv[2]).read_text())
con.commit()
PYEOF

    local cols
    cols="$(PY "$FRESH_DB" "SELECT group_concat(name) FROM pragma_table_info('sessions') WHERE name IN ('task_id','epic_id','active_task_id','active_epic_id')")"
    assert_eq "sessions has task_id + epic_id and neither old name" "task_id|epic_id" "${cols//,/|}"

    cols="$(PY "$FRESH_DB" "SELECT group_concat(name) FROM pragma_table_info('session_statuses') WHERE name IN ('task_id','active_task_id')")"
    assert_eq "session_statuses has task_id and not active_task_id" "task_id" "$cols"

    assert_eq "the write-once trigger exists" "sessions_task_id_write_once" \
        "$(PY "$FRESH_DB" "SELECT name FROM sqlite_master WHERE type='trigger' AND name='sessions_task_id_write_once'")"

    # The FK actions must have moved with the columns, not been dropped.
    # Sorted: pragma_foreign_key_list does not promise declaration order.
    assert_eq "sessions' FKs point at tasks through the NEW column names" "epic_id|task_id" \
        "$(PY "$FRESH_DB" "SELECT group_concat(f, '|') FROM (SELECT \"from\" AS f FROM pragma_foreign_key_list('sessions') WHERE \"table\"='tasks' ORDER BY 1)")"
}

# ─── section C: the change file migrates a real pre-rename database ──────────

test_migration() {
    section "C. The change file carries a pre-rename database across"

    local old_db="$TMP/schema/old.db"

    # Build the PRE-rename shape from the post-rename source, so this fixture
    # cannot drift from schema.sql the way a hand-copied old DDL would: apply
    # the current schema, drop the trigger, and rename the three columns BACK.
    python3 - "$old_db" "$WT/internal/schema/schema.sql" <<'PYEOF'
import sqlite3, sys, pathlib
con = sqlite3.connect(sys.argv[1])
con.executescript(pathlib.Path(sys.argv[2]).read_text())
con.execute("DROP TRIGGER IF EXISTS sessions_task_id_write_once")
con.execute("ALTER TABLE sessions RENAME COLUMN task_id TO active_task_id")
con.execute("ALTER TABLE sessions RENAME COLUMN epic_id TO active_epic_id")
con.execute("ALTER TABLE session_statuses RENAME COLUMN task_id TO active_task_id")
con.execute("INSERT INTO projects (id, name, path) VALUES (1, 'p', '/p')")
con.execute("INSERT INTO tasks (id, project_id, title, status) VALUES (7, 1, 'epic', 'underway')")
con.execute("INSERT INTO tasks (id, project_id, parent_id, title, status) VALUES (8, 1, 7, 'child', 'underway')")
con.execute("INSERT INTO sessions (id, session_id, project_id, state, active_task_id, active_epic_id)"
            " VALUES (5, 'uuid-5', 1, 'working', 8, 7)")
con.execute("INSERT INTO session_statuses (id, session_id, active_task_id, headline)"
            " VALUES (3, 5, 8, 'doing the thing')")
con.commit()
PYEOF
    assert_eq "fixture: the pre-rename DB really has the old column" "active_task_id" \
        "$(PY "$old_db" "SELECT name FROM pragma_table_info('sessions') WHERE name='active_task_id'")"

    # THE ORDERING. `endless db apply-change` opens the DB through monitor.DB(),
    # which applies schema.sql, BEFORE dispatching to the change script. Do that
    # here, against the OLD database, exactly as the real land does.
    python3 - "$old_db" "$WT/internal/schema/schema.sql" <<'PYEOF'
import sqlite3, sys, pathlib
con = sqlite3.connect(sys.argv[1])
con.executescript(pathlib.Path(sys.argv[2]).read_text())
con.commit()
PYEOF
    assert_eq "schema.sql survives meeting the pre-rename DB" "active_task_id" \
        "$(PY "$old_db" "SELECT name FROM pragma_table_info('sessions') WHERE name='active_task_id'")"

    # ...and it left a trigger naming a column that does not exist yet, which is
    # precisely what would abort the rename if the change file did not drop it
    # first. Prove the hazard is real rather than theoretical.
    assert_eq "schema.sql left a dangling write-once trigger on the old table" \
        "sessions_task_id_write_once" \
        "$(PY "$old_db" "SELECT name FROM sqlite_master WHERE type='trigger' AND name='sessions_task_id_write_once'")"
    local naive
    naive="$(PY "$old_db" "ALTER TABLE sessions RENAME COLUMN active_task_id TO task_id")"
    assert_contains "a rename WITHOUT dropping it first aborts (the trap being avoided)" \
        "no such column: OLD.task_id" "$naive"

    # Now the change file itself.
    local out rc
    out="$( cd "$WT" && ENDLESS_CHANGE_DB="$old_db" \
            go run internal/schema/changes/e-1969-rename-sessions-task-id.go 2>&1 )"; rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "the change file applies cleanly"
    else report_fail "the change file applies cleanly" "exit 0" "exit=$rc"$'\n'"$out"; fi

    assert_eq "sessions.active_task_id is gone" "" \
        "$(PY "$old_db" "SELECT group_concat(name) FROM pragma_table_info('sessions') WHERE name LIKE 'active_%'")"
    assert_eq "sessions.task_id + epic_id are there" "task_id|epic_id" \
        "$(PY "$old_db" "SELECT group_concat(name, '|') FROM pragma_table_info('sessions') WHERE name IN ('task_id','epic_id')")"
    assert_eq "session_statuses.task_id is there" "task_id" \
        "$(PY "$old_db" "SELECT group_concat(name) FROM pragma_table_info('session_statuses') WHERE name IN ('task_id','active_task_id')")"

    # Data, not just shape. A rename that silently dropped the values would pass
    # every structural check above.
    assert_eq "the session's task + epic values survived the rename" "8|7" \
        "$(PY "$old_db" "SELECT task_id || '|' || epic_id FROM sessions WHERE id=5")"
    assert_eq "the status snapshot's task value survived too" "8" \
        "$(PY "$old_db" "SELECT task_id FROM session_statuses WHERE id=3")"
    assert_eq "the FKs were rewritten to the new names" "epic_id|task_id" \
        "$(PY "$old_db" "SELECT group_concat(f, '|') FROM (SELECT \"from\" AS f FROM pragma_foreign_key_list('sessions') WHERE \"table\"='tasks' ORDER BY 1)")"
    assert_eq "the trigger is back after the rename" "sessions_task_id_write_once" \
        "$(PY "$old_db" "SELECT name FROM sqlite_master WHERE type='trigger' AND name='sessions_task_id_write_once'")"

    # Re-running it must be a no-op, not an error.
    out="$( cd "$WT" && ENDLESS_CHANGE_DB="$old_db" \
            go run internal/schema/changes/e-1969-rename-sessions-task-id.go 2>&1 )"; rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "a second run is a clean no-op"
    else report_fail "a second run is a clean no-op" "exit 0" "exit=$rc"$'\n'"$out"; fi

    # And it must be a no-op on a database that was BORN with the new names —
    # a fresh install, the sandbox, every test DB. Without the per-column probe
    # this is where `ALTER TABLE ... RENAME COLUMN` hard-errors.
    local born="$TMP/schema/born-new.db"
    cp "$FRESH_DB" "$born"
    out="$( cd "$WT" && ENDLESS_CHANGE_DB="$born" \
            go run internal/schema/changes/e-1969-rename-sessions-task-id.go 2>&1 )"; rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "no-op on a DB born with the new names"
    else report_fail "no-op on a DB born with the new names" "exit 0" "exit=$rc"$'\n'"$out"; fi
}

# ─── section D: the write-once trigger's four cases ──────────────────────────

test_write_once_trigger() {
    section "D. \`sessions.task_id\` is write-once (ED-1560)"

    local db="$TMP/schema/trigger.db"
    cp "$FRESH_DB" "$db"
    PY "$db" \
        "INSERT INTO projects (id, name, path) VALUES (1, 'p', '/p')" \
        "INSERT INTO tasks (id, project_id, title, status) VALUES (11, 1, 'a', 'ready')" \
        "INSERT INTO tasks (id, project_id, title, status) VALUES (12, 1, 'b', 'ready')" \
        "INSERT INTO sessions (id, session_id, project_id, state) VALUES (1, 'u1', 1, 'working')" \
        >/dev/null

    assert_eq "NULL -> a task id is allowed (the claim)" "11" \
        "$(PY "$db" "UPDATE sessions SET task_id = 11 WHERE id=1" "SELECT task_id FROM sessions WHERE id=1")"

    assert_eq "re-affirming the SAME id is allowed (re-claims are idempotent)" "11" \
        "$(PY "$db" "UPDATE sessions SET task_id = 11 WHERE id=1" "SELECT task_id FROM sessions WHERE id=1")"

    assert_contains "repointing to a DIFFERENT id aborts" "sessions.task_id is write-once" \
        "$(PY "$db" "UPDATE sessions SET task_id = 12 WHERE id=1")"
    assert_eq "...and the original binding is untouched" "11" \
        "$(PY "$db" "SELECT task_id FROM sessions WHERE id=1")"

    assert_contains "clearing to NULL aborts too" "sessions.task_id is write-once" \
        "$(PY "$db" "UPDATE sessions SET task_id = NULL WHERE id=1")"
    assert_eq "...and the binding is still there" "11" \
        "$(PY "$db" "SELECT task_id FROM sessions WHERE id=1")"

    # session_statuses is deliberately NOT write-once: a snapshot records
    # involvement at a moment, not ownership.
    PY "$db" "INSERT INTO session_statuses (id, session_id, task_id, headline) VALUES (1, 1, 11, 'h')" >/dev/null
    assert_eq "session_statuses.task_id is freely writable" "" \
        "$(PY "$db" "UPDATE session_statuses SET task_id = NULL WHERE id=1" "SELECT ifnull(task_id,'') FROM session_statuses WHERE id=1")"
}

# ─── section E: task removal leaves the session binding alone ────────────────

test_removal_keeps_binding() {
    section "E. \`task remove\` does not clear the session's task_id"

    # A status snapshot pointing at the doomed task, so the asymmetry below is
    # measured rather than assumed.
    E sql "INSERT INTO session_statuses (id, session_id, task_id, headline)
             VALUES (1, $S_HELD, $T_GONE, 'about the doomed task')" --write >/dev/null 2>&1

    # Bind the session to the task that is about to be removed. It already holds
    # T_HELD, and write-once forbids moving it — so use a SECOND session, which
    # is what one-session-one-task means in the first place.
    E sql "INSERT INTO sessions (id, session_id, project_id, state, kind_id, task_id)
             VALUES (9802, 'uuid-9802', $PROJ_ID, 'idle', 1, $T_GONE)" --write >/dev/null 2>&1
    assert_eq "precondition: session 9802 holds E-$T_GONE" "$T_GONE" \
        "$(Q "SELECT task_id FROM sessions WHERE id=9802")"

    local out
    out="$(E task remove "E-$T_GONE")"

    assert_eq "the task is marked removed, not deleted (ED-1547)" "1" \
        "$(Q "SELECT removed FROM tasks WHERE id=$T_GONE")"
    assert_eq "the removal did not abort on the write-once trigger" "" \
        "$(printf '%s' "$out" | grep -o 'write-once' || true)"
    assert_eq "the session still points at the removed task" "$T_GONE" \
        "$(Q "SELECT task_id FROM sessions WHERE id=9802")"
    assert_eq "...and the OTHER session's binding is untouched" "$T_HELD" \
        "$(Q "SELECT task_id FROM sessions WHERE id=$S_HELD")"
    assert_eq "session_statuses.task_id WAS cleared (not write-once)" "" \
        "$(Q "SELECT ifnull(task_id,'') FROM session_statuses WHERE id=1")"
}

# ─── section F: the readers still resolve a session's task ───────────────────

test_readers() {
    section "F. Readers still resolve a session's task through the renamed column"

    local out
    out="$(E session show "ES-$S_HELD")"
    assert_contains "\`session show\` names the held task" "E-$T_HELD" "$out"

    out="$(E session show "ES-$S_HELD" --json)"
    assert_contains "\`session show --json\` carries it under the \`task\` key" '"task":' "$out"
    assert_contains "...with the held task's id" "$T_HELD" "$out"

    # --all: a session with no messages is omitted from the default listing.
    out="$(E session list --all --json)"
    assert_contains "\`session list --json\` emits the task_id key" '"task_id"' "$out"
    assert_contains "...carrying the held task's id" "$T_HELD" "$out"

    # `task show`'s touch block reads session_tasks for WHICH sessions touched the
    # task, then joins sessions to append what each one is currently active on —
    # and that join is the renamed column. Seed the touch so the join has a row.
    E sql "INSERT INTO session_tasks (session_id, task_id, created_at, updated_at, relation_id)
             VALUES ($S_HELD, $T_HELD, '2026-08-01T00:00:00', '2026-08-01T00:00:00', 1)" --write >/dev/null 2>&1
    out="$(E task show "E-$T_HELD")"
    assert_contains "\`task show\` names the task the session holds, on its touch row" \
        "ES-$S_HELD (E-$T_HELD)" "$out"
}

# ─── section G: nothing launched Claude ──────────────────────────────────────

test_no_launch() {
    section "G. No check launched Claude"

    if [[ -f "$LAUNCH_MARKER" ]]; then
        report_fail "no check exec'd claude" "no launch" "$(cat "$LAUNCH_MARKER")"
    else
        report_pass "no check exec'd claude"
    fi
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    WT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -n "${WT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }

    command -v go >/dev/null      || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }
    command -v uv >/dev/null      || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    command -v git >/dev/null     || { printf 'ERROR: git not on PATH\n' >&2; exit 2; }
    command -v python3 >/dev/null || { printf 'ERROR: python3 not on PATH\n' >&2; exit 2; }
    [[ -x "${WT}/bin/endless-go" ]] || {
        printf 'ERROR: %s/bin/endless-go missing — run `just build`\n' "${WT}" >&2; exit 2; }

    printf '%sE-1969 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"

    if ! test_suites_fail_fast; then
        printf '\n%sUnit suites failed — stopping before the schema and CLI sections.%s\n\n' \
            "${RED}${BOLD}" "${RESET}"
        summary
        exit 1
    fi

    if ! setup_fixture; then
        printf 'ERROR: fixture setup failed (isolated DB seeding)\n' >&2
        [[ -n "$TMP" ]] && rm -rf "$TMP"
        exit 2
    fi

    test_old_names_gone
    test_fresh_schema
    test_migration
    test_write_once_trigger
    test_removal_keeps_binding
    test_readers
    test_no_launch

    [[ -n "$TMP" ]] && rm -rf "$TMP"
    summary
}

main "$@"
