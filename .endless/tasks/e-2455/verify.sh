#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2455 and records what was true when E-2455
# landed. Edit it only if you ARE E-2455. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2455: show precedes edges to tasks stewarded by other sessions.
#
# What is verified here:
#   A. Fail-fast: this task's own Go tests, and each contract test by name so
#      deleting one cannot turn the section green by absence.
#   B. The E-2444 -> E-2445 shape end to end, through a binary built from this
#      tree against a throwaway database: a row's soft predecessor is another
#      session's task, so its row does not seed the graph, yet it is drawn at
#      the head of `->`, with its own blocker and soft predecessor; a finished
#      soft predecessor is not drawn. Here the row is left out as in flight
#      rather than as stewarded elsewhere — stewardship needs session liveness
#      a throwaway database does not carry — and graphSeeds leaves out both by
#      one rule; the stewarded flag itself is section A's sessionstatuscmd
#      contract test.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
TMP="$(cd "${TMP}" && pwd -P)"
trap 'rm -rf "${TMP}"' EXIT

command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required to seed the probe database"

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
contract ./internal/monitor TestSessionGraphData_WalksPrecedesPredecessors
contract ./internal/sessionstatuscmd TestGatherGraph_StewardedElsewherePrecedesPredecessorDrawn

# ---------------------------------------------------------------------------
section "B. A soft predecessor that is another session's task, end to end"
# ---------------------------------------------------------------------------
# Built from this tree. --db-dir names a throwaway database; --task/--session
# are the headless seam that reads it instead of pinning main.

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go || setup_error "cannot build cmd/endless-go from this tree"
PROBE="${TMP}/db"
mkdir -p "${PROBE}"
DB="${PROBE}/endless.db"
GO=("${BIN}" --db-dir "${PROBE}")
"${GO[@]}" session-status --task 1 >/dev/null 2>&1 || true
[[ -f "${DB}" ]] || setup_error "the binary did not create ${DB}"

# Session 1 is on E-2500 and has E-2445 and E-2444 on its list. E-2444 should
# precede E-2445 and is session 2's task. E-2430 blocks E-2444, E-2431 should
# precede it, and the confirmed E-2420 should precede E-2445.
sqlite3 "${DB}" <<'SQL' || setup_error "cannot seed the probe database"
INSERT INTO projects (id, name, path) VALUES (1, 'e-2455-probe', '/tmp/e-2455-probe-none');
INSERT INTO tasks (id, project_id, title, status, phase) VALUES
  (2500,1,'own task','underway','now'),
  (2445,1,'the dependent','ready','now'),
  (2444,1,'soft predecessor, other session','underway','now'),
  (2430,1,'its blocker','ready','now'),
  (2431,1,'its soft predecessor','ready','now'),
  (2420,1,'finished soft predecessor','confirmed','now');
INSERT INTO sessions (id, project_id, state, task_id) VALUES (1,1,'working',2500), (2,1,'working',2444);
INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at) VALUES
  (1,2500,1,'t','t'), (1,2445,1,'t','t'), (1,2444,1,'t','t'), (2,2444,1,'t','t');
INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) VALUES
  ('task',2444,'task',2445,'precedes'), ('task',2430,'task',2444,'blocks'),
  ('task',2431,'task',2444,'precedes'), ('task',2420,'task',2445,'precedes');
SQL

board() { "${GO[@]}" session-status --task 2500 --session 1 --cols 200 "$@" 2>"${TMP}/stderr.txt"; }
graph_lines() { printf '%s\n' "$1" | grep -E '^(E-|<> |cycle: )' || true; }

TABLE="$(board)"
assert_contains "the dependent is a row in session 1's table" "E-2445" "${TABLE}"
GRAPH="$(graph_lines "${TABLE}")"
assert_contains "another session's soft predecessor draws at the head of ->" "E-2444 -> E-2445" "${GRAPH}"
assert_contains "its blocker draws" "E-2430 => E-2444" "${GRAPH}"
assert_contains "its own soft predecessor draws" "E-2431" "${GRAPH}"
assert_not_contains "a finished soft predecessor does not draw" "E-2420" "${GRAPH}"
assert_eq "--graph renders the same lines" "${GRAPH}" "$(board --graph)"

summary
