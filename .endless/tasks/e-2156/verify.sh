#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2156 and records what was true when E-2156
# landed. Edit it only if you ARE E-2156. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# E-2156 rebuilt `project status` and `project monitor` around tasks: three
# lists (urgent, now/next epics, other now/next), each task once, only the third
# list truncated and only by `project monitor`, no sessions as rows, no clock.
# Everything below renders against a throwaway database built from schema.sql;
# nothing here can reach the real one.

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"
RUN_DIR="$(mktemp -d)" || setup_error "could not create a temp dir"
trap 'rm -rf "${RUN_DIR}"' EXIT

command -v sqlite3 >/dev/null || setup_error "sqlite3 is required"
command -v uv >/dev/null || setup_error "uv is required"
command -v go >/dev/null || setup_error "go is required"

# ── 1. unit gate ────────────────────────────────────────────────────────────
section "1. Unit gate (fail-fast)"

gate() {
    local label="$1"; shift
    local log="${RUN_DIR}/gate-$(echo "${label}" | tr -c 'a-zA-Z0-9' '-').log"
    if "$@" >"${log}" 2>&1; then
        report_pass "${label}"
    else
        report_fail "${label}" "pass" "$(tail -25 "${log}")"
        printf '\n%sFAIL-FAST: unit gate red; later assertions suppressed.%s\n' "${RED}" "${RESET}"
        summary
    fi
}

gate "go test ./internal/projectstatuscmd/ ./internal/taskrow/ ./internal/sessionstatuscmd/" \
    go test ./internal/projectstatuscmd/ ./internal/taskrow/ ./internal/sessionstatuscmd/
gate "go test ./internal/monitor/" go test ./internal/monitor/
# The Python suite runs as it would in a plain shell. The CLI words its output
# differently when it detects an agent, and a verify run launched from a Claude
# session inherits the variables it detects one by — so tests pinning the human
# wording would fail here for reasons that have nothing to do with this task.
gate "Python suite" env -u ENDLESS_AUDIENCE -u CLAUDECODE -u CLAUDE_CODE_ENTRYPOINT -u CLAUDE_CODE_SESSION_ID \
    -u CLAUDE_CODE_HOST_SESSION_ID -u CLAUDE_AGENT_SDK_VERSION \
    uv run --project "${WT}" pytest "${WT}/tests" -q -x

EGO="${RUN_DIR}/endless-go"
gate "build endless-go" go build -o "${EGO}" ./cmd/endless-go

# ── fixture ─────────────────────────────────────────────────────────────────
DBDIR="${RUN_DIR}/fixture/db"
PROJ="${RUN_DIR}/fixture/proj"
mkdir -p "${DBDIR}" "${PROJ}" || setup_error "mkdir failed"
DB="${DBDIR}/endless.db"
sqlite3 "${DB}" < "${WT}/internal/schema/schema.sql" >/dev/null 2>&1 \
    || setup_error "could not apply the schema"

q() { sqlite3 "${DB}" "$1" >/dev/null 2>&1 || setup_error "fixture SQL failed: $1"; }

# type_id: 1 todo, 4 epic. updated_at descends with id within each list, except
# where a test says otherwise.
reset_fixture() {
    q "DELETE FROM sessions; DELETE FROM tasks; DELETE FROM projects;"
    q "INSERT INTO projects (id,name,path,status) VALUES (1,'demo','${PROJ}','active');"
}

# ps <args...> — one render, against the fixture, through the headless seam.
ps() { ( cd "${PROJ}" && "${EGO}" --db-dir "${DBDIR}" project-status --project-id 1 --cols 100 "$@" 2>&1 ); }

# order — the task ids of a frame, in render order, space-separated.
order() { grep -oE ' E-[0-9]+ ' | tr -d ' ' | tr '\n' ' ' | sed 's/ $//'; }

# ── 2. the three lists ──────────────────────────────────────────────────────
section "2. Three lists, in order, each task once"

reset_fixture
q "INSERT INTO tasks (id,project_id,title,status,phase,type_id,updated_at) VALUES
 (11,1,'urgent task','unverified','urgent',1,'2026-09-01T11:00:00'),
 (12,1,'urgent epic','unplanned', 'urgent',4,'2026-09-01T12:00:00'),
 (21,1,'now epic',   'ready',     'now',   4,'2026-09-01T09:00:00'),
 (22,1,'next epic',  'submitted', 'next',  4,'2026-09-01T10:00:00'),
 (31,1,'t unplanned','unplanned', 'now',   1,'2026-09-01T01:00:00'),
 (32,1,'t submitted','submitted', 'now',   1,'2026-09-01T02:00:00'),
 (33,1,'t ready',    'ready',     'next',  1,'2026-09-01T03:00:00'),
 (34,1,'t underway', 'underway',  'now',   1,'2026-09-01T04:00:00'),
 (35,1,'t unverified','unverified','next', 1,'2026-09-01T05:00:00'),
 (36,1,'t unreviewed','unreviewed','now',  3,'2026-09-01T06:00:00'),
 (37,1,'t revisit',  'revisit',   'now',   1,'2026-09-01T07:00:00'),
 (90,1,'closed',     'confirmed', 'now',   1,'2026-09-01T23:00:00'),
 (91,1,'a later one','ready',     'later', 1,'2026-09-01T23:00:00'),
 (92,1,'a later epic','ready',    'later', 4,'2026-09-01T22:00:00');"

OUT="$(ps)"
assert_eq "urgent, then now/next epics, then the rest — newest first within each" \
    "E-12 E-11 E-22 E-21 E-37 E-36 E-35 E-34 E-33 E-32 E-31" "$(order <<<"${OUT}")"
assert_eq "no task appears twice" \
    "" "$(order <<<"${OUT}" | tr ' ' '\n' | sort | uniq -d)"
assert_not_contains "terminal tasks are not listed" "E-90" "${OUT}"
assert_not_contains "later tasks are not in the default view" "E-91" "${OUT}"
assert_contains "the urgent epic carries its epic letter AND the urgent glyph" "✎ E E-12   !" "${OUT}"
assert_contains "unreviewed reads ☰, not the ⁇ net" "☰ R E-36" "${OUT}"
assert_not_contains "no row wears the ⁇ should-never-happen glyph" "⁇" "${OUT}"

# ── 3. untruncated lists ────────────────────────────────────────────────────
section "3. Urgent and epic lists never truncate"

reset_fixture
for i in $(seq 100 111); do
    q "INSERT INTO tasks (id,project_id,title,status,phase,type_id,updated_at) VALUES (${i},1,'u${i}','ready','urgent',1,'2026-09-01T10:00:00');"
done
for i in $(seq 200 211); do
    q "INSERT INTO tasks (id,project_id,title,status,phase,type_id,updated_at) VALUES (${i},1,'e${i}','ready','now',4,'2026-09-01T10:00:00');"
done
for i in $(seq 300 339); do
    q "INSERT INTO tasks (id,project_id,title,status,phase,type_id,updated_at) VALUES (${i},1,'o${i}','unplanned','now',1,'2026-09-01T10:00:00');"
done

count() { order <<<"$1" | tr ' ' '\n' | grep -c "^E-$2" || true; }

SMALL="$(ps --monitor --rows 8)"
assert_eq "every urgent task renders at --rows 8" "12" "$(count "${SMALL}" 1)"
assert_eq "every epic renders at --rows 8" "12" "$(count "${SMALL}" 2)"
assert_contains "the cut third list leaves a trace" "… 40 more (project status)" "${SMALL}"

# ── 4. only list 3 grows ────────────────────────────────────────────────────
section "4. Only the third list truncates, and by height"

AT40="$(ps --monitor --rows 40)"
AT50="$(ps --monitor --rows 50)"
n40="$(count "${AT40}" 3)"; n50="$(count "${AT50}" 3)"
assert_eq "more height buys more third-list rows (${n40} at 40, ${n50} at 50)" \
    "yes" "$( (( n50 > n40 && n40 > 0 )) && echo yes || echo no)"
assert_eq "the urgent list is the same at both heights" "$(count "${AT40}" 1)" "$(count "${AT50}" 1)"
assert_eq "the epic list is the same at both heights" "$(count "${AT40}" 2)" "$(count "${AT50}" 2)"
assert_eq "the 40-row frame fits 40 rows (fault-row allowance included)" \
    "yes" "$( (( $(wc -l <<<"${AT40}") + 1 <= 40 )) && echo yes || echo no)"

# ── 5. --sort ───────────────────────────────────────────────────────────────
section "5. --sort updated and --sort id differ"

reset_fixture
q "INSERT INTO tasks (id,project_id,title,status,phase,type_id,updated_at) VALUES
 (10,1,'old, touched today','ready','now',1,'2026-09-05T10:00:00'),
 (20,1,'mid','ready','now',1,'2026-09-02T10:00:00'),
 (30,1,'new, untouched','ready','now',1,'2026-09-01T10:00:00');"
assert_eq "--sort updated (the default) leads with the recently touched old task" \
    "E-10 E-20 E-30" "$(ps | order)"
assert_eq "--sort id leads with the newest id" "E-30 E-20 E-10" "$(ps --sort id | order)"
assert_contains "--sort refuses an unknown key" "--sort must be updated or id" "$(ps --sort title)"

# ── 6. project status truncates nothing; --later ────────────────────────────
section "6. project status truncates nothing; --later shows only later"

reset_fixture
for i in $(seq 300 359); do
    q "INSERT INTO tasks (id,project_id,title,status,phase,type_id,updated_at) VALUES (${i},1,'o${i}','unplanned','now',1,'2026-09-01T10:00:00');"
done
q "INSERT INTO tasks (id,project_id,title,status,phase,type_id,updated_at) VALUES
 (500,1,'later task','ready','later',1,'2026-09-01T10:00:00'),
 (501,1,'later epic','ready','later',4,'2026-09-01T09:00:00'),
 (502,1,'maybe task','ready','maybe',1,'2026-09-01T10:00:00');"
for rows in 5 20 1000; do
    assert_eq "project status at --rows ${rows} renders all 60" "60" "$(ps --rows "${rows}" | order | wc -w | tr -d ' ')"
done
assert_not_contains "project status prints no truncation footer" "more (project status)" "$(ps --rows 5)"
assert_eq "--later shows only later tasks, epic first" "E-501 E-500" "$(ps --later | order)"

# ── 7 & 8. sessions consulted, never rows ───────────────────────────────────
section "7–8. No session is a row; a live session marks its underway task ⟳, none ◷"

reset_fixture
q "INSERT INTO tasks (id,project_id,title,status,phase,type_id,updated_at) VALUES
 (40,1,'held','underway','now',1,'2026-09-01T10:00:00'),
 (41,1,'held, prompted','underway','now',1,'2026-09-01T09:00:00'),
 (42,1,'unverified, session still open','unverified','now',1,'2026-09-01T08:00:00'),
 (43,1,'parent','ready','now',4,'2026-09-01T07:00:00');"
q "INSERT INTO sessions (id,session_id,project_id,state,task_id,started_at,last_activity) VALUES
 (7001,'u-1',1,'working',40,'2026-09-01T09:00:00','2026-09-01T09:00:00'),
 (7002,'u-2',1,'prompted',41,'2026-09-01T09:00:00','2026-09-01T09:00:00'),
 (7003,'u-3',1,'idle',42,'2026-09-01T09:00:00','2026-09-01T09:00:00'),
 (7004,'u-4',1,'idle',NULL,'2026-09-01T09:00:00','2026-09-01T09:00:00');"
HELD="$(ps)"
for bad in "ES-" "7004" "●" "↑" "↩"; do
    assert_not_contains "no row carries ${bad}" "${bad}" "${HELD}"
done
assert_eq "four task rows and nothing else" "E-43 E-40 E-41 E-42" "$(order <<<"${HELD}")"
assert_contains "a task a live session holds reads ⟳" "⟳ T E-40" "${HELD}"
assert_contains "a task whose session is blocked on a prompt reads ⚠" "⚠ T E-41" "${HELD}"
assert_contains "an open session does not hide ☑ on unverified work" "☑ T E-42" "${HELD}"

q "UPDATE sessions SET state='ended' WHERE id=7001;"
assert_contains "the same task with no live session reads ◷" "◷ T E-40" "$(ps)"

# ── 9. no clock ─────────────────────────────────────────────────────────────
section "9. No age column: an unchanged row set renders byte-identically"

A="$(ps --monitor --rows 20)"
sleep 1.2
B="$(ps --monitor --rows 20)"
assert_eq "two renders a second apart are identical" "${A}" "${B}"

# ── 10. no coined term ──────────────────────────────────────────────────────
section "10. No name for these views but project status and project monitor"

REBUILT=(
    internal/projectstatuscmd/render.go
    internal/projectstatuscmd/json.go
    internal/projectstatuscmd/project_status.go
    internal/monitor/project_status.go
    internal/taskrow/taskrow.go
    src/endless/project_status_cmd.py
    tests/test_project_status.py
    docs/guide/appendix-a.md
    docs/guide/reference.md
)
HITS="$( { grep -n -i -E 'attention board|dashboard|attention view|task view|attention triage' "${REBUILT[@]}";
          grep -n -i -w 'board' "${REBUILT[@]}"; } \
        | grep -v -F 'session monitor`** — the live dashboard' || true)"
assert_eq "no coined synonym in the rebuilt files" "" "${HITS}"

summary
