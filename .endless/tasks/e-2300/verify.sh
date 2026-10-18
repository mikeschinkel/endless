#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2300 and records what was true when E-2300
# landed. Edit it only if you ARE E-2300. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2300: a session bound to a tmux server that no longer exists reads dead,
# judged by probing the socket its server recorded, rather than unknown.
#
# What is verified here:
#   A. Fail-fast: the touched Go packages, this task's contract tests by NAME
#      (so deleting one cannot turn the section green by absence), and the
#      Python tests for the claim refusal.
#   B. End to end through an endless-go built from this tree, against a scratch
#      database and REAL sockets on this run's private tmux directory: a socket
#      now answering as another server, a socket nothing listens on, a missing
#      socket file, a binding with no recorded socket, and a live pane — each
#      read through `session-query list-live`, the claim guard's own source.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"
command -v python3 >/dev/null 2>&1 || setup_error "python3 is required to read --json"
command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required to seed the scratch database"
command -v tmux >/dev/null 2>&1 || setup_error "tmux is required for the end-to-end layer"
[[ -n "${TMUX_TMPDIR:-}" ]] || setup_error "TMUX_TMPDIR is unset; run this through endless task verify"

# Sockets go under the run's private tmux directory: a unix socket path is
# limited to ~104 bytes, which a mktemp under $TMPDIR can exceed on macOS.
E2E="$(mktemp -d "${TMUX_TMPDIR}/e2300.XXXX")" || setup_error "cannot create a scratch directory"
SOCKS="${E2E}/es"
cleanup() {
    tmux -S "${SOCKS}/endless-tasks" kill-server 2>/dev/null
    rm -rf "${E2E}"
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

PKGS=(./internal/monitor ./internal/schema/... ./internal/sessionquerycmd ./internal/worktreecmd)
if out=$(go test "${PKGS[@]}" 2>&1); then
    report_pass "go test: monitor, schema, sessionquerycmd, worktreecmd"
else
    report_fail "go test: monitor, schema, sessionquerycmd, worktreecmd" \
        "exit 0" "$(printf '%s' "${out}" | grep -v '^20[0-9][0-9]/' | tail -40)"
    summary
fi

check_named() {
    local pkg="$1" t="$2"
    if go test "${pkg}" -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}"; then
        report_pass "contract test runs and passes: ${t}"
    else
        report_fail "contract test runs and passes: ${t}" "--- PASS: ${t}" "no PASS line"
    fi
}

for t in TestLiveness_GoneServerTruthTable TestLiveness_ObservedServerIsNeverProbed \
         TestLiveness_ProbesEachSocketOnce TestLiveness_GoneVerdictWritesNothing \
         TestProbeTmuxSocket_FileVerdicts TestEnsureTmuxPaneProcess_RecordsSocketPath \
         TestEnsureTmuxPaneProcess_SkipsRelativeSocket \
         TestWorktreeInUse_IgnoresSessionOnGoneServer \
         TestSessionStatusRows_InFlightIgnoresSessionOnGoneServer \
         TestSessionGraphData_InFlightIgnoresSessionOnGoneServer \
         TestListLiveSessions_OmitsSessionOnGoneServer \
         TestListLiveSessions_ReportsEndlessServerDown \
         TestStewardship_GoneServerSessionDoesNotSteward \
         TestStewardship_UnreachableSessionStewards \
         TestStewardship_GoneClaimerKeepsDuplicate \
         TestStewardship_UnreachableClaimerClearsDuplicate; do
    check_named ./internal/monitor "${t}"
done
check_named ./internal/schema TestMigrate_MatchesSchemaSQL

if out=$(uv run pytest tests/test_check_task_ownership.py tests/test_task_reopen.py -q 2>&1); then
    report_pass "pytest: the claim refusal for an unknown owner, and spawn under test"
else
    report_fail "pytest: the claim refusal for an unknown owner, and spawn under test" \
        "exit 0" "$(printf '%s' "${out}" | tail -40)"
fi

# ---------------------------------------------------------------------------
section "B. End to end: real sockets, a built endless-go, list-live"
# ---------------------------------------------------------------------------

BIN="${E2E}/endless-go"
go build -o "${BIN}" ./cmd/endless-go || setup_error "cannot build cmd/endless-go from this tree"

# The binary's own HOME, naming ${SOCKS} as Endless's socket directory, so its
# observation reaches only the servers this suite starts. Run from ${E2E}: inside
# a project, the CLI layer is that checkout's own config and the socket
# directory would not be ours.
mkdir -p "${E2E}/home/.config/endless" "${E2E}/db" "${E2E}/proj" "${SOCKS}"
chmod 700 "${SOCKS}"
printf '{"tmux":{"socket_dir":"%s"}}\n' "${SOCKS}" > "${E2E}/home/.config/endless/config.json"
cd "${E2E}" || setup_error "cannot cd to ${E2E}"

go_e2e() {
    env -u TMUX -u TMUX_PANE HOME="${E2E}/home" XDG_CONFIG_HOME="${E2E}/home/.config" \
        "${BIN}" --db-dir "${E2E}/db" "$@"
}

go_e2e session-query list-live --project-root "${E2E}/proj" >/dev/null 2>&1 \
    || setup_error "list-live could not open the scratch database"
PROJECT="$(sqlite3 "${E2E}/db/endless.db" "SELECT id FROM projects LIMIT 1")"
[[ -n "${PROJECT}" ]] || setup_error "no project row was registered"

# Endless's task-session server, now running as NEWER. Its console socket is
# never created.
tmux -S "${SOCKS}/endless-tasks" new-session -d -s tasks 'sleep 600' \
    || setup_error "cannot start a tmux server on ${SOCKS}/endless-tasks"
tmux -S "${SOCKS}/endless-tasks" set-option -g @server_uuid NEWER
PANE="$(tmux -S "${SOCKS}/endless-tasks" display-message -p '#{pane_id}')"

# A socket file nothing listens on: a server killed without unlinking it.
python3 -c 'import socket,sys; s=socket.socket(socket.AF_UNIX); s.bind(sys.argv[1]); s.close()' \
    "${E2E}/refused" || setup_error "cannot create a stale socket"

sqlite3 "${E2E}/db/endless.db" <<SQL || setup_error "cannot seed the scratch database"
INSERT INTO processes (id, kind_id, server_uuid, address) VALUES
  (1, 1, 'OLD', '%0'), (2, 1, 'REFUSED', '%0'), (3, 1, 'CONSOLE', '%0'),
  (4, 1, 'PRE', '%0'), (5, 1, 'NEWER', '${PANE}');
INSERT INTO tmux_servers (server_uuid, socket_path, first_seen_at) VALUES
  ('OLD',     '${SOCKS}/endless-tasks',   'x'),
  ('REFUSED', '${E2E}/refused',           'x'),
  ('CONSOLE', '${SOCKS}/endless-console', 'x'),
  ('NEWER',   '${SOCKS}/endless-tasks',   'x');
INSERT INTO sessions (id, session_id, project_id, platform, state, process_id) VALUES
  (1, 's-old',     ${PROJECT}, 'claude', 'working', 1),
  (2, 's-refused', ${PROJECT}, 'claude', 'working', 2),
  (3, 's-console', ${PROJECT}, 'claude', 'idle',    3),
  (4, 's-pre',     ${PROJECT}, 'claude', 'idle',    4),
  (5, 's-newer',   ${PROJECT}, 'claude', 'working', 5);
SQL

rows="$(go_e2e session-query list-live --project-root "${E2E}/proj" 2>/dev/null \
    | python3 -c 'import json,sys
for r in json.load(sys.stdin)["rows"]:
    print(r["endless_session_id"], r["liveness"], str(r["endless_server_down"]).lower())')"

row_of() { printf '%s\n' "${rows}" | awk -v id="$1" '$1 == id { print $2, $3 }'; }

assert_eq "a socket now answering as another server: dead, not listed" "" "$(row_of 1)"
assert_eq "a socket file nothing listens on: dead, not listed" "" "$(row_of 2)"
assert_eq "a missing socket file of Endless's: unknown, and its server is down" \
    "unknown true" "$(row_of 3)"
assert_eq "a binding with no recorded socket: unknown, never judged" \
    "unknown false" "$(row_of 4)"
assert_eq "a pane on the running server: live" "live false" "$(row_of 5)"

state="$(sqlite3 "${E2E}/db/endless.db" "SELECT group_concat(state, ',') FROM (SELECT state FROM sessions ORDER BY id)")"
assert_eq "no verdict was written to sessions" "working,working,idle,idle,working" "${state}"

summary
