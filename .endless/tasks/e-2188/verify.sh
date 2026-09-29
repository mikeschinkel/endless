#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2188 and records what was true when E-2188
# landed. Edit it only if you ARE E-2188. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2188: show each session's focused task, and which session owns a task that
# sits on more than one board.
#
# What is verified here:
#   A. Fail-fast: this task's own Go tests, and each contract test by name so
#      deleting one cannot turn the section green by absence. Focus following
#      writes but not reads is proven here, through real events.
#   B. The schema: a database the binary builds carries sessions.focus_task_id.
#   C. Boards, end to end, through a binary built from this tree against a
#      throwaway database holding four live sessions with overlapping
#      session_tasks rows: the owner ladder, the duplicate mark on both boards,
#      focus overriding a hide and an owner, an ended session's task coming
#      back, the claimer never counted, and the frame rows never hidden.
#   D. Colour: focus is an inverse id, a duplicate a bright-white-on-black id,
#      and the claimed task's focus is colour only.
#   E. The legend: normal, compact exactly when only compact fits, normal
#      (wrapping) when neither fits — in both `session status` and `session
#      monitor`.
#   F. `session monitor` draws the frame `session status` draws.
#   G. The monitor's pane is sized by display rows, not lines.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required to seed the probe database"
command -v python3 >/dev/null 2>&1 || setup_error "python3 is required to measure legend widths"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

PKGS=(./internal/events ./internal/monitor ./internal/sessionstatuscmd
      ./internal/liveview ./internal/schema/... ./internal/sessiontaskrelation)
if out=$(go test "${PKGS[@]}" 2>&1); then
    report_pass "go test: events, monitor, sessionstatuscmd, liveview, schema, sessiontaskrelation"
else
    report_fail "go test: events, monitor, sessionstatuscmd, liveview, schema, sessiontaskrelation" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
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
contract ./internal/events TestSessionFocus_FollowsWritesNotReads
contract ./internal/events TestSessionFocus_UnknownTaskIsNotAnError
contract ./internal/events TestRelation_MovesFocus
contract ./internal/schema TestMigrate_MatchesSchemaSQL
for t in TestOwnership_WorkedCase TestOwnership_UpdateNeverTakesOwnership \
         TestOwnership_SoleUpdaterOwns TestOwnership_Ambiguous \
         TestOwnership_EndedSessionReleases TestOwnership_ClaimerIsNotADuplicate \
         TestOwnership_FrameRowsNeverHidden; do
    contract ./internal/monitor "${t}"
done
for t in TestColumnFourMark TestColumnFourMarkWidths TestIDField \
         TestApplyHiddenMode_FocusAndOwnership TestBuildLegend_Fit \
         TestRender_ClaimedTaskFocusIsColourOnly TestRenderJSON_CarriesFocus; do
    contract ./internal/sessionstatuscmd "${t}"
done
contract ./internal/liveview TestFrameDisplayRows

# ---------------------------------------------------------------------------
section "B. The schema"
# ---------------------------------------------------------------------------
# Built from this tree, not taken from bin/, so the answers come from what is
# committed here. --db-dir names a throwaway database, which keeps the probe out
# of the real record and out of this worktree's sandbox; --task/--session are
# the headless seam that reads the resolved context instead of pinning main.

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go || setup_error "cannot build cmd/endless-go from this tree"
PROBE="${TMP}/db"
mkdir -p "${PROBE}"
DB="${PROBE}/endless.db"
GO=("${BIN}" --db-dir "${PROBE}")

"${GO[@]}" session-status --task 1 >/dev/null 2>&1 || true
[[ -f "${DB}" ]] || setup_error "the binary did not create ${DB}"

assert_eq "a database the binary builds has sessions.focus_task_id" \
    "focus_task_id" "$(sqlite3 "${DB}" "SELECT name FROM pragma_table_info('sessions') WHERE name='focus_task_id'")"
assert_contains "and it is a foreign key to tasks, nulled on delete" \
    "tasks|id|SET NULL" \
    "$(sqlite3 "${DB}" "SELECT \"table\"||'|'||\"to\"||'|'||on_delete FROM pragma_foreign_key_list('sessions') WHERE \"from\"='focus_task_id'")"

# ---------------------------------------------------------------------------
section "C. Boards"
# ---------------------------------------------------------------------------
# Live sessions 1 (A), 2 (B), 3 (C), 4 (D) claimed E-100..E-103, and each is
# focused on its own claimed task. E-90 is E-100's parent. Relations: 2
# surfaced, 3 revisited.
#   E-150  A filed it, B updated it.        → A owns
#   E-151  B and C updated it, no filer.    → ambiguous
#   E-152  D filed it, A updated it.        → D owns while D is live
#   E-153  A updated it, then hid it.       → A owns; hidden on A's board
#   E-154  A filed it; session 5 claimed it (the spawn flow).
#   E-90   B filed E-100's parent.          → frame row on A's board
#   E-102  C's claim, which B updated: the ↩ from row when A was spawned by C.
sqlite3 "${DB}" <<'SQL' || setup_error "cannot seed the probe database"
INSERT INTO projects (id, name, path) VALUES (1, 'e-2188-probe', '/tmp/e-2188-probe');
INSERT INTO tasks (id, project_id, title, status, phase) VALUES
  (90,1,'the parent','ready','now'),
  (100,1,'A claimed','underway','now'), (101,1,'B claimed','underway','now'),
  (102,1,'C claimed','underway','now'), (103,1,'D claimed','underway','now'),
  (150,1,'A filed B updated','ready','now'), (151,1,'B and C updated','ready','now'),
  (152,1,'D filed A updated','ready','now'), (153,1,'A hid it','ready','now'),
  (154,1,'A filed it and spawned','underway','now');
UPDATE tasks SET parent_id = 90 WHERE id = 100;
INSERT INTO sessions (id, project_id, state, task_id, focus_task_id) VALUES
  (1,1,'working',100,100), (2,1,'working',101,101), (3,1,'working',102,102),
  (4,1,'working',103,103), (5,1,'working',154,154);
INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at) VALUES
  (1,100,1,'t','t'), (2,101,1,'t','t'), (3,102,1,'t','t'), (4,103,1,'t','t'), (5,154,1,'t','t'),
  (1,150,2,'t','t'), (2,150,3,'t','t'),
  (2,151,3,'t','t'), (3,151,3,'t','t'),
  (4,152,2,'t','t'), (1,152,3,'t','t'),
  (1,153,3,'t','t'),
  (1,154,2,'t','t'),
  (2,90,2,'t','t'),
  (2,102,3,'t','t');
INSERT INTO session_hidden_tasks (session_id, task_id, hidden_at) VALUES (1,153,'t');
SQL

sql() { sqlite3 "${DB}" "$1" || setup_error "sqlite3 failed: $1"; }

# board <focal> <viewer> [extra args] — one plain-text frame.
board() {
    local focal="$1" viewer="$2"
    shift 2
    "${GO[@]}" session-status --task "${focal}" --session "${viewer}" --cols 200 "$@" 2>"${TMP}/stderr.txt"
}

# row <frame> <id> — the frame's line for E-<id>, or "" when it is not drawn.
row() { printf '%s\n' "$1" | grep -E "E-$2( |$)" || true; }

A="$(board 100 1 --from-session 3)"
B="$(board 101 2)"
C="$(board 102 3)"

assert_contains "the filer's board shows the task it filed" "E-150" "$(row "${A}" 150)"
assert_eq "an updater's board omits a task another live session filed" "" "$(row "${B}" 150)"
assert_contains "an ambiguous task stays on one updater's board, marked ◫" "◫E-151" "$(row "${B}" 151)"
assert_contains "and on the other's, marked ◫" "◫E-151" "$(row "${C}" 151)"
assert_eq "a task a live session filed is omitted from its updater's board" "" "$(row "${A}" 152)"
assert_eq "a hidden task is omitted while unfocused" "" "$(row "${A}" 153)"
assert_contains "and counted in the hidden footer" "1 hidden" "${A}"
assert_contains "the spawn flow: the filer's ⟳ row for a claimed task is shown" "⟳" "$(row "${A}" 154)"
assert_not_contains "and the claimer is not counted as a duplicate" "◫" "$(row "${A}" 154)"
assert_not_contains "on the claimer's own board either" "◫" "$(row "$(board 154 5)" 154)"
assert_contains "the parent row is never hidden, whoever filed it" "↑" "$(row "${A}" 90)"
assert_contains "the spawner row is never hidden, whoever updated it" "↩" "$(row "${A}" 102)"
assert_not_contains "the claimed task's own row never wears ◼︎ or ◫" "◫" "$(row "${A}" 100)"

# The worked case: B focuses E-150, which A owns.
sql "UPDATE sessions SET focus_task_id = 150 WHERE id = 2"
A="$(board 100 1 --from-session 3)"
B="$(board 101 2)"
assert_contains "a task focused elsewhere is marked ◫ on its owner's board" "◫E-150" "$(row "${A}" 150)"
assert_contains "and shown, marked ◫, on the focusing board" "◫E-150" "$(row "${B}" 150)"
assert_contains "the legend documents the mark" "◫ duplicate" "$(printf '%s\n' "${B}" | head -1)"

sql "UPDATE sessions SET focus_task_id = 101 WHERE id = 2"
A="$(board 100 1 --from-session 3)"
B="$(board 101 2)"
assert_eq "once the focus moves on, the task drops off that board" "" "$(row "${B}" 150)"
assert_not_contains "and the owner's mark clears" "◫" "$(row "${A}" 150)"

# Focus overrides a manual hide.
sql "UPDATE sessions SET focus_task_id = 153 WHERE id = 1"
A="$(board 100 1 --from-session 3)"
assert_contains "a focused task is shown although hidden, marked ◼︎" "◼︎E-153" "$(row "${A}" 153)"
assert_contains "and still carries its ⊘" "⊘" "$(row "${A}" 153)"
assert_not_contains "and is not counted as hidden" "hidden (--show-hidden)" "${A}"
assert_contains "the legend documents ◼︎" "◼︎ focus" "$(printf '%s\n' "${A}" | head -1)"

# Focus overrides an owner too: it is shown, and it is a duplicate.
sql "UPDATE sessions SET focus_task_id = 152 WHERE id = 1"
A="$(board 100 1 --from-session 3)"
assert_contains "a focused task another session owns is shown, marked ◫" "◫E-152" "$(row "${A}" 152)"

# A dead session owns nothing.
sql "UPDATE sessions SET focus_task_id = 100 WHERE id = 1; UPDATE sessions SET state = 'ended' WHERE id = 4"
A="$(board 100 1 --from-session 3)"
assert_contains "an ended session's task comes back on a live board" "E-152" "$(row "${A}" 152)"
assert_not_contains "unmarked" "◫" "$(row "${A}" 152)"
assert_not_contains "and with focus on the claimed task, no row wears ◼︎" "◼︎" "${A}"

JSON="$(board 100 1 --json)"
assert_contains "--json states focus on the frame" '"focus": "E-100"' "${JSON}"
# json_row <json> <id> <field> — one field of one row, as Python prints it.
json_row() {
    printf '%s' "$1" | python3 -c '
import json, sys
rows = [r for r in json.load(sys.stdin)["rows"] if r["id"] == int(sys.argv[1])]
print(rows[0][sys.argv[2]] if rows else "absent")' "$2" "$3"
}
assert_eq "--json states the claimed task's focus on its row" "True" "$(json_row "${JSON}" 100 focused)"
assert_eq "--json keeps a row the table omits, flagged owned_elsewhere" \
    "True" "$(json_row "$(board 101 2 --json)" 150 owned_elsewhere)"
assert_eq "--json flags an ambiguous row duplicate_work" \
    "True" "$(json_row "$(board 101 2 --json)" 151 duplicate_work)"

# ---------------------------------------------------------------------------
section "D. Colour"
# ---------------------------------------------------------------------------
# Colour needs a terminal. script(1) gives the binary one, so what is asserted
# is exactly what a person sees.
colour() {
    local focal="$1" viewer="$2"
    if [[ "$(uname)" == Darwin ]]; then
        script -q /dev/null "${GO[@]}" session-status --task "${focal}" --session "${viewer}" --cols 200 </dev/null
    else
        script -qc "$(printf '%q ' "${GO[@]}" session-status --task "${focal}" --session "${viewer}" --cols 200)" /dev/null </dev/null
    fi
}
ESC=$'\x1b'
sql "UPDATE sessions SET focus_task_id = 150 WHERE id = 2"
BC="$(colour 101 2)"
assert_contains "a duplicate's id is bright white on black" "${ESC}[97;40mE-150${ESC}[39;49m" "${BC}"
sql "UPDATE sessions SET focus_task_id = 101 WHERE id = 2"
BC="$(colour 101 2)"
assert_contains "a focused claimed task's id is inverse" "${ESC}[7mE-101${ESC}[27m" "${BC}"
assert_not_contains "with no ◼︎ glyph" "◼︎" "${BC}"

# ---------------------------------------------------------------------------
section "E. The legend fits"
# ---------------------------------------------------------------------------
sql "UPDATE sessions SET focus_task_id = 153 WHERE id = 1"
# width <text> — display width; every legend glyph here is one column, and the
# variation selector after ◼ is zero.
width() { python3 -c 'import sys; print(len(sys.stdin.read().rstrip("\n").replace("︎","")))'; }
legend() { "${GO[@]}" session-status --task 100 --session 1 --from-session 3 --cols "$1" "${@:2}" | head -1; }

for mode in status monitor; do
    extra=()
    [[ "${mode}" == monitor ]] && extra=(--monitor)
    NORMAL="$(legend 300 "${extra[@]}")"
    NW="$(printf '%s' "${NORMAL}" | width)"
    COMPACT="$(legend $((NW - 1)) "${extra[@]}")"
    CW="$(printf '%s' "${COMPACT}" | width)"
    TIGHT="$(legend $((CW - 1)) "${extra[@]}")"

    assert_contains "${mode}: the normal form is <icon> <label>, two spaces apart" "● this  " "${NORMAL}"
    assert_contains "${mode}: one column short of normal, the compact form" "●this " "${COMPACT}"
    assert_not_contains "${mode}: compact has no double spaces" "  " "${COMPACT}"
    assert_eq "${mode}: compact holds every entry normal does" \
        "$(printf '%s' "${NORMAL}" | tr -d ' ')" "$(printf '%s' "${COMPACT}" | tr -d ' ')"
    assert_eq "${mode}: when even compact overflows, the normal form, untruncated" "${NORMAL}" "${TIGHT}"
done

# ---------------------------------------------------------------------------
section "F. session monitor draws session status's frame"
# ---------------------------------------------------------------------------
# Piped, --monitor degrades to one frame through the same resolve→render path
# the live loop repaints; a later split between the two would show here.
for cols in 200 60; do
    assert_eq "monitor frame == status frame at ${cols} columns" \
        "$("${GO[@]}" session-status --task 100 --session 1 --from-session 3 --cols "${cols}")" \
        "$("${GO[@]}" session-status --task 100 --session 1 --from-session 3 --cols "${cols}" --monitor)"
done

# ---------------------------------------------------------------------------
section "G. The monitor's pane is sized by display rows"
# ---------------------------------------------------------------------------
assert_contains "FitPaneToFrame measures FrameDisplayRows, not FrameLines" \
    "PaneHeightForFrame(FrameDisplayRows(frame, cols)" "$(cat internal/liveview/liveview.go)"
assert_contains "and the loop hands it the width it rendered at" \
    "FitPaneToFrame(cfg.Pane, frame, cols, rows, fitted)" "$(cat internal/liveview/liveview.go)"

summary
