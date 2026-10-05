#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2225 and records what was true when E-2225
# landed. Edit it only if you ARE E-2225. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2225: ownership wording follows ED-1605 — the owner is the session that
# claimed the task, E-2188's display-time "owner" is the steward, and the
# handoff's landing line is the user's, from the owner's side.
#
# What is verified here:
#   A. Fail-fast: this task's own Go tests, the renamed contract tests by name.
#   B. `session-status --json`, through a binary built from this tree against a
#      throwaway database, carries `stewarded_elsewhere` and not
#      `owned_elsewhere`.
#   C. Rendered handoffs carry the new landing rule and not the old lines.
#   D. The old E-2188 names and the old landing line are gone from the source.
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

PKGS=(./internal/monitor ./internal/sessionstatuscmd ./internal/templatecmd)
if out=$(go test "${PKGS[@]}" 2>&1); then
    report_pass "go test: monitor, sessionstatuscmd, templatecmd"
else
    report_fail "go test: monitor, sessionstatuscmd, templatecmd" "exit 0" "$(printf '%s' "${out}" | tail -30)"
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
contract ./internal/sessionstatuscmd TestRenderJSON_CarriesFocus
contract ./internal/sessionstatuscmd TestApplyHiddenMode_FocusAndStewardship
contract ./internal/templatecmd TestRender_Handoff_WorktreeRemovalIsCategorical

# ---------------------------------------------------------------------------
section "B. session-status --json names the steward"
# ---------------------------------------------------------------------------

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go || setup_error "cannot build cmd/endless-go from this tree"
PROBE="${TMP}/db"
mkdir -p "${PROBE}"
DB="${PROBE}/endless.db"
GO=("${BIN}" --db-dir "${PROBE}")

"${GO[@]}" session-status --task 1 >/dev/null 2>&1 || true
[[ -f "${DB}" ]] || setup_error "the binary did not create ${DB}"

# Live sessions 1 (A) and 2 (B) claimed E-100 and E-101. A filed E-150 and B
# updated it, so A is E-150's steward and B's view omits it.
sqlite3 "${DB}" <<'SQL' || setup_error "cannot seed the probe database"
INSERT INTO projects (id, name, path) VALUES (1, 'e-2225-probe', '/tmp/e-2225-probe');
INSERT INTO tasks (id, project_id, title, status, phase) VALUES
  (100,1,'A claimed','underway','now'), (101,1,'B claimed','underway','now'),
  (150,1,'A filed B updated','ready','now');
INSERT INTO sessions (id, project_id, state, task_id, focus_task_id) VALUES
  (1,1,'working',100,100), (2,1,'working',101,101);
INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at) VALUES
  (1,100,1,'t','t'), (2,101,1,'t','t'),
  (1,150,2,'t','t'), (2,150,3,'t','t');
SQL

# json_row <json> <id> <field> — one field of one row, "absent" when missing.
json_row() {
    printf '%s' "$1" | python3 -c '
import json, sys
rows = [r for r in json.load(sys.stdin)["rows"] if r["id"] == int(sys.argv[1])]
print(rows[0].get(sys.argv[2], "absent") if rows else "no row")' "$2" "$3"
}

JB="$("${GO[@]}" session-status --task 101 --session 2 --json 2>"${TMP}/stderr.txt")"
JA="$("${GO[@]}" session-status --task 100 --session 1 --json 2>>"${TMP}/stderr.txt")"
assert_eq "the updater's --json flags the row stewarded_elsewhere" "True" "$(json_row "${JB}" 150 stewarded_elsewhere)"
assert_eq "and carries no owned_elsewhere field" "absent" "$(json_row "${JB}" 150 owned_elsewhere)"
assert_eq "the steward's own --json does not flag it" "False" "$(json_row "${JA}" 150 stewarded_elsewhere)"
assert_not_contains "no row anywhere carries owned_elsewhere" "owned_elsewhere" "${JA}${JB}"

# ---------------------------------------------------------------------------
section "C. Rendered handoffs carry the landing rule"
# ---------------------------------------------------------------------------
# The rendering assertions live in TestRender_Handoff_WorktreeRemovalIsCategorical
# (section A), which renders every handoff type and the claim wrapper. Here the
# templates themselves are checked, so a wrapper that bypasses the partial is
# caught too.

TPL=internal/templatecmd/templates/handoff
assert_eq "no handoff template still says the spawning session owns landing" \
    "" "$(grep -rl 'owns landing' "${TPL}" || true)"
assert_eq "no handoff template still says to land without asking" \
    "" "$(grep -rl 'worktree land` without asking' "${TPL}" || true)"
assert_contains "the shared partial carries the landing rule" \
    "landed from the owner's side" "$(cat "${TPL}/_mechanics.tmpl")"
assert_contains "and tells the session not to land itself" \
    "do not run \`endless worktree land\` yourself" "$(cat "${TPL}/_mechanics.tmpl")"

# ---------------------------------------------------------------------------
section "D. The old names are gone"
# ---------------------------------------------------------------------------

assert_eq "no source still names OwnedElsewhere / owned_elsewhere / the ownership annotator" "" \
    "$(grep -rlE 'OwnedElsewhere|owned_elsewhere|AnnotateSessionStatusOwnership|liveOwnership|taskOwnership' internal src docs/guide || true)"
assert_eq "session_ownership.go is renamed to session_stewardship.go" "absent" \
    "$([[ -e internal/monitor/session_ownership.go ]] && echo present || echo absent)"
assert_contains "the sessions guide names the steward" "### Focus, and a task's steward" "$(cat docs/guide/sessions.md)"
assert_eq "and no longer calls one session's view a board in that title" "" \
    "$(grep -n 'more than one board' docs/guide/sessions.md || true)"

summary
