#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2204 and records what was true when E-2204
# landed. Edit it only if you ARE E-2204. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2204: hiding a task gives up its ownership, non-actionable rows dim, the
# duplicate mark is 232 on 160, and a task that has just become spawnable is
# highlighted 232 on 118 until it is claimed.
#
# What is verified here:
#   A. Fail-fast: this task's own Go tests, each contract test by name.
#   B. Boards, end to end, through a binary built from this tree against a
#      throwaway database: the E-1814 shape (two live updaters, one hid it) is
#      no longer marked; a hider focused on the task again is.
#   C. Unblocked: ▷ on a task whose only blocker resolved, not while another
#      blocker is open, gone once claimed; --json carries `spawnable`.
#   D. Colour, through a real terminal: duplicate 232/160, spawnable 232/118,
#      in-flight and parent rows dimmed, a highlight on a dimmed row lifted out
#      of the dim.
#   E. `session monitor` draws the frame `session status` draws.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required to seed the probe database"
command -v python3 >/dev/null 2>&1 || setup_error "python3 is required to read --json"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

PKGS=(./internal/monitor ./internal/sessionstatuscmd)
if out=$(go test "${PKGS[@]}" 2>&1); then
    report_pass "go test: monitor, sessionstatuscmd"
else
    report_fail "go test: monitor, sessionstatuscmd" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

contract() {
    local pkg="$1" t="$2"
    if go test "${pkg}" -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}"; then
        report_pass "contract test runs and passes: ${t}"
    else
        report_fail "contract test runs and passes: ${t}" "--- PASS: ${t}" "no PASS line"
    fi
}
for t in TestOwnership_HiddenRevisiterExcluded TestOwnership_HiddenSurfacerExcluded \
         TestOwnership_HiddenNonFocuserExcluded TestOwnership_HiddenButFocusedCounts \
         TestSpawnable_OnlyBlockerResolves TestSpawnable_ClearsOnceClaimed \
         TestSpawnable_AnotherBlockerStillOpen TestSpawnable_NeverBlocked \
         TestSpawnable_UnclaimedSessionView; do
    contract ./internal/monitor "${t}"
done
for t in TestColorize_DimsNonActionableRows TestIDField TestIDField_HighlightLiftsOutOfDim \
         TestIDField_Spawnable TestHighlightPrecedence TestRender_SpawnableLegend \
         TestColumnFourMarkWidths; do
    contract ./internal/sessionstatuscmd "${t}"
done

# ---------------------------------------------------------------------------
section "B. Hiding gives up ownership"
# ---------------------------------------------------------------------------
# Built from this tree; --db-dir names a throwaway database, and --task/--session
# are the headless seam, so nothing here reads or writes a real database.

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go || setup_error "cannot build cmd/endless-go from this tree"
PROBE="${TMP}/db"
mkdir -p "${PROBE}"
DB="${PROBE}/endless.db"
GO=("${BIN}" --db-dir "${PROBE}")

"${GO[@]}" session-status --task 1 >/dev/null 2>&1 || true
[[ -f "${DB}" ]] || setup_error "the binary did not create ${DB}"

# Live sessions 1 (A), 2 (B), 3 (C) claimed E-100..E-102, each focused on its
# own. E-90 is E-100's parent. Relations: 2 surfaced, 3 revisited.
#   E-151  B and C updated it; C hid it.   → B owns, unmarked (the E-1814 shape)
#   E-200  E-100 blocks it.                → ▷ once E-100 resolves
#   E-201  E-100 and E-102 block it.       → never ▷ while E-102 is open
#   E-300  A filed it; session 5 is working it (⟳ on A's board).
sqlite3 "${DB}" <<'SQL' || setup_error "cannot seed the probe database"
INSERT INTO projects (id, name, path) VALUES (1, 'e-2204-probe', '/tmp/e-2204-probe');
INSERT INTO tasks (id, project_id, title, status, phase) VALUES
  (90,1,'the parent','ready','now'),
  (100,1,'A claimed','underway','now'), (101,1,'B claimed','underway','now'),
  (102,1,'C claimed','underway','now'),
  (151,1,'B and C updated, C hid it','ready','now'),
  (200,1,'blocked by A only','ready','now'),
  (201,1,'blocked by A and C','ready','now'),
  (300,1,'A filed, 5 working','underway','now');
UPDATE tasks SET parent_id = 90 WHERE id = 100;
INSERT INTO sessions (id, project_id, state, task_id, focus_task_id) VALUES
  (1,1,'working',100,100), (2,1,'working',101,101), (3,1,'working',102,102),
  (5,1,'working',300,300);
INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at) VALUES
  (1,100,1,'t','t'), (2,101,1,'t','t'), (3,102,1,'t','t'), (5,300,1,'t','t'),
  (2,151,3,'t','t'), (3,151,3,'t','t'),
  (1,300,2,'t','t');
INSERT INTO session_hidden_tasks (session_id, task_id, hidden_at) VALUES (3,151,'t');
INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) VALUES
  ('task',100,'task',200,'blocks'),
  ('task',100,'task',201,'blocks'),
  ('task',102,'task',201,'blocks');
SQL

sql() { sqlite3 "${DB}" "$1" || setup_error "sqlite3 failed: $1"; }

board() {
    local focal="$1" viewer="$2"
    shift 2
    "${GO[@]}" session-status --task "${focal}" --session "${viewer}" --cols 200 "$@" 2>"${TMP}/stderr.txt"
}
row() { printf '%s\n' "$1" | grep -E "E-$2( |$)" || true; }

B="$(board 101 2)"
assert_contains "the remaining updater's board shows the task" "E-151" "$(row "${B}" 151)"
assert_not_contains "unmarked: the hider no longer makes it ambiguous" "◫" "$(row "${B}" 151)"

sql "UPDATE sessions SET focus_task_id = 151 WHERE id = 3"
B="$(board 101 2)"
assert_contains "a hider focused on the task again counts: marked ◫" "◫E-151" "$(row "${B}" 151)"
sql "UPDATE sessions SET focus_task_id = 102 WHERE id = 3"

# ---------------------------------------------------------------------------
section "C. Unblocked"
# ---------------------------------------------------------------------------

A="$(board 100 1)"
assert_not_contains "no ▷ while the blocker is open" "▷" "${A}"

sql "UPDATE tasks SET status = 'assumed' WHERE id = 100"
A="$(board 100 1)"
assert_contains "▷ once its only blocker resolved" "▷E-200" "$(row "${A}" 200)"
assert_not_contains "no ▷ while another blocker is still open" "▷" "$(row "${A}" 201)"
assert_contains "the legend documents ▷" "▷ unblocked" "$(printf '%s\n' "${A}" | head -1)"
assert_contains "still there on the next draw" "▷E-200" "$(row "$(board 100 1)" 200)"

json_row() {
    printf '%s' "$1" | python3 -c '
import json, sys
rows = [r for r in json.load(sys.stdin)["rows"] if r["id"] == int(sys.argv[1])]
print(rows[0][sys.argv[2]] if rows else "absent")' "$2" "$3"
}
assert_eq "--json carries spawnable" "True" "$(json_row "$(board 100 1 --json)" 200 spawnable)"
assert_eq "--json: not spawnable while a blocker holds it" "False" "$(json_row "$(board 100 1 --json)" 201 spawnable)"

# ---------------------------------------------------------------------------
section "D. Colour"
# ---------------------------------------------------------------------------
colour() {
    local focal="$1" viewer="$2"
    if [[ "$(uname)" == Darwin ]]; then
        script -q /dev/null "${GO[@]}" session-status --task "${focal}" --session "${viewer}" --cols 200 </dev/null
    else
        script -qc "$(printf '%q ' "${GO[@]}" session-status --task "${focal}" --session "${viewer}" --cols 200)" /dev/null </dev/null
    fi
}
ESC=$'\x1b'
AC="$(colour 100 1)"
assert_contains "a spawnable id is 232 on 118" "${ESC}[38;5;232;48;5;118mE-200${ESC}[39;49m" "${AC}"
assert_contains "the parent row is dimmed" "${ESC}[2m↑" "${AC}"
assert_contains "the in-flight row is dimmed" "${ESC}[2m⟳" "${AC}"

# A duplicate on an in-flight row: B and C both updated E-300, which session 5
# is working. On B's board it is ⟳ (dimmed) and ◫ (lifted out of the dim).
sql "INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at) VALUES (2,300,3,'t','t'), (3,300,3,'t','t'); DELETE FROM session_tasks WHERE session_id = 1 AND task_id = 300"
BC="$(colour 101 2)"
assert_contains "a duplicate id is 232 on 160, lifted out of its row's dim" \
    "${ESC}[22m${ESC}[38;5;232;48;5;160mE-300${ESC}[39;49m${ESC}[2m" "${BC}"

# ---------------------------------------------------------------------------
section "E. session monitor draws session status's frame"
# ---------------------------------------------------------------------------
for cols in 200 60; do
    assert_eq "monitor frame == status frame at ${cols} columns" \
        "$("${GO[@]}" session-status --task 100 --session 1 --cols "${cols}")" \
        "$("${GO[@]}" session-status --task 100 --session 1 --cols "${cols}" --monitor)"
done

summary
