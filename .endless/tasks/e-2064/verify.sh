#!/usr/bin/env bash
#
# E-2064 verification — the supersession note is a DETAIL annotation, not a
# column.
#
# Before: E-1956 put ` (replaced by E-NNN)` beside a terminal status, and E-1185
# added ` (duplicates E-NNN)` on the same rule. Both reached the human TABLES,
# where a status is not a value on its own line but a cell in a column shared by
# every row. The column is sized by its longest cell, so
# 'obsolete (replaced by E-1367)' — 29 columns against 8 for a bare status —
# was charged to every row's Title. Four annotated children truncated every
# title in `task show E-799 --children`.
#
# After: the human tables render the bare status. `task show`, `session status`
# and every --llm/--json mode keep the note, because none of them pays a shared
# width for it. `decision list`'s sibling ' (by ED-NNN)' annotation had the
# identical defect and is folded in here.
#
# This is NOT an undo of E-1956. The fact still travels with the status
# everywhere the status has room for it; it stops travelling only where it was
# billing every other row.
#
# Run from inside the worktree (esu puts you there):
#   esu && ./tests/tasks/e-2064-verify.sh
#
# What it proves:
#   1. FAIL-FAST unit gate: the Python suites for both relations and for the
#      column-width invariant, plus the Go `session status` suites this change
#      deliberately leaves alone. Everything below runs the same code, so a red
#      gate makes it noise.
#   2. Adding the relation does not change `task list`'s human output BY ONE
#      BYTE — the row set is identical across both renders, so the diff is the
#      relation's cost and it is zero. Plus the two consequences stated
#      directly: the Status rule is as wide as the widest bare status, and the
#      long title that used to truncate is rendered whole.
#   3. Every command that renders that table is covered, not just the one the
#      bug was filed against: list, recent, search, next, and show --children.
#   4. `task show` still carries both notes, and --llm/--json still carry the
#      relation — including ungated on a non-terminal status, which is what
#      makes this a display change rather than a data change.
#   5. `decision list` pays nothing either, while `decision show` and
#      `decision list --llm`/`--json` keep naming the successor.
#   6. `session status` is untouched: it has no Status column at all, appends
#      the note past the title and charges it to that row's own budget.
#   7. Source shape, so the column cannot quietly re-widen: the two table
#      renderers no longer reach for the note helpers, the detail renderers
#      still do, and the helpers themselves are intact.
#
# Isolation: a throwaway git repo as project root under a temp dir, with its own
# XDG_CONFIG_HOME (own DB) and XDG_CACHE_HOME, and `roots` pinned to that temp
# dir so reconcile never walks the developer's real ~/Projects. No real
# DB/ledger/cache is touched. The Python CLI runs from the worktree source with
# <worktree>/bin prepended to PATH, so the event bridge execs the candidate
# endless-go rather than the global install.
#
# Exit 0 on all-passed, 1 on any failure, 2 on a setup problem.

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
setup_error() { printf '%sSETUP ERROR:%s %s\n' "${RED}${BOLD}" "${RESET}" "$1" >&2; exit 2; }

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

# ─── fixture ─────────────────────────────────────────────────────────────────

WT="$(git rev-parse --show-toplevel 2>/dev/null)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

TMP=""; REPO=""; DBDIR=""; PID=""
cleanup() { [[ -n "${TMP}" ]] && rm -rf "${TMP}"; }
trap cleanup EXIT

# E: the worktree's Python CLI against the isolated DB, from the repo dir.
E() { ( cd "$REPO" && uv run --project "$WT" endless "$@" ); }
Q() { E sql "$1" --tsv 2>/dev/null; }
W() { E sql "$1" --write >/dev/null 2>&1; }

# Fixed ids so every assertion can name them literally.
CLOSED=8001     # terminal; gains BOTH relations below
OPEN=8002       # open; must never carry a note either way
NEWER=8003      # the replacement — `NEWER replaces CLOSED`
KEEPER=8004     # `CLOSED duplicates KEEPER`
CHILD=8005      # terminal child of OPEN, for `task show --children`

# 50 columns. At 80 the bare table gives the Title 55, so this renders whole;
# the annotated cell would have left 34 and cut it. The title is the evidence.
LONG_TITLE="Consolidate the Go binaries into one endless"
RETIRED=7001    # superseded decision
GOVERNS=7002    # its successor

setup_fixture() {
    TMP="$(mktemp -d)" || return 1
    REPO="$TMP/repo"
    DBDIR="$TMP/xdg/endless"
    export XDG_CONFIG_HOME="$TMP/xdg"
    export XDG_CACHE_HOME="$TMP/cache"
    export PATH="$WT/bin:$PATH"
    mkdir -p "$REPO" "$DBDIR" || return 1

    git -C "$REPO" init -q || return 1
    git -C "$REPO" config user.email verify@test
    git -C "$REPO" config user.name verify
    git -C "$REPO" commit -q --allow-empty -m "initial commit" || return 1

    printf '{"roots": ["%s"]}\n' "$TMP" > "$DBDIR/config.json"

    E project register "$REPO" --name probe --label Probe --desc d \
        --lang Go --status active >/dev/null 2>&1
    PID="$(Q "SELECT id FROM projects WHERE name='probe'")"
    [[ -n "$PID" ]] || return 1

    # Every row exists BEFORE the first render. The relations are the only thing
    # added between the two renders, which is what makes the diff attributable.
    W "INSERT INTO tasks (id, project_id, title, status, phase) VALUES
         ($CLOSED, $PID, '$LONG_TITLE', 'obsolete', 'now'),
         ($OPEN,   $PID, 'Move the task display reads over to Go', 'ready', 'now'),
         ($NEWER,  $PID, 'The replacement', 'underway', 'now'),
         ($KEEPER, $PID, 'The keeper',      'underway', 'now')"
    W "INSERT INTO tasks (id, project_id, parent_id, title, status, phase) VALUES
         ($CHILD, $PID, $OPEN, 'A superseded child task', 'obsolete', 'now')"
    [[ "$(Q "SELECT count(*) FROM tasks WHERE id BETWEEN 8001 AND 8005")" == "5" ]] || return 1

    W "INSERT INTO decisions (id, project_id, title, description, status, created_at, updated_at) VALUES
         ($RETIRED, $PID, 'How endless binaries are built and named', '', 'superseded', datetime('now'), datetime('now')),
         ($GOVERNS, $PID, 'The rule that took over from it', '', 'accepted', datetime('now'), datetime('now'))"
    [[ "$(Q "SELECT count(*) FROM decisions")" == "2" ]] || return 1
    [[ "$(Q "SELECT count(*) FROM task_deps")" == "0" ]] || return 1
}

link_relations() {
    # Active-voice storage, as each command writes it: `NEWER replaces CLOSED`
    # notes the TARGET; `CLOSED duplicates KEEPER` notes the SOURCE.
    W "INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) VALUES
         ('task', $NEWER,  'task', $CLOSED, 'replaces'),
         ('task', $CLOSED, 'task', $KEEPER, 'duplicates'),
         ('task', $NEWER,  'task', $CHILD,  'replaces')"
    W "INSERT INTO decision_relations (source_decision_id, target_kind, target_id, relation_type)
         VALUES ($GOVERNS, 'decision', $RETIRED, 'supersedes')"
    [[ "$(Q "SELECT count(*) FROM task_deps")" == "3" ]] || return 1
    [[ "$(Q "SELECT count(*) FROM decision_relations")" == "1" ]] || return 1
}

# func_src FILE FUNC — the source text of one top-level function, via ast, so a
# shape assertion is scoped to the function it names instead of grepping a
# 6000-line module and matching a neighbour.
func_src() {
    python3 - "$1" "$2" <<'PY'
import ast, sys
tree = ast.parse(open(sys.argv[1]).read())
for node in ast.walk(tree):
    if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)) and node.name == sys.argv[2]:
        print(ast.get_source_segment(open(sys.argv[1]).read(), node))
        break
else:
    sys.exit(1)
PY
}

# ─── 1. unit gate (fail-fast) ───────────────────────────────────────────────

section "1. Unit gate (fail-fast)"

if uv run --project "$WT" pytest \
        "$WT/tests/test_status_column_width.py" \
        "$WT/tests/test_replaced_by_inline.py" \
        "$WT/tests/test_duplicates_inline.py" \
        "$WT/tests/test_decision_cmd.py" \
        -q >/tmp/e2064-pytest.log 2>&1; then
    report_pass "pytest: column width, replaced_by, duplicates, decisions"
else
    report_fail "pytest suites" "pass" "failed — see /tmp/e2064-pytest.log"
    printf '\n%sFAIL-FAST: unit gate red; later assertions suppressed.%s\n' "${RED}" "${RESET}"
    exit 1
fi

# session status is the surface this task deliberately does NOT touch, so its
# suites are part of the gate: a green run here is the claim that the Go side
# still renders the note exactly as E-1956/E-1185 left it.
if ( cd "$WT" && go test -count=1 ./internal/sessionstatuscmd/ ) \
        >/tmp/e2064-go.log 2>&1; then
    report_pass "go test ./internal/sessionstatuscmd (session status untouched)"
else
    report_fail "go test ./internal/sessionstatuscmd" "pass" \
        "failed — see /tmp/e2064-go.log"
    printf '\n%sFAIL-FAST: unit gate red; later assertions suppressed.%s\n' "${RED}" "${RESET}"
    exit 1
fi

setup_fixture || setup_error "fixture setup failed"

# ─── 2. the table pays nothing ──────────────────────────────────────────────

section "2. The relation costs the task table nothing"

before_list="$(E task list --all 2>&1)"
before_children="$(E task show "E-$OPEN" --children 2>&1)"
link_relations || setup_error "could not write the relations"
after_list="$(E task list --all 2>&1)"

# The decisive assertion. Same rows in both renders; the only difference is the
# relation. Byte-identity is therefore "the relation cost zero columns", which
# is a stronger claim than "no note appears" and is the actual defect.
assert_eq "adding the relation leaves 'task list' byte-identical" \
    "$before_list" "$after_list"
assert_not_contains "...and the note is not in it" "replaced by" "$after_list"
assert_not_contains "...nor the duplicates note" "(duplicates" "$after_list"

# The two consequences, stated directly rather than inferred from the diff.
rule_w="$(printf '%s\n' "$after_list" | python3 -c '
import sys
lines = sys.stdin.read().splitlines()
i = next(n for n, ln in enumerate(lines) if ln.startswith("ID  "))
# Columns are "  "-separated in the header and in the rule beneath it, so the
# third run of box-drawing is the Status column at its rendered width. len() on
# str counts characters, which is the unit the column is measured in.
print(len(lines[i + 1].split("  ")[2]))')"
assert_eq "the Status rule is as wide as the widest bare status" "8" "$rule_w"
assert_contains "the long title renders whole, not truncated" \
    "$LONG_TITLE" "$after_list"
assert_not_contains "...so no row shows the ellipsis it used to" "…" "$after_list"

# ─── 3. every command that renders that table ───────────────────────────────

section "3. Every renderer of that table, not just the reported one"

after_children="$(E task show "E-$OPEN" --children 2>&1)"
assert_eq "'task show --children' is byte-identical too" \
    "$before_children" "$after_children"
assert_contains "...and the terminal child is still listed" \
    "E-$CHILD" "$after_children"
assert_not_contains "...bare" "replaced by" "$after_children"

for cmd in "task list --all" "task recent" "task search endless"; do
    out="$(E $cmd 2>&1)"
    assert_not_contains "'$cmd' renders the bare status" "replaced by" "$out"
done
assert_not_contains "'task next' renders the bare status" \
    "replaced by" "$(E task next 2>&1)"
assert_not_contains "'task active' renders the bare status" \
    "replaced by" "$(E task active 2>&1)"

# ─── 4. the detail views keep it ────────────────────────────────────────────

section "4. task show keeps the note (this is not an undo of E-1956)"

show_human="$(E task show "E-$CLOSED" 2>&1)"
status_line="$(printf '%s\n' "$show_human" | grep '^Status:')"
assert_contains "'task show' names the replacement on the Status line" \
    "(replaced by E-$NEWER)" "$status_line"
assert_contains "...and the duplicate, composed on the same line" \
    "(duplicates E-$KEEPER)" "$status_line"
assert_not_contains "the keeper's own Status line stays bare" "duplicates" \
    "$(E task show "E-$KEEPER" 2>&1 | grep '^Status:')"

show_llm="$(E task show "E-$CLOSED" --llm 2>&1)"
assert_contains "'task show --llm' carries replaced_by=" \
    "replaced_by=E-$NEWER" "$show_llm"
assert_contains "...and duplicates=" "duplicates=E-$KEEPER" "$show_llm"

list_llm="$(E task list --all --llm 2>&1)"
assert_contains "'task list --llm' still carries it (no shared column to pay)" \
    "replaced_by=E-$NEWER" "$list_llm"

json_rb="$(E task show "E-$CLOSED" --json 2>&1 | python3 -c \
    'import json,sys; print(",".join(json.load(sys.stdin)["replaced_by"]))')"
assert_eq "'task show --json' carries the relation as data" "E-$NEWER" "$json_rb"

# Ungated on a NON-terminal status: the fix is a display rule, so the data path
# must still be indifferent to status.
W "UPDATE tasks SET status='unverified' WHERE id=$CLOSED"
json_open="$(E task list --all --json 2>&1 | python3 -c \
    "import json,sys; rows={r['id']:r for r in json.load(sys.stdin)}; print(','.join(rows['E-$CLOSED']['replaced_by']))")"
assert_eq "--json emits the relation on an OPEN status too" "E-$NEWER" "$json_open"
W "UPDATE tasks SET status='obsolete' WHERE id=$CLOSED"

# ─── 5. the decision table, same defect ─────────────────────────────────────

section "5. decision list pays nothing; decision show keeps it"

# The relation was written above; re-render against a table that never saw it by
# unlinking, rendering, and relinking — same row set, relation the only variable.
W "DELETE FROM decision_relations WHERE source_decision_id=$GOVERNS"
before_dec="$(E decision list 2>&1)"
W "INSERT INTO decision_relations (source_decision_id, target_kind, target_id, relation_type)
     VALUES ($GOVERNS, 'decision', $RETIRED, 'supersedes')"
after_dec="$(E decision list 2>&1)"

assert_eq "adding the relation leaves 'decision list' byte-identical" \
    "$before_dec" "$after_dec"
assert_not_contains "...and the '(by ED-N)' note is not in it" \
    "(by ED-" "$after_dec"
assert_contains "the retired decision is still listed, bare" \
    "ED-$RETIRED" "$after_dec"

assert_contains "'decision list --llm' still names the successor" \
    "superseded (by ED-$GOVERNS)" "$(E decision list --llm 2>&1)"
assert_contains "'decision show' still names the successor" \
    "ED-$GOVERNS" "$(E decision show "ED-$RETIRED" 2>&1)"
dec_json="$(E decision list --json 2>&1 | python3 -c \
    "import json,sys; rows={r['id']:r for r in json.load(sys.stdin)}; print(','.join(rows['ED-$RETIRED']['superseded_by']))")"
assert_eq "'decision list --json' carries superseded_by" "ED-$GOVERNS" "$dec_json"

# ─── 6. session status is untouched ─────────────────────────────────────────

section "6. session status was left alone, on purpose"

SS="$WT/internal/sessionstatuscmd/session_status.go"
[[ -f "$SS" ]] || setup_error "missing $SS"

# It has no Status column to widen: the status is the leading icon, and the note
# is appended PAST the title and charged against that row's own budget. There is
# no shared width for it to inflate, so the defect does not exist there.
ss_render="$(awk '/^func statusNotes\(/,/^}/' "$SS")"
assert_contains "statusNotes still composes both notes in the Go renderer" \
    "replacedByNote(r) + duplicatesNote(r)" "$ss_render"
assert_contains "the note is still charged to the row's OWN title budget" \
    "avail := titleBudget - runewidth.StringWidth(note)" "$(cat "$SS")"
assert_not_contains "session status renders no Status column to widen" \
    "'Status'" "$(cat "$SS")"

# ─── 7. source shape, so the column cannot re-widen ─────────────────────────

section "7. Source shape — the table renderers no longer reach for the notes"

TASK_CMD="$WT/src/endless/task_cmd.py"
DEC_CMD="$WT/src/endless/decision_cmd.py"
for f in "$TASK_CMD" "$DEC_CMD"; do
    [[ -f "$f" ]] || setup_error "missing $f"
done

flat="$(func_src "$TASK_CMD" _render_flat_table)" \
    || setup_error "_render_flat_table not found in task_cmd.py"
assert_not_contains "_render_flat_table does not call status_notes" \
    "status_notes" "$flat"
assert_not_contains "...nor replaced_by_map" "replaced_by_map" "$flat"
assert_not_contains "...nor duplicates_map" "duplicates_map" "$flat"
assert_contains "...and sizes Status from the bare value" \
    'len(r["status"])' "$flat"

detail="$(func_src "$TASK_CMD" _render_detail_human)" \
    || setup_error "_render_detail_human not found in task_cmd.py"
assert_contains "the human detail renderer still composes both notes" \
    "status_notes(" "$detail"

listd="$(func_src "$DEC_CMD" list_decisions)" \
    || setup_error "list_decisions not found in decision_cmd.py"
assert_contains "list_decisions sizes Status from the bare value" \
    'len(r["status"])' "$listd"
assert_contains "...and looks the relation up only for --json/--llm" \
    "if (as_json or llm)" "$listd"

# The helpers themselves must survive — the detail views and the machine modes
# still call them, so deleting them would be the other way to break this.
assert_contains "replaced_by_note is intact" "def replaced_by_note" "$(cat "$TASK_CMD")"
assert_contains "duplicates_note is intact" "def duplicates_note" "$(cat "$TASK_CMD")"
assert_contains "status_notes is intact" "def status_notes" "$(cat "$TASK_CMD")"
assert_contains "superseded_by_note is intact" "def superseded_by_note" "$(cat "$DEC_CMD")"

summary
