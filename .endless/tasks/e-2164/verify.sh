#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2164 and records what was true when E-2164
# landed. Edit it only if you ARE E-2164. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2164: render the task ordering graph in session status, and add the
# advisory `precedes` and `conflicts_with` relations.
#
# What is verified here:
#   A. Fail-fast: this task's own Go and Python tests, and each contract test
#      by name so deleting one cannot turn the section green by absence.
#   B. The graph end to end, through a binary built from this tree against a
#      throwaway database: an off-list blocker renders; a `later` blocker
#      renders; a terminal blocker does not; a task with two blockers shows
#      both; `->` for precedes; `<>` for a declared conflict; a three-way
#      conflict is one set line; an edgeless task never appears; --graph;
#      --json carries the same lines; output is byte-stable; no edges at all
#      draws nothing.
#   C. Detected conflicts end to end: two worktrees touching one file render
#      `<>` once the worktree-paths job has run — and not before — and a
#      blocking relation between them replaces the `<>` with an arrow.
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

PKGS=(./internal/monitor ./internal/sessionstatuscmd ./internal/pathsjob ./cmd/endless-go)
if out=$(go test "${PKGS[@]}" 2>&1); then
    report_pass "go test: monitor, sessionstatuscmd, pathsjob, endless-go"
else
    report_fail "go test: monitor, sessionstatuscmd, pathsjob, endless-go" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi
if out=$(uv run pytest -q tests/test_advisory_relations.py tests/test_relations.py 2>&1); then
    report_pass "pytest: advisory relations, relations"
else
    report_fail "pytest: advisory relations, relations" "exit 0" "$(printf '%s' "${out}" | tail -30)"
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
for t in TestSessionGraphData_AddsOpenBlockersTransitively \
         TestSessionGraphData_CarriesPrecedesAndConflictsAmongNodes \
         TestWorktreeChangedPaths_EmptyCacheIsAMissWithoutGit \
         TestWorktreeChangedPaths_CommittedAndUncommittedPaths \
         TestWorktreeChangedPaths_MovedHeadIsStale \
         TestRefreshWorktreePathsCache_PrunesFinishedTasks; do
    contract ./internal/monitor "${t}"
done
for t in TestGraph_PlanExample TestGraph_GroupNeverOverstates \
         TestGraph_PrecedesRendersAdvisoryArrowAndOrdersFirst \
         TestGraph_NoEdgesRendersNothing TestGraph_ConflictOnlyTasksAppear \
         TestGraph_MutualConflictIsOneSetLine TestGraph_ChainShapedConflictIsPairs \
         TestGraph_CycleIsReportedNotTruncated TestGraph_RepeatsAndInFlightRenderDim \
         TestGraph_TwoBlockersTwoLines \
         TestGraph_WrapsAtArrowUnderFirstGroup TestGraph_ByteStableAcrossRenders \
         TestGraphSeeds_Exclusions TestGatherGraph_DetectedAndDeclaredConflicts \
         TestGatherGraph_OffListBlockerAndJSONParity TestRenderFrame_GraphAfterHiddenFooter; do
    contract ./internal/sessionstatuscmd "${t}"
done

# ---------------------------------------------------------------------------
section "B. The graph, end to end"
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

# Session 1 is on E-100 and has touched E-110..E-117 and E-130..E-132.
#   E-120 (later, off-list) blocks E-110;  E-121 (unverified, off-list) blocks E-110
#   E-122 (confirmed) blocks E-111 — terminal: imposes no order
#   E-112 precedes E-113
#   E-114 conflicts_with E-115 (declared)
#   E-130, E-131, E-132 mutually conflict
#   E-116 has no ordering edge at all
#   E-117 relates_to E-116 — not an ordering relation
sqlite3 "${DB}" <<'SQL' || setup_error "cannot seed the probe database"
INSERT INTO projects (id, name, path) VALUES (1, 'e-2164-probe', '/tmp/e-2164-probe-none');
INSERT INTO tasks (id, project_id, title, status, phase) VALUES
  (100,1,'the focal','underway','now'),
  (110,1,'two blockers','ready','now'), (111,1,'terminal blocker','ready','now'),
  (112,1,'should go first','ready','now'), (113,1,'should go second','ready','next'),
  (114,1,'declared a','ready','now'), (115,1,'declared b','ready','now'),
  (116,1,'no edges','ready','now'), (117,1,'relates only','ready','now'),
  (120,1,'later blocker','ready','later'), (121,1,'unverified blocker','unverified','now'),
  (122,1,'done blocker','confirmed','now'),
  (130,1,'set a','ready','now'), (131,1,'set b','ready','now'), (132,1,'set c','ready','now');
INSERT INTO sessions (id, project_id, state, task_id) VALUES (1,1,'working',100);
INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at) VALUES
  (1,100,1,'t','t'), (1,110,2,'t','t'), (1,111,2,'t','t'), (1,112,2,'t','t'),
  (1,113,2,'t','t'), (1,114,2,'t','t'), (1,115,2,'t','t'), (1,116,2,'t','t'),
  (1,117,2,'t','t'), (1,130,2,'t','t'), (1,131,2,'t','t'), (1,132,2,'t','t');
INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) VALUES
  ('task',120,'task',110,'blocks'), ('task',121,'task',110,'blocks'),
  ('task',122,'task',111,'blocks'),
  ('task',112,'task',113,'precedes'),
  ('task',114,'task',115,'conflicts_with'),
  ('task',130,'task',131,'conflicts_with'), ('task',132,'task',131,'conflicts_with'),
  ('task',130,'task',132,'conflicts_with'),
  ('task',117,'task',116,'relates_to');
SQL

board() { "${GO[@]}" session-status --task 100 --session 1 --cols 200 "$@" 2>"${TMP}/stderr.txt"; }
graph_lines() { printf '%s\n' "$1" | grep -E '^(E-|<> |cycle: )' || true; }

FRAME="$(board)"
GRAPH="$(graph_lines "${FRAME}")"
assert_eq "the graph renders under the rows, in derived order" \
"E-112 -> E-113
E-120 => E-110
E-121 => E-110
<> E-130 | E-131 | E-132
E-114 <> E-115" "${GRAPH}"
assert_contains "an off-list blocker in phase later renders" "E-120 => E-110" "${GRAPH}"
assert_contains "an unverified blocker still blocks and renders" "E-121 => E-110" "${GRAPH}"
assert_contains "a task with two blockers shows both, one line each" \
    $'E-120 => E-110\nE-121 => E-110' "${GRAPH}"
assert_not_contains "a terminal blocker does not render" "E-122" "${GRAPH}"
assert_not_contains "a task with no ordering edge never appears" "E-116" "${GRAPH}"
assert_not_contains "relates_to is not an ordering relation" "E-117" "${GRAPH}"
assert_not_contains "the viewing session's own task is not seeded" "E-100" "${GRAPH}"
assert_eq "the graph is byte-stable across renders" "${FRAME}" "$(board)"

ONLY="$(board --graph)"
assert_eq "--graph renders the graph alone" "${GRAPH}" "${ONLY}"

JSON_LINES="$("${GO[@]}" session-status --task 100 --session 1 --json 2>/dev/null |
    python3 -c 'import json,sys; print("\n".join(json.load(sys.stdin)["graph"]["lines"]))')"
assert_eq "--json carries exactly the lines the text draws" "${GRAPH}" "${JSON_LINES}"
SOURCES="$("${GO[@]}" session-status --task 100 --session 1 --json 2>/dev/null |
    python3 -c 'import json,sys; g=json.load(sys.stdin)["graph"]; print(" ".join(c["source"] for c in g["conflicts"]))')"
assert_eq "--json marks each conflict declared" "declared declared declared declared" "${SOURCES}"
ONLIST="$("${GO[@]}" session-status --task 100 --session 1 --json 2>/dev/null |
    python3 -c 'import json,sys; g=json.load(sys.stdin)["graph"]; print(" ".join(n["id"] for n in g["nodes"] if not n["on_list"]))')"
# E-121 is a row already (the row query walks open blockers upstream), so it
# is on the list; E-120 is a row too but phase later, so it is excluded as a
# seed and pulled back in only because it blocks one.
assert_eq "--json marks the pulled-in later blocker off-list" "E-120" "${ONLIST}"

# An in-flight blocker is the one a session is already on.
sqlite3 "${DB}" "INSERT INTO sessions (id, project_id, state, task_id) VALUES (2,1,'working',121);"
IN_FLIGHT="$("${GO[@]}" session-status --task 100 --session 1 --json 2>/dev/null |
    python3 -c 'import json,sys; g=json.load(sys.stdin)["graph"]; print([n["in_flight"] for n in g["nodes"] if n["id"]=="E-121"][0])')"
assert_eq "an in-flight blocker still renders, flagged in flight (drawn dim)" "True" "${IN_FLIGHT}"

# No edges at all: nothing — no header, no blank line.
sqlite3 "${DB}" "DELETE FROM task_deps;"
EMPTY="$(board)"
assert_eq "a session with no edges draws no graph line" "" "$(graph_lines "${EMPTY}")"
assert_not_contains "and no blank line either" $'\n\n' "${EMPTY}"
assert_contains "--graph says so rather than printing nothing" "no ordering relations" "$(board --graph)"

# ---------------------------------------------------------------------------
section "C. Detected conflicts, end to end"
# ---------------------------------------------------------------------------
# A real repository with two task worktrees that both change shared.txt.

export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@example.com GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@example.com
REPO="${TMP}/repo"
mkdir -p "${REPO}"
git -C "${REPO}" init -q --initial-branch=main || setup_error "git init"
printf 'x\n' > "${REPO}/shared.txt"
git -C "${REPO}" add shared.txt && git -C "${REPO}" commit -qm base || setup_error "base commit"
for id in 201 202; do
    git -C "${REPO}" worktree add -q -b "task/${id}" "${REPO}/.endless/worktrees/e-${id}" \
        || setup_error "worktree add e-${id}"
done
printf 'from 201\n' >> "${REPO}/.endless/worktrees/e-201/shared.txt"
git -C "${REPO}/.endless/worktrees/e-201" commit -qam "E-201: edit" || setup_error "commit in e-201"
printf 'from 202, uncommitted\n' >> "${REPO}/.endless/worktrees/e-202/shared.txt"

sqlite3 "${DB}" <<SQL || setup_error "cannot seed the detection fixture"
INSERT INTO projects (id, name, path) VALUES (2, 'e-2164-repo', '${REPO}');
INSERT INTO tasks (id, project_id, title, status, phase) VALUES
  (200,2,'the focal','underway','now'), (201,2,'edits shared','ready','now'),
  (202,2,'also edits shared','ready','now');
INSERT INTO sessions (id, project_id, state, task_id) VALUES (3,2,'working',200);
INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at) VALUES
  (3,200,1,'t','t'), (3,201,2,'t','t'), (3,202,2,'t','t');
SQL

board2() { "${GO[@]}" session-status --task 200 --session 3 --cols 200 "$@" 2>/dev/null; }
assert_eq "before the job has run, nothing is detected (the render path runs no git)" \
    "" "$(graph_lines "$(board2)")"

"${GO[@]}" jobs run --job worktree-paths >"${TMP}/job.txt" 2>&1 \
    || setup_error "jobs run --job worktree-paths failed: $(cat "${TMP}/job.txt")"
assert_eq "after the job, two worktrees touching one file render <>" \
    "E-201 <> E-202" "$(graph_lines "$(board2)")"
DETECTED="$("${GO[@]}" session-status --task 200 --session 3 --json 2>/dev/null |
    python3 -c 'import json,sys; c=json.load(sys.stdin)["graph"]["conflicts"][0]; print(c["source"], ",".join(c["paths"]))')"
assert_eq "--json marks it detected, naming the shared path" "detected shared.txt" "${DETECTED}"

sqlite3 "${DB}" "INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) VALUES ('task',201,'task',202,'conflicts_with');"
BOTH="$("${GO[@]}" session-status --task 200 --session 3 --json 2>/dev/null |
    python3 -c 'import json,sys; print(json.load(sys.stdin)["graph"]["conflicts"][0]["source"])')"
assert_eq "declared and detected together are marked both" "both" "${BOTH}"

sqlite3 "${DB}" "INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type) VALUES ('task',202,'task',201,'blocks');"
assert_eq "a blocking relation between them replaces the <> with an arrow" \
    "E-202 => E-201" "$(graph_lines "$(board2)")"

summary
