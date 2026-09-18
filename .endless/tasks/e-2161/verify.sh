#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2161 and records what was true when E-2161
# landed. Edit it only if you ARE E-2161. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2161: removing a task retains its children's parent_id; reads re-root
# through a new `task_tree` view.
#
# WHAT LANDED
#   ED-1547 (E-1929) stopped DELETEing a removed task's row so an id could
#   never be re-minted. The row survived; the EDGE did not. Non-cascade
#   removal and `task import --replace` both ran
#   `UPDATE tasks SET parent_id = NULL WHERE parent_id = ?`, mirroring the
#   ON DELETE SET NULL the hard-delete path had used.
#
#   That null was load-bearing for exactly one thing: every tree read joins
#   live_tasks to live_tasks, so a LIVE child left pointing at a hidden row
#   would hang off nothing and vanish from `task list`, `task show --children`
#   and the monitors. For a child that was ITSELF already removed — the case the
#   `task remove` guard leaves reachable, since it refuses a non-cascade
#   removal whose children are live — it protected no render at all and
#   simply destroyed the association, unrecoverably: there is no restore
#   verb, so nothing in the live database can reconstruct one.
#
#   Both statements are gone. A new view, `task_tree`, is live_tasks plus
#   `effective_parent_id` — the nearest ancestor with removed = 0, walked
#   with a recursive CTE, NULL when every ancestor is removed — and the
#   reads that BUILD A TREE or COUNT CHILDREN were pointed at it. A schema
#   change reconstructs already-nulled edges from the ledger, which still
#   carries them because the nulling emitted no event of its own.
#
# THE CLAIMS (this suite checks these, not the plumbing)
#   C1  THE NULLING IS GONE. Neither removal path writes parent_id, in the
#       live executor or in the projector — they are one function, which is
#       the property internal/events/task_removal.go exists to hold.
#   C2  THE VIEW ANSWERS THE QUESTION. task_tree exposes
#       effective_parent_id: the nearest live ancestor, NULL when there is
#       none (a removed chain, a dangling id, a removed cycle), and it holds
#       exactly live_tasks' rows — a superset in COLUMNS, not in rows.
#   C3  THE RENDER SURVIVED. A live child of a removed parent appears under
#       its grandparent in `task show --children`, in `task list --parent`
#       and in `task next`, and does not disappear or jump to the root.
#   C4  THE SPLIT HELD. `parent_id` still answers "what did the user set" —
#       the `Parent:` line, the removal guard, `task move --children-of` and
#       the cycle validation all still read it. Only "where does this
#       render" moved.
#   C5  THE BACKFILL READS THE LEDGER AND IS IDEMPOTENT. It restores an edge
#       the ledger still names, leaves a task deliberately re-rooted with
#       `task move --root` alone, and changes nothing on a second run.
#   C6  SCHEMA PARITY. schema.sql and the goose migration set build the same
#       database — the assertion that keeps the two spellings of this view
#       from drifting.
#
# ISOLATION
#   Nothing here touches the main database or the real ledger. Every check
#   is a package test, or runs against a throwaway SQLite file and a ledger
#   written into a scratch directory outside the repository.
#
# Layers:
#   A. FAIL-FAST — this task's own unit tests, Go and Python. Nothing below
#      is meaningful if the behaviour they pin is broken.
#   B. The nulling is gone from the source of both paths — C1.
#   C. The view's contract, exercised against the declared schema — C2, C6.
#   D. The render, claim by claim — C3.
#   E. The boundary: what deliberately did NOT move — C4.
#   F. The backfill against a ledger of its own — C5.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 on any
# failure, 2 on a setup problem.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

# Outside the repository on purpose: a probe database inside .endless/ is the
# 0-byte ghost file `endless worktree land` refuses over.
TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# Run first: they are the detailed proof, and everything below is the
# claim-level statement of what they establish.

if go test ./internal/events/ -run 'TestRemoveTask|TestTaskTree|TestEpicDerivation_Counts' \
        >"${TMP}/go-events.txt" 2>&1; then
    report_pass "Go: removal retains the edge, and the view re-roots (internal/events)"
else
    report_fail "Go: removal retains the edge, and the view re-roots (internal/events)" \
        "go test passes" "$(tail -20 "${TMP}/go-events.txt")"
fi

# C6 lives here: TestMigrate_MatchesSchemaSQL builds one database from
# schema.sql and one from the migration set and requires them identical, which
# is what stops this view's two spellings from drifting apart.
if go test ./internal/schema/ >"${TMP}/go-schema.txt" 2>&1; then
    report_pass "Go: schema.sql and the migration set build the same database"
else
    report_fail "Go: schema.sql and the migration set build the same database" \
        "go test passes" "$(tail -20 "${TMP}/go-schema.txt")"
fi

# ---------------------------------------------------------------------------
section "B. The nulling is gone — C1"
# ---------------------------------------------------------------------------
# Source-level, and deliberately so: the claim is that NO code path writes
# parent_id during a removal. A behavioural test can only prove the paths it
# thought to call; this proves there is nothing left to call. One function
# serves both the executor and the projector, so one file is the whole surface.

# Comments stripped first: the file explains at length what it no longer does,
# and a grep that counted those sentences would fail on a correct tree while
# passing on one where someone deleted the explanation and kept the statement.
REMOVAL="$(grep -v '^[[:space:]]*//' internal/events/task_removal.go)"
assert_not_contains "removal never nulls parent_id" "parent_id = NULL" "${REMOVAL}"
assert_not_contains "removal never writes parent_id at all" "SET parent_id" "${REMOVAL}"

# The shared-implementation property the nulling used to live inside. If the
# projector grew a removal of its own, a rebuild could disagree with the live
# path about what removal means — the failure ED-1547 exists to close, and the
# reason this is one function rather than two.
assert_contains "the projector still replays removal through the shared function" \
    "removeTaskTree" "$(cat internal/events/projector.go)"

# ---------------------------------------------------------------------------
section "C. The view answers 'where does this render' — C2"
# ---------------------------------------------------------------------------
# Against a database built from the DECLARED schema, not a hand-rolled tasks
# table: a view resolves its column names lazily, so only the real schema
# proves this one resolves at all.

PROBE="${TMP}/view.db"
sqlite3 "${PROBE}" < internal/schema/schema.sql >/dev/null 2>"${TMP}/schema-err.txt" \
    || setup_error "cannot build a probe database from schema.sql: $(head -3 "${TMP}/schema-err.txt")"

sqlite3 "${PROBE}" >/dev/null 2>"${TMP}/seed-err.txt" <<'SQL' || setup_error "cannot seed the probe tree: $(head -3 "${TMP}/seed-err.txt")"
INSERT INTO projects (id, name, path) VALUES (1, 'e-2161-probe', '/tmp/e-2161-probe');
INSERT INTO tasks (id, project_id, title, phase, status, parent_id, removed) VALUES
  (1,  1, 'root',           'now', 'ready', NULL, 0),
  (2,  1, 'removed middle', 'now', 'ready', 1,    1),
  (3,  1, 'adopted child',  'now', 'ready', 2,    0),
  (4,  1, 'ordinary child', 'now', 'ready', 3,    0),
  (5,  1, 'removed root',   'now', 'ready', NULL, 1),
  (6,  1, 'stranded',       'now', 'ready', 5,    0),
  (7,  1, 'removed a',      'now', 'ready', 1,    1),
  (8,  1, 'removed b',      'now', 'ready', 7,    1),
  (9,  1, 'two hops up',    'now', 'ready', 8,    0);
SQL

# A dangling parent_id is written with foreign keys off — which is also the
# only way a real one arose, under a connection that predates enforcement.
sqlite3 "${PROBE}" >/dev/null 2>&1 <<'SQL'
PRAGMA foreign_keys=OFF;
INSERT INTO tasks (id, project_id, title, phase, status, parent_id, removed)
VALUES (10, 1, 'dangling', 'now', 'ready', 9999, 0);
SQL

eff() { sqlite3 "${PROBE}" "SELECT COALESCE(effective_parent_id, 'NULL') FROM task_tree WHERE id = $1"; }

assert_eq "a true root has no effective parent"                            "NULL" "$(eff 1)"
assert_eq "a live child of a removed parent is adopted by its grandparent" "1"    "$(eff 3)"
assert_eq "an ordinary edge is untouched"                                  "3"    "$(eff 4)"
assert_eq "no live ancestor at all means it renders at the root"           "NULL" "$(eff 6)"
assert_eq "the walk crosses more than one removed ancestor"                "1"    "$(eff 9)"
assert_eq "a dangling parent_id renders at the root rather than vanishing" "NULL" "$(eff 10)"

# The literal column is untouched: the view derives, it does not rewrite.
assert_eq "parent_id itself is unchanged by the view" "2" \
    "$(sqlite3 "${PROBE}" 'SELECT parent_id FROM tasks WHERE id = 3')"

# A superset in COLUMNS, not in rows: every converted reader swapped
# live_tasks for task_tree, and must not have gained a removed row by doing so.
assert_eq "task_tree holds exactly live_tasks' rows" \
    "$(sqlite3 "${PROBE}" 'SELECT count(*) FROM live_tasks')" \
    "$(sqlite3 "${PROBE}" 'SELECT count(*) FROM task_tree')"

# ---------------------------------------------------------------------------
section "D. The render, claim by claim — C3"
# ---------------------------------------------------------------------------
# Each render path gets its own line rather than one "the tests passed", so a
# failure names the surface that broke. These drive the real render functions
# (task_cmd.detail_item / show_plan / next_tasks) against an isolated config,
# which is what the runner's temp HOME and XDG_CONFIG_HOME already provide.

render_check() {
    local label="$1" test_name="$2"
    if uv run pytest "tests/test_removed_parent_rerooting.py::${test_name}" -q \
            >"${TMP}/py-${test_name}.txt" 2>&1; then
        report_pass "${label}"
    else
        report_fail "${label}" "pytest passes" "$(tail -15 "${TMP}/py-${test_name}.txt")"
    fi
}

render_check "task show --children lists the adopted child (human)" \
    test_human_children_show_the_adopted_child
render_check "task show --children lists it in JSON, and the count agrees" \
    test_json_children_show_the_adopted_child
render_check "task show --children lists it in agent mode" \
    test_agent_children_show_the_adopted_child
render_check "task list --parent finds it under the grandparent" \
    test_list_filters_by_where_the_task_renders
render_check "task list --parent none finds a task with no live ancestor" \
    test_list_parent_none_means_shown_at_the_root
render_check "task next treats the adopted child as the leaf" \
    test_next_treats_the_adopted_child_as_the_leaf

# ---------------------------------------------------------------------------
section "E. The boundary — what deliberately did NOT move — C4"
# ---------------------------------------------------------------------------
# The split this change rests on: parent_id answers "what did the user set",
# effective_parent_id answers "where does this render". A conversion that ran
# over the writes and the guards too would have been a different, wrong change,
# and these are the sites where telling the two apart mattered.

render_check "the Parent: line still reports the literal parent" \
    test_parent_line_still_reports_the_literal_parent
render_check "task list --removed still filters on the literal parent" \
    test_removed_listing_still_reads_the_literal_parent

TASKCMD="$(cat src/endless/task_cmd.py)"

# `task move --children-of` previews an UPDATE that matches on parent_id, so it
# must count exactly the rows it will write, not the wider set that merely
# renders under the task.
assert_contains "task move --children-of still writes and counts by parent_id" \
    "UPDATE tasks SET parent_id = ? WHERE parent_id = ?" "${TASKCMD}"

# The removal guard asks "does removing this strand LIVE children", which only
# a literal child can be.
assert_contains "the remove guard still counts literal children" \
    "SELECT count(*) FROM live_tasks WHERE parent_id = ?" "${TASKCMD}"

# `task list --removed` renders removed rows, which task_tree excludes by
# construction, so it forks back to `tasks` and the literal column.
assert_contains "task list --removed forks back to tasks and parent_id" \
    'parent_col = "parent_id" if removed_only else "effective_parent_id"' "${TASKCMD}"

# Cycle validation is a write-time guard over the column being written.
assert_contains "the cycle guard still walks parent_id" \
    "SELECT parent_id FROM tasks WHERE id = ?" "$(cat internal/events/parent_cycle.go)"

# ---------------------------------------------------------------------------
section "F. The backfill, against a ledger of its own — C5"
# ---------------------------------------------------------------------------
# A database whose edge was nulled the way removal used to null it, beside a
# ledger that still names the parent. The change has to restore the one the
# ledger still vouches for, leave a deliberate root alone, and do nothing on a
# second run.

BF="${TMP}/backfill"
mkdir -p "${BF}/proj/.endless/db-ledger"
sqlite3 "${BF}/endless.db" < internal/schema/schema.sql >/dev/null \
    || setup_error "cannot build the backfill probe database"
sqlite3 "${BF}/endless.db" >/dev/null 2>"${TMP}/bf-err.txt" <<SQL || setup_error "cannot seed the backfill probe: $(head -3 "${TMP}/bf-err.txt")"
INSERT INTO projects (id, name, path) VALUES (1, 'e2161bf', '${BF}/proj');
INSERT INTO tasks (id, project_id, title, phase, status, parent_id, removed) VALUES
  (1, 1, 'parent',           'now', 'ready', NULL, 1),
  (2, 1, 'orphaned by null', 'now', 'ready', NULL, 1),
  (3, 1, 'moved to root',    'now', 'ready', NULL, 0),
  (4, 1, 'never parented',   'now', 'ready', NULL, 0);
SQL

# Task 2 was created under 1 and the nulling emitted no event, so the ledger
# still says so. Task 3 was created under 1 and then deliberately re-rooted, so
# its LAST word is NULL. Task 4 was created at the root and never moved.
cat >"${BF}/proj/.endless/db-ledger/db-entries-probe-000001.jsonl" <<'LEDGER'
{"v":1,"ts":"5A000000000001","kind":"task.created","project":"e2161bf","entity":{"type":"task","id":"1"},"actor":{"kind":"cli","id":"probe"},"payload":{"title":"parent","phase":"now","status":"ready","type":"todo","sort_order":0}}
{"v":1,"ts":"5A000000000002","kind":"task.created","project":"e2161bf","entity":{"type":"task","id":"2"},"actor":{"kind":"cli","id":"probe"},"payload":{"title":"orphaned by null","phase":"now","status":"ready","type":"todo","sort_order":0,"parent_id":1}}
{"v":1,"ts":"5A000000000003","kind":"task.created","project":"e2161bf","entity":{"type":"task","id":"3"},"actor":{"kind":"cli","id":"probe"},"payload":{"title":"moved to root","phase":"now","status":"ready","type":"todo","sort_order":0,"parent_id":1}}
{"v":1,"ts":"5A000000000004","kind":"task.moved","project":"e2161bf","entity":{"type":"task","id":"3"},"actor":{"kind":"cli","id":"probe"},"payload":{"old_parent_id":1,"new_parent_id":null}}
{"v":1,"ts":"5A000000000005","kind":"task.created","project":"e2161bf","entity":{"type":"task","id":"4"},"actor":{"kind":"cli","id":"probe"},"payload":{"title":"never parented","phase":"now","status":"ready","type":"todo","sort_order":0}}
LEDGER

run_backfill() {
    ENDLESS_CHANGE_DB="${BF}/endless.db" \
        go run ./internal/schema/changes/e-2161-restore-nulled-parents.go 2>&1
}

FIRST="$(run_backfill)" || setup_error "the backfill failed: ${FIRST}"
assert_contains "the backfill reports what it restored" "1 parent edge(s) restored" "${FIRST}"
# Three, not one: the ledger's last word is "root" for the re-rooted task AND
# for the two the ledger only ever saw created at the root. Both are the same
# answer to the same question — the ledger says this task has no parent — and
# the count is of that answer, not of one way of arriving at it.
assert_contains "and counts every task the ledger puts at the root" \
    "3 left at root (deliberate)" "${FIRST}"
assert_eq "the edge the ledger still names is restored" "1" \
    "$(sqlite3 "${BF}/endless.db" 'SELECT parent_id FROM tasks WHERE id = 2')"
assert_eq "a task deliberately moved to the root is left alone" "" \
    "$(sqlite3 "${BF}/endless.db" "SELECT COALESCE(parent_id, '') FROM tasks WHERE id = 3")"
assert_eq "a task that never had a parent is left alone" "" \
    "$(sqlite3 "${BF}/endless.db" "SELECT COALESCE(parent_id, '') FROM tasks WHERE id = 4")"

# The _schema_version marker gates the re-run, so a second invocation is a
# no-op that says so rather than a second pass over the same rows.
SECOND="$(run_backfill)" || setup_error "the second backfill run failed: ${SECOND}"
assert_contains "a second run is refused by its own marker" "already applied" "${SECOND}"
assert_eq "and the restored edge is unchanged" "1" \
    "$(sqlite3 "${BF}/endless.db" 'SELECT parent_id FROM tasks WHERE id = 2')"

summary
