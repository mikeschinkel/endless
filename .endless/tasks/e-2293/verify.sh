#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2293 and records what was true when E-2293
# landed. Edit it only if you ARE E-2293. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2293: session status suppresses a row only for a session PROVEN PRESENT
# (pane seen), a single present claimer clears ◫, a hide outranks the focus
# pointer, suppression is counted and revealable, and `session unhide --task`
# pins a row against stewardship.
#
# What is verified here:
#   A. Fail-fast: this task's Go and Python tests.
#   B. Through a binary built from this tree, against a throwaway database:
#      a filer whose pane nobody can see (the E-1993/E-1995 case) does not
#      take the row off another session's view.
#   C. A hide on a row that is only the viewer's focus is honoured and footered.
#   D. --show-suppressed is accepted, and --json carries `pinned`.
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

PKGS=(./internal/monitor ./internal/sessionstatuscmd ./internal/events ./internal/schema)
if out=$(go test "${PKGS[@]}" 2>&1); then
    report_pass "go test: monitor, sessionstatuscmd, events, schema"
else
    report_fail "go test: monitor, sessionstatuscmd, events, schema" "exit 0" "$(printf '%s' "${out}" | tail -30)"
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
contract ./internal/monitor TestStewardship_UnreachableSessionDoesNotSteward
contract ./internal/monitor TestStewardship_DeadPaneDoesNotSteward
contract ./internal/monitor TestStewardship_IdlePresentSessionStillStewards
contract ./internal/monitor TestStewardship_OneClaimerClearsDuplicate
contract ./internal/monitor TestStewardship_UnprovenClaimerKeepsDuplicate
contract ./internal/monitor TestStewardship_TwoClaimersMarkDuplicate
contract ./internal/monitor TestStewardship_PinOutranksSteward
contract ./internal/sessionstatuscmd TestApplyHiddenMode_FocusAndStewardship
contract ./internal/sessionstatuscmd TestRender_SuppressedFooterAndGlyph
contract ./internal/events TestSessionTasksHidden_RefusesOwnTask
contract ./internal/events TestSessionTasksHidden_FocusIsHideable
contract ./internal/events TestSessionTasksUnhidden_UnhidesOrPins

if out=$(uv run pytest tests/test_session_hide_tasks.py tests/test_session_task_membership.py -q 2>&1); then
    report_pass "pytest: session hide/unhide --task and membership verbs"
else
    report_fail "pytest: session hide/unhide --task and membership verbs" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# ---------------------------------------------------------------------------
section "B. A filer nobody can see does not suppress"
# ---------------------------------------------------------------------------

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go || setup_error "cannot build cmd/endless-go from this tree"
PROBE="${TMP}/db"
mkdir -p "${PROBE}"
DB="${PROBE}/endless.db"
GO=("${BIN}" --db-dir "${PROBE}")

"${GO[@]}" session-status --task 1 >/dev/null 2>&1 || true
[[ -f "${DB}" ]] || setup_error "the binary did not create ${DB}"

# Session 1 (A) claimed E-100 and updated E-150. Session 4 filed E-150 and is
# idle, bound to pane %265 on a tmux server that does not exist — exactly
# session 1113's state when it withheld E-1993/E-1995. Under the old rule it
# was E-150's steward and A's view omitted it.
#
# Session 1 is also focused on E-160 and has hidden it; E-160 is not its task.
sqlite3 "${DB}" <<'SQL' || setup_error "cannot seed the probe database"
INSERT INTO projects (id, name, path) VALUES (1, 'e-2293-probe', '/tmp/e-2293-probe');
INSERT INTO tasks (id, project_id, title, status, phase) VALUES
  (100,1,'A claimed','underway','now'), (103,1,'ghost claimed','underway','now'),
  (150,1,'ghost filed, A updated','ready','now'),
  (160,1,'A focused and hid','ready','now');
INSERT INTO processes (id, kind_id, server_uuid, address) VALUES (1, 1, 'vanished-server', '%265');
INSERT INTO sessions (id, project_id, state, task_id, focus_task_id, process_id) VALUES
  (1,1,'working',100,160,NULL), (4,1,'idle',103,103,1);
INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at) VALUES
  (1,100,1,'t','t'), (4,103,1,'t','t'),
  (4,150,2,'t','t'), (1,150,3,'t','t'), (1,160,3,'t','t');
INSERT INTO session_hidden_tasks (session_id, task_id, hidden_at) VALUES (1,160,'t');
SQL

# json_row <json> <id> <field> — one field of one row, "absent" when missing.
json_row() {
    printf '%s' "$1" | python3 -c '
import json, sys
rows = [r for r in json.load(sys.stdin)["rows"] if r["id"] == int(sys.argv[1])]
print(rows[0].get(sys.argv[2], "absent") if rows else "no row")' "$2" "$3"
}

JA="$("${GO[@]}" session-status --task 100 --session 1 --json 2>"${TMP}/stderr.txt")" \
    || setup_error "session-status --json failed: $(cat "${TMP}/stderr.txt")"
assert_eq "E-150 is not stewarded elsewhere on A's view" "False" "$(json_row "${JA}" 150 stewarded_elsewhere)"
TA="$("${GO[@]}" session-status --task 100 --session 1 --cols 120 2>>"${TMP}/stderr.txt")"
assert_contains "and the table draws it" "E-150" "${TA}"
assert_not_contains "and nothing is reported suppressed" "suppressed" "${TA}"

# ---------------------------------------------------------------------------
section "C. A hide outranks the focus pointer"
# ---------------------------------------------------------------------------

assert_eq "E-160 is A's focus" "True" "$(json_row "${JA}" 160 focused)"
assert_not_contains "the hidden focus row is not drawn" "E-160" "${TA}"
assert_contains "and the footer counts it" "… 1 hidden (--show-hidden)" "${TA}"

# ---------------------------------------------------------------------------
section "D. The new surface"
# ---------------------------------------------------------------------------

if TS="$("${GO[@]}" session-status --task 100 --session 1 --cols 120 --show-suppressed 2>&1)"; then
    report_pass "--show-suppressed is accepted"
else
    report_fail "--show-suppressed is accepted" "exit 0" "${TS}"
fi
assert_eq "--json carries pinned" "False" "$(json_row "${JA}" 150 pinned)"

summary
