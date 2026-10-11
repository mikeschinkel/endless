#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2294 and records what was true when E-2294
# landed. Edit it only if you ARE E-2294. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2294: `task sessions <id>` lists the sessions holding a task with their
# relation, state, raw tmux liveness (live/dead/unknown/unbound), pane when
# live, and own-vs-focus; `task show`'s Touched-by block reports the same
# liveness through the same read.
#
# What is verified here:
#   A. Fail-fast: this task's Go and Python tests. The Python ones drive the
#      real CLI end to end against a private tmux server.
#   B. Through a binary built from this tree, against a throwaway database and
#      a private tmux server: `session-query session-liveness` reports each of
#      the four states, and a session whose row says `working` but whose pane
#      is gone reads `dead`.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
# A short socket directory of our own: a deep one overruns the 104-byte
# AF_UNIX path limit on macOS and tmux cannot start a server in it.
SOCK="$(mktemp -d /tmp/e2294.XXXXXX)" || setup_error "cannot create a tmux socket directory"
cleanup() {
    TMUX_TMPDIR="${SOCK}" tmux kill-server >/dev/null 2>&1
    rm -rf "${TMP}" "${SOCK}"
}
trap cleanup EXIT

command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required to seed the probe database"
command -v python3 >/dev/null 2>&1 || setup_error "python3 is required to read --json"
command -v tmux    >/dev/null 2>&1 || setup_error "tmux is required for the liveness probe"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

# internal/monitor only: the session-liveness verb's own proof is section B.
# internal/sessionquerycmd's TestEnsureClaudeID_LazyCreatesMissingRow passes
# only when it can reach a live tmux server, which this run deliberately
# cannot — a pre-existing dependence, not this task's.
if out=$(go test ./internal/monitor 2>&1); then
    report_pass "go test: monitor"
else
    report_fail "go test: monitor" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

if go test ./internal/monitor -run '^TestSessionsLiveness_ReportsRawStateAndLivePaneOnly$' -v 2>&1 \
        | grep -q '^--- PASS: TestSessionsLiveness_ReportsRawStateAndLivePaneOnly'; then
    report_pass "contract test runs and passes: TestSessionsLiveness_ReportsRawStateAndLivePaneOnly"
else
    report_fail "contract test runs and passes: TestSessionsLiveness_ReportsRawStateAndLivePaneOnly" \
        "--- PASS line" "no PASS line"
fi

if out=$(uv run pytest tests/test_task_sessions.py tests/test_task_show_sessions.py -q 2>&1); then
    report_pass "pytest: task sessions and task show's Touched-by block"
else
    report_fail "pytest: task sessions and task show's Touched-by block" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi
assert_not_contains "no task sessions test was skipped" "skipped" "${out}"

# ---------------------------------------------------------------------------
section "B. session-liveness reports the four raw states"
# ---------------------------------------------------------------------------

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go || setup_error "cannot build cmd/endless-go from this tree"
PROBE="${TMP}/db"
mkdir -p "${PROBE}"
DB="${PROBE}/endless.db"
GO=(env TMUX_TMPDIR="${SOCK}" "${BIN}" --db-dir "${PROBE}")

"${GO[@]}" session-query session-liveness 1 >/dev/null 2>&1 || true
[[ -f "${DB}" ]] || setup_error "the binary did not create ${DB}"

TMUX_TMPDIR="${SOCK}" tmux new-session -d -s e2294 || setup_error "cannot start a private tmux server"
TMUX_TMPDIR="${SOCK}" tmux set-option -g @server_uuid e2294-probe || setup_error "cannot name the tmux server"
PANE="$(TMUX_TMPDIR="${SOCK}" tmux display-message -p -t e2294 '#{pane_id}')" \
    || setup_error "cannot read the fixture pane id"

# 1 live: bound to the pane that exists. 2 dead: same server, pane absent, row
# still `working`. 3 unknown: a server that no longer exists. 4 unbound.
sqlite3 "${DB}" <<SQL || setup_error "cannot seed the probe database"
INSERT INTO projects (id, name, path) VALUES (1, 'e-2294-probe', '/tmp/e-2294-probe');
INSERT INTO processes (id, kind_id, server_uuid, address) VALUES
  (1, 1, 'e2294-probe', '${PANE}'), (2, 1, 'e2294-probe', '%99999'),
  (3, 1, 'vanished-server', '${PANE}');
INSERT INTO sessions (id, session_id, project_id, state, process_id) VALUES
  (1, 'u1', 1, 'working', 1), (2, 'u2', 1, 'working', 2),
  (3, 'u3', 1, 'idle', 3), (4, 'u4', 1, 'idle', NULL);
SQL

OUT="$("${GO[@]}" session-query session-liveness 1 2 3 4 5 2>"${TMP}/stderr.txt")" \
    || setup_error "session-liveness failed: $(cat "${TMP}/stderr.txt")"
ROWS="$(printf '%s' "${OUT}" | python3 -c '
import json, sys
for r in json.load(sys.stdin)["rows"]:
    print(r["session_id"], r["liveness"], r["pane"] or "-")')"

assert_eq "rows come back in argument order with raw states" \
    "1 live ${PANE}
2 dead -
3 unknown -
4 unbound -
5 unbound -" "${ROWS}"

if "${GO[@]}" session-query session-liveness ES-1 >/dev/null 2>"${TMP}/bad.txt"; then
    report_fail "a non-integer session id is refused" "non-zero exit" "exit 0"
else
    assert_contains "a non-integer session id is refused" "is not a positive integer" "$(cat "${TMP}/bad.txt")"
fi

summary
