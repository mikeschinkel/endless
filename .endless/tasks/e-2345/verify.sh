#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2345 and records what was true when E-2345
# landed. Edit it only if you ARE E-2345. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2345: draw the session task's blocker chain in the status graph.
#
# What is verified here:
#   A. Fail-fast: this task's own Go tests, and each contract test by name so
#      deleting one cannot turn the section green by absence.
#   B. The E-2333 shape end to end, through a binary built from this tree
#      against a throwaway database: the own task is blocked by a task in
#      another project, itself blocked by another live session's task, and
#      the whole chain draws; one step downstream draws, with the downstream
#      task's own blocker chain, but not the downstream task's dependents;
#      a confirmed, unlanded own task still draws its blockers; a landed one
#      drops out.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
TMP="$(cd "${TMP}" && pwd -P)"
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
for t in TestSessionGraphData_TerminalOwnTaskBlockersDrawn \
         TestSessionGraphData_CrossProjectChainToOwnTask \
         TestSessionGraphData_DownstreamOneStepWithBlockerChains \
         TestSessionGraphData_BlocksCycleTerminates; do
    contract ./internal/monitor "${t}"
done
for t in TestGraphSeeds_BrightUnlandedRowsAndOwnTask TestGraphSeeds_OwnTaskUntilLanded \
         TestGatherGraph_OwnTaskSeedsAndIsPassedAsOwn TestGraph_RepeatsDimInFlightDoesNot \
         TestGraph_CycleIsReportedNotTruncated; do
    contract ./internal/sessionstatuscmd "${t}"
done

# ---------------------------------------------------------------------------
section "B. The own task's chain, end to end"
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

# Session 1 is on E-2333 (project 1). E-2208 (project 2) blocks it, and E-2206
# (project 2, worked by live session 2) blocks E-2208. Downstream: E-2333
# blocks E-2400, which blocks E-2401 (not to be drawn); E-2402 (later) blocks
# E-2400, and E-2403 blocks E-2402.
sqlite3 "${DB}" <<'SQL' || setup_error "cannot seed the probe database"
INSERT INTO projects (id, name, path) VALUES
  (1, 'e-2345-probe-a', '/tmp/e-2345-probe-a-none'), (2, 'e-2345-probe-b', '/tmp/e-2345-probe-b-none');
INSERT INTO tasks (id, project_id, title, status, phase) VALUES
  (2333,1,'own task','underway','now'),
  (2208,2,'mid blocker','ready','now'),
  (2206,2,'root blocker','underway','now'),
  (2400,1,'downstream','ready','now'),
  (2401,1,'downstream of downstream','ready','now'),
  (2402,1,'blocker of downstream','ready','later'),
  (2403,1,'its blocker','ready','now');
INSERT INTO sessions (id, project_id, state, task_id) VALUES (1,1,'working',2333), (2,2,'working',2206);
INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at) VALUES (1,2333,1,'t','t');
INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) VALUES
  ('task',2206,'task',2208,'blocks'), ('task',2208,'task',2333,'blocks'),
  ('task',2333,'task',2400,'blocks'), ('task',2400,'task',2401,'blocks'),
  ('task',2402,'task',2400,'blocks'), ('task',2403,'task',2402,'blocks');
SQL

board() { "${GO[@]}" session-status --task 2333 --session 1 --cols 200 "$@" 2>"${TMP}/stderr.txt"; }
graph_lines() { printf '%s\n' "$1" | grep -E '^(E-|<> |cycle: )' || true; }

GRAPH="$(graph_lines "$(board)")"
assert_eq "the own task's cross-project chain and one step downstream draw" \
"E-2206 => E-2208 => E-2333 => E-2400
E-2403 => E-2402 => E-2400" "${GRAPH}"
assert_not_contains "a downstream task's own dependents are not added" "E-2401" "${GRAPH}"
assert_eq "--graph renders the same lines" "${GRAPH}" "$(board --graph)"

INFLIGHT="$("${GO[@]}" session-status --task 2333 --session 1 --json 2>/dev/null |
    python3 -c 'import json,sys; g=json.load(sys.stdin)["graph"]; print([n["in_flight"] for n in g["nodes"] if n["id"]=="E-2206"][0])')"
assert_eq "--json still flags the other session's blocker in flight" "True" "${INFLIGHT}"

# Color is on only for a terminal, so render through a pseudo-terminal.
command -v script >/dev/null 2>&1 || setup_error "script(1) is required to render through a pty"
CMD=("${BIN}" --db-dir "${PROBE}" session-status --task 2333 --session 1 --cols 200 --graph)
if script --version >/dev/null 2>&1; then # util-linux: the command is one -c string
    COLORED="$(script -qec "$(printf '%q ' "${CMD[@]}")" /dev/null </dev/null 2>/dev/null)"
else # BSD/macOS: the command follows the output file
    COLORED="$(script -q /dev/null "${CMD[@]}" </dev/null 2>/dev/null)"
fi
assert_contains "the in-flight blocker draws bright, not dim" $'\e[1mE-2206' "${COLORED}"
assert_not_contains "the in-flight blocker is never dim" $'\e[2mE-2206' "${COLORED}"
assert_contains "a repeated id still draws dim" $'\e[2mE-2400' "${COLORED}"

# A confirmed own task that has not landed still draws what blocks it.
sqlite3 "${DB}" "UPDATE tasks SET status='confirmed' WHERE id=2333;"
assert_contains "a confirmed, unlanded own task still draws its blocker chain" \
    "E-2206 => E-2208 => E-2333" "$(graph_lines "$(board --graph)")"

# Once it lands, it is no longer a starting point.
sqlite3 "${DB}" "INSERT INTO task_landings (task_id, merge_commit_sha) VALUES (2333, 'abc1234');" \
    || setup_error "cannot record a landing for E-2333"
assert_not_contains "a landed own task drops out of the graph" "E-2333" "$(graph_lines "$(board --graph)")"

summary
