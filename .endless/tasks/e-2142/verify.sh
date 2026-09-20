#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2142 and records what was true when E-2142
# landed. Edit it only if you ARE E-2142. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2142: retire four abandoned surfaces — the curated `next` list,
# `task import`, `tasks.source_file`, and session task ordering.
#
# WHAT LANDED
#   Each surface was a WRITER whose READER was gone or was never built:
#
#     * `endless task next revise` populated a five-table curated list.
#       `endless project next` — the command that would have displayed it —
#       does not exist and never did. A populated store nothing read.
#     * `endless task import` / `task import-json` filed tasks from a markdown
#       or JSON file and stamped tasks.source_file. Endless was first conceived
#       as a tool that imported and synced markdown files; storing the markdown
#       in the database replaced that, and the product no longer thinks in
#       source files.
#     * `endless session order` wrote session_tasks.do_order, read only by
#       `session status --tree` to override its DAG-derived order.
#
#   `endless task next` is a DIFFERENT command and stays. `session status
#   --tree` stays too — only its ordering override goes.
#
#   One part was not a deletion. The db-ledger is the permanent record, so it
#   still holds events of the three kinds those commands emitted. They are now
#   declared RETIRED: they validate, and they project nothing.
#
# THE CLAIMS (this suite checks these, not the plumbing)
#   C1  THE COMMANDS ARE GONE. `task next revise`, `task import`,
#       `task import-json` and `session order` refuse as unknown commands and
#       appear in no --help. `endless task next` still works.
#   C2  RETIREMENT IS DECLARED, NOT INFERRED. A ledger holding all three
#       retired kinds replays clean and does not stop at them; a kind nobody
#       declared still fails loudly with the unknown-kind error. Both halves,
#       because either alone is satisfiable by the wrong implementation.
#   C3  THE SCHEMA DROPPED, BOTH WAYS. Five tables and two columns are gone
#       from the declared shape AND from a database goose builds from scratch,
#       and the drop is a no-op against a database that already lacks them —
#       the property that made it a Go migration rather than SQL.
#   C4  THE TREE SURVIVED, DAG-ONLY. `session status --tree` still renders,
#       and nothing in its path reads or mentions do_order.
#   C5  source_file IS UNREADABLE AND UNWRITTEN. `task show` prints no
#       `Source:` line, the JSON shape carries no `source_file` key, and
#       neither the executor nor the projector names it in an INSERT.
#   C6  NOTHING STILL CALLS THE REMOVED MACHINERY. The Go and Python symbols
#       that backed these surfaces are absent, not merely unreferenced.
#
# ISOLATION
#   Nothing here touches the main database or the real ledger. Every check is
#   a package test, a source-level read, or a query against a throwaway SQLite
#   file built in a scratch directory outside the repository.
#
# Layers:
#   A. FAIL-FAST — this task's own unit tests, Go and Python. Nothing below is
#      meaningful if the behaviour they pin is broken.
#   B. The commands refuse — C1.
#   C. Retirement is declared — C2.
#   D. The schema, against a real probe database — C3.
#   E. The tree kept its command and lost its override — C4.
#   F. source_file has no reader and no writer — C5.
#   G. The machinery is gone from the tree — C6.
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

# go_test runs one named set and reports it as a single line.
go_test() {
    local label="$1" pkg="$2" run="$3"
    # Assigned on its own line, not folded into the `local` above: the builtin
    # expands all of its arguments before it assigns any of them, so a later
    # word referring to an earlier one reads it as unset (and dies under -u).
    local out="${TMP}/go-$(printf '%s' "${run}" | tr -c 'A-Za-z0-9' '_').txt"
    if go test "${pkg}" -run "${run}" >"${out}" 2>&1; then
        report_pass "${label}"
    else
        report_fail "${label}" "go test passes" "$(tail -20 "${out}")"
    fi
}

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# Run first: they are the detailed proof, and everything below is the
# claim-level statement of what they establish.

go_test "Go: retired kinds validate, undeclared ones do not (internal/events)" \
    ./internal/events/ 'TestRetiredKinds|TestValidate_RetiredKindsPassAndUndeclared|TestValidKinds_Count'
go_test "Go: a ledger of retired kinds replays as declared no-ops (internal/events)" \
    ./internal/events/ 'TestProjectToTempDB_RetiredKindsReplayAsDeclaredNoOps'
go_test "Go: migrated and declared databases still agree (internal/schema)" \
    ./internal/schema/ 'TestMigrate_'
go_test "Go: the tree derives its order from the DAG alone (internal/sessionstatuscmd)" \
    ./internal/sessionstatuscmd/ 'TestBuildForest|TestBuildSpine'

py_test() {
    local label="$1" target="$2"
    local out="${TMP}/py-$(printf '%s' "${target}" | tr -c 'A-Za-z0-9' '_').txt"
    if uv run pytest "${target}" -q >"${out}" 2>&1; then
        report_pass "${label}"
    else
        report_fail "${label}" "pytest passes" "$(tail -20 "${out}")"
    fi
}

py_test "Python: one removal path, one guard (tests/test_task_remove_relations.py)" \
    tests/test_task_remove_relations.py
py_test "Python: no command carries a value-bearing --json (tests/test_output_format.py)" \
    tests/test_output_format.py

# ---------------------------------------------------------------------------
section "B. The commands refuse — C1"
# ---------------------------------------------------------------------------
# Driven through the real Click group rather than a built binary: the claim is
# about what the CLI EXPOSES, and the group is where exposure is decided. A
# subprocess would additionally depend on which endless is on PATH, which is
# the one thing a verify run must not be sensitive to.

cat >"${TMP}/probe.py" <<'PY'
import json, sys
from click.testing import CliRunner
from endless.cli import main

r = CliRunner()
out = {"refusals": {}, "help": {}}
for argv in (["task", "import", "x"],
             ["task", "import-json", "x"],
             ["task", "next", "revise", "--file", "x"],
             ["session", "order", "E-1"]):
    res = r.invoke(main, argv)
    out["refusals"][" ".join(argv)] = res.output
for group in (["task", "--help"], ["task", "next", "--help"], ["session", "--help"]):
    out["help"][" ".join(group)] = r.invoke(main, group).output
out["next_works"] = r.invoke(main, ["task", "next", "--help"]).exit_code == 0
json.dump(out, sys.stdout)
PY
PROBE="$(uv run python "${TMP}/probe.py" 2>"${TMP}/probe-err.txt")" \
    || setup_error "cannot drive the CLI: $(head -5 "${TMP}/probe-err.txt")"

refusal() { printf '%s' "${PROBE}" | python3 -c \
    'import json,sys; print(json.load(sys.stdin)["refusals"][sys.argv[1]])' "$1"; }
helptext() { printf '%s' "${PROBE}" | python3 -c \
    'import json,sys; print(json.load(sys.stdin)["help"][sys.argv[1]])' "$1"; }

assert_contains "\`task import\` is not a command"       "No such command" "$(refusal 'task import x')"
assert_contains "\`task import-json\` is not a command"  "No such command" "$(refusal 'task import-json x')"
assert_contains "\`task next revise\` is not a command"  "No such command" "$(refusal 'task next revise --file x')"
assert_contains "\`session order\` is not a command"     "No such command" "$(refusal 'session order E-1')"

# Absent from help, not merely hidden: a hidden command still runs.
assert_not_contains "\`task --help\` offers no import"   "import" "$(helptext 'task --help')"
assert_not_contains "\`task next --help\` offers no revise" "revise" "$(helptext 'task next --help')"
assert_not_contains "\`session --help\` offers no order" $'\n  order' "$(helptext 'session --help')"

# The one command that shares a name with a retired one and is unrelated.
assert_eq "\`task next\` itself still works" "True" \
    "$(printf '%s' "${PROBE}" | python3 -c 'import json,sys; print(json.load(sys.stdin)["next_works"])')"

# ---------------------------------------------------------------------------
section "C. Retirement is declared, not inferred — C2"
# ---------------------------------------------------------------------------
# The behaviour is asserted by the Go tests in layer A. What is asserted HERE is
# the shape of the declaration, because the cheap way to survive replay — ignore
# every kind the projector does not recognise — passes a behavioural test of the
# first half and silently throws away the second.

EVENT_GO="$(cat internal/events/event.go)"
assert_contains "a named retired-kinds registry exists" "RetiredKinds = map[Kind]bool{" "${EVENT_GO}"
for kind in task.bulk_cleared session_tasks.ordered project_next.revised; do
    assert_contains "${kind} is named as retired" "Kind = \"${kind}\"" "${EVENT_GO}"
done

# Disjoint from ValidKinds is what keeps a retired kind UNEMITTABLE: the
# executor and the `event emit` front door both gate on ValidKinds alone.
assert_not_contains "task.bulk_cleared is not emittable" \
    "KindTaskBulkCleared:   true" "${EVENT_GO}"
assert_not_contains "session_tasks.ordered is not emittable" \
    "KindSessionTasksOrdered: true," "$(sed -n '/ValidKinds = map/,/^}/p' internal/events/event.go)"
assert_contains "the emit front door still gates on ValidKinds alone" \
    "if !events.ValidKinds[evtKind] {" "$(cat internal/eventcmd/event.go)"

# The projector says "declared no-op" where it would otherwise say nothing —
# the difference between a decision and an accident.
assert_contains "the projector handles retired kinds explicitly" \
    "if RetiredKinds[evt.Kind] {" "$(cat internal/events/projector.go)"

# ---------------------------------------------------------------------------
section "D. The schema, against a real probe database — C3"
# ---------------------------------------------------------------------------
# Built from the DECLARED schema, not a hand-rolled fragment: the claim is about
# what a database HAS, and only the real schema can answer that. The goose side
# of the same claim — that a database built from the migration set agrees — is
# TestMigrate_MatchesSchemaSQL in layer A, which is also the end-to-end proof
# that the drop RUNS (goose's baseline creates all seven objects, and 00003
# removes them again).

PROBE_DB="${TMP}/declared.db"
sqlite3 "${PROBE_DB}" < internal/schema/schema.sql >/dev/null 2>"${TMP}/schema-err.txt" \
    || setup_error "cannot build a probe database from schema.sql: $(head -3 "${TMP}/schema-err.txt")"

for table in project_next project_next_lanes project_next_tasks \
             project_next_pending project_next_events; do
    assert_eq "table ${table} is gone" "0" \
        "$(sqlite3 "${PROBE_DB}" \
            "SELECT count(*) FROM sqlite_master WHERE name = '${table}'")"
done

assert_eq "tasks.source_file is gone" "0" \
    "$(sqlite3 "${PROBE_DB}" "SELECT count(*) FROM pragma_table_info('tasks') WHERE name = 'source_file'")"
assert_eq "session_tasks.do_order is gone" "0" \
    "$(sqlite3 "${PROBE_DB}" "SELECT count(*) FROM pragma_table_info('session_tasks') WHERE name = 'do_order'")"

# The indexes went with their tables rather than outliving them.
assert_eq "no project_next index survives" "0" \
    "$(sqlite3 "${PROBE_DB}" \
        "SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name LIKE '%project_next%'")"

# The drop is idempotent BY PROBE, which is why it is a Go step: SQLite has no
# DROP COLUMN IF EXISTS, and a database built straight from schema.sql (this one,
# and every test that does the same) reaches the migration already lacking the
# columns.
MIGRATION="internal/schema/migrations/00003_retire_curated_next_import_and_order.go"
[[ -f "${MIGRATION}" ]] || setup_error "the migration is missing: ${MIGRATION}"
assert_contains "the column drop probes before dropping" \
    "pragma_table_info(?)" "$(cat "${MIGRATION}")"
assert_contains "both halves of the set are registered with goose" \
    "goose.WithGoMigrations(migrations.Go()...)" "$(cat internal/schema/migrate.go)"
# A Go step that LatestVersion could not see would report a database ahead of
# its own binary — E-2020's "something is wrong" signal, fired by a counting bug.
assert_contains "LatestVersion counts the Go half too" \
    "for _, m := range migrations.Go() {" "$(cat internal/schema/migrate.go)"

# ---------------------------------------------------------------------------
section "E. The tree kept its command and lost its override — C4"
# ---------------------------------------------------------------------------
# `session status --tree` was explicitly NOT in scope for removal. Only its
# explicit-ordering override was, so the surviving behaviour is what the tree
# already rendered for every session that never ran `session order`.

TREE_GO="$(cat internal/sessionstatuscmd/tree.go)"
TREE_SRC="$(grep -v '^[[:space:]]*//' internal/sessionstatuscmd/tree.go)"

assert_contains "the tree still builds a forest" "func buildForest(" "${TREE_GO}"
assert_contains "the tree still derives from the blocked-by DAG" "assignByDAG(" "${TREE_SRC}"
# Comments stripped: this file explains at length what it no longer does, and a
# grep that counted those sentences would fail on a correct tree.
assert_not_contains "the layering override is gone" "assignByLayer" "${TREE_SRC}"
assert_not_contains "the tree reads no do_order" "doOrder" "${TREE_SRC}"
assert_not_contains "the do_order reader is gone from monitor" \
    "func SessionStatusDoOrder" "$(cat internal/monitor/session_status.go)"

# `--tree` itself is still an option a user can pass.
assert_contains "\`session status --tree\` is still offered" "--tree" \
    "$(printf '%s' "${PROBE}" | python3 -c \
        'import json,sys; print(json.load(sys.stdin)["help"]["session --help"])' >/dev/null 2>&1 \
        && uv run python -c 'from click.testing import CliRunner; from endless.cli import main; print(CliRunner().invoke(main, ["session","status","--help"]).output)')"

# ---------------------------------------------------------------------------
section "F. source_file has no reader and no writer — C5"
# ---------------------------------------------------------------------------
# A dropped column is only safely dropped when nothing still names it in SQL.
# Both INSERT sites are checked, because the executor (live writes) and the
# projector (ledger replay) are separate statements that have drifted before.

assert_not_contains "the executor does not write source_file" \
    "source_file" "$(grep -v '^[[:space:]]*//' internal/events/executor.go)"
assert_not_contains "the projector does not write source_file" \
    "source_file" "$(grep -v '^[[:space:]]*//' internal/events/projector.go)"
assert_not_contains "no Python read of source_file remains" \
    "source_file" "$(grep -v '^[[:space:]]*#' src/endless/task_cmd.py)"

# The payload FIELD is deliberately retained — historical task.imported events
# carry the key — and retaining it is only safe because nothing reads the value.
assert_contains "the payload keeps the field for historical events" \
    'SourceFile  string `json:"source_file,omitempty"`' "$(cat internal/events/payload.go)"

# ---------------------------------------------------------------------------
section "G. The machinery is gone from the tree — C6"
# ---------------------------------------------------------------------------
# Absent, not merely unreferenced. Dead code that still compiles is the state
# these surfaces were already in — a writer with no reader — so "nothing calls
# it" is the wrong bar for this particular task.

for f in internal/events/project_next.go \
         internal/events/project_next_pending.go \
         src/endless/session_order_cmd.py \
         tests/test_session_order.py \
         tests/test_task_next_revise.py; do
    if [[ -e "${f}" ]]; then
        report_fail "${f} is deleted" "no such file" "still present"
    else
        report_pass "${f} is deleted"
    fi
done

# The bulk-clear removal went with the column it enumerated by. It cannot come
# back without the column, which is the point of dropping both in one step.
assert_not_contains "removeTasksBySourceFile is gone" \
    "func removeTasksBySourceFile" "$(cat internal/events/task_removal.go)"
assert_not_contains "the ordering executor is gone" \
    "func execSessionTasksOrdered" "$(cat internal/events/session_tasks.go)"
# …while the helper it shared with the SURVIVING membership verbs stays.
assert_contains "the shared task-id parser survives" \
    "func parseTaskDisplayID" "$(cat internal/events/session_tasks.go)"

# The auto-import hook path shelled `endless task import`. It had no callers,
# and leaving it would have left a hook shelling a command that no longer exists.
assert_not_contains "the auto-import hook path is gone" \
    "func autoImportTask" "$(cat internal/hookcmd/claude.go)"

# The SessionStart empty-state hint named `task import` to a first-time user.
assert_contains "the empty-state hint names a command that exists" \
    "endless task add" "$(cat internal/monitor/task.go)"

summary
