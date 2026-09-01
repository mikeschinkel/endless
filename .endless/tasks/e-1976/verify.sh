#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1976 and records what was true when E-1976
# landed. Edit it only if you ARE E-1976. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1976 verification — the project attention board.
#
# Before: Endless had `session status` / `session monitor`, which answer "what is
# next for the task I am on". Nothing answered the other question — "what, across
# every session in this project, is claiming a person's attention". The only
# signal for that was a red asterisk on a tmux window tab, which does not scale
# to ~70 concurrent sessions, and 57 unverified tasks in `endless` alone (75
# machine-wide, 71 of them 30–90 days old) had accumulated precisely because
# nothing kept them in view.
#
# After: `endless project status` (snapshot) and `endless project monitor` (live)
# render one ranked board of every attention claim in a project, loudest first,
# with a live session and the task it claimed folded onto one row. The metadata
# card that used to hold the name `project status` is now `endless project info`.
#
# Three properties carry the design, and each has a failure mode this suite
# reproduces rather than asserts:
#
#   1. RANK. Ranking is what makes the board a board. The order is the enum's
#      declaration order, so there is no second list to keep in step.
#   2. THE CAP IS PER GROUP. A board-wide cap with unverified ranked near the
#      top spends every row on the backlog, and the sessions the board exists to
#      triage never render.
#   3. THE FRAME FITS ITS PANE. An overrun frame does not wrap, it SCROLLS — and
#      a scrolled frame loses its TOP, which on a ranked board is the loudest
#      rows. The board therefore spends a measured height budget rather than
#      discovering it at paint time.
#
# Deliberately NOT built, and filed instead as E-2091: the `waiting` rank has no
# live producer. No installed Claude hook fires when Claude asks for permission,
# and `sessions.state`'s `needs_input` value is written only on INSERT and on the
# revive-an-ended-row CASE — so every row carrying it today is a session that
# registered and never had a turn. Section 6 proves the rank is BUILT and that
# the query deliberately withholds it, which is what makes E-2091 a one-line
# change rather than a feature.
#
# Run it through the runner, from anywhere inside the worktree:
#   just verify
#
# What it proves:
#   1. FAIL-FAST unit gate: the Go and Python suites this task owns pass.
#      Everything below runs the same source, so a red gate makes it all noise.
#   2. ONE implementation, not two: the live-pane loop and the fault badge were
#      EXTRACTED, not copied, and `session status` still drives the same code
#      through its own tests.
#   3. The rename landed whole: `project info` is the card, `project status` is
#      the board, and nothing still points at the old name.
#   4. End to end against a throwaway database: every rank renders with the right
#      glyph, a session and its task are ONE row, and the statuses that claim
#      nothing stay off the board.
#   5. The cap is per group and the frame fits its budget — the two properties
#      whose absence makes the board unreadable at real backlog sizes.
#   6. The withheld rank: `waiting` is built, ranked and bold, and the query
#      excludes today's dishonest producer.
#   7. Machine output stays whole and parseable, and the tmux layout is what
#      E-1815 specified.
#   8. Nothing legal became collateral damage: `session status`, `session
#      monitor` and the sixteen rowcap listings are unchanged.
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

# Refuse a direct run, and pick up the shared vocabulary (section, report_pass,
# report_fail, assert_*, setup_error, summary — plus the TAP stream the runner
# normalizes). Sourced as the FIRST executable statement so the refusal fires
# before anything else in this file does.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

# The runner names this suite's own directory; a hand-typed path would have to
# match a directory-casing convention it cannot see (.endless/tasks/CLAUDE.md).
SUITE_DIR="${ENDLESS_VERIFY_DIR:-$(dirname "${BASH_SOURCE[0]}")}"

# Everything this suite writes goes in the runner's own per-run temp dir, so the
# isolation it built covers the logs and the fixture database too. Nothing lands
# in /tmp to be found later and wondered about.
RUN_DIR="${ENDLESS_VERIFY_RUN}"

SCHEMA_SQL="${WT}/internal/schema/schema.sql"
GO_BIN="${WT}/bin/endless-go"
BOARD_SRC="${WT}/internal/projectstatuscmd/board.go"
QUERY_SRC="${WT}/internal/monitor/project_status.go"
WINDOW_SRC="${WT}/internal/projectstatuscmd/window.go"
PY_SRC="${WT}/src/endless/project_status_cmd.py"
CLI_SRC="${WT}/src/endless/cli.py"
ROWCAP_SRC="${WT}/src/endless/rowcap.py"
SESSION_SRC="${WT}/internal/sessionstatuscmd/session_status.go"
LIVEVIEW_SRC="${WT}/internal/liveview/liveview.go"




for f in "${SCHEMA_SQL}" "${BOARD_SRC}" "${QUERY_SRC}" "${WINDOW_SRC}" "${PY_SRC}" \
         "${CLI_SRC}" "${ROWCAP_SRC}" "${SESSION_SRC}" "${LIVEVIEW_SRC}"; do
    [[ -f "${f}" ]] || setup_error "missing ${f}"
done
command -v sqlite3 >/dev/null || setup_error "sqlite3 is required"
command -v uv >/dev/null || setup_error "uv is required"
command -v go >/dev/null || setup_error "go is required"

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
section "1. Unit gate (fail-fast)"

gate() {
    local label="$1"; shift
    local log="${RUN_DIR}/gate-$(echo "${label}" | tr -c 'a-zA-Z0-9' '-').log"
    if "$@" >"${log}" 2>&1; then
        report_pass "${label}"
    else
        report_fail "${label}" "pass" "failed — see ${log}"
        printf '\n%sFAIL-FAST: unit gate red; later assertions suppressed.%s\n' "${RED}" "${RESET}"
        # summary, not a bare exit: it closes the TAP plan, so the runner still
        # reports the checks that DID run rather than seeing a truncated stream.
        summary
    fi
}

# Built from THIS tree, so every assertion below exercises the source under test.
gate "go build ./..." go build ./...
gate "go test ./internal/projectstatuscmd/" go test ./internal/projectstatuscmd/
gate "go test ./internal/liveview/" go test ./internal/liveview/
gate "go test ./internal/faultbadge/ (the extracted badge)" go test ./internal/faultbadge/
# Named exactly, not matched by prefix. `-run 'TestProject|...'` also swept in
# TestProjectPath_TildeExpandsToHome, a neighbouring test this task never
# touched — it fails under the verify runner's temp $HOME because macOS hands
# out a symlinked temp path and that test compares a resolved path against an
# unresolved one. Filed as E-2094. A gate that reports somebody else's
# HOME-sensitivity as this task's failure is the mistake
# .endless/tasks/CLAUDE.md forbids, in miniature.
MONITOR_TESTS='^(TestProjectSessionNameIsTmuxSafe|TestBoardTaskStatusesComesFromTheVocabulary|TestProjectStatusRows.*|TestProjectByNameIsReadOnly)$'
gate "go test ./internal/monitor/ (this task's query tests)" \
    go test ./internal/monitor/ -run "${MONITOR_TESTS}"
gate "go test ./internal/taskstatus/ (the new AwaitsUser group)" go test ./internal/taskstatus/
gate "pytest tests/test_project_status.py" uv run pytest tests/test_project_status.py -q

# The two suites this task could break by extraction. They are the regression
# witnesses: `session status` now reaches its loop, its pane fit and its badge
# through other packages, and if the extraction changed behaviour it shows here.
gate "go test ./internal/sessionstatuscmd/ (the view the extraction moved out of)" \
    go test ./internal/sessionstatuscmd/
gate "pytest tests/test_rowcap.py (the shared cap this task parameterised)" \
    uv run pytest tests/test_rowcap.py -q

[[ -x "${GO_BIN}" ]] || setup_error "no endless-go at ${GO_BIN} — run 'just build'"

# ── 2. extracted, not copied ────────────────────────────────────────────────
# Two dashboards that each carried their own redraw loop, pane fit and fault
# badge would be two places for each rule to drift. The whole point of the
# extraction is that there is one of each.
section "2. One live-pane loop, one fault badge"

if grep -q 'liveview.Loop(' "${SESSION_SRC}"; then
    report_pass "session status drives the shared loop, not a private copy"
else
    report_fail "session status drives the shared loop" \
        "a liveview.Loop call in session_status.go" "absent"
fi

if grep -q 'liveview.Loop(' "${WT}/internal/projectstatuscmd/project_status.go"; then
    report_pass "project monitor drives the same shared loop"
else
    report_fail "project monitor drives the same shared loop" \
        "a liveview.Loop call in project_status.go" "absent"
fi

# The redraw cadence and the pane-sizing constants must have exactly one home,
# or the two boards repaint out of step and size themselves by different rules.
for sym in 'monitorInterval        = liveview.Interval' \
           'monitorPaneSlack       = liveview.PaneSlack' \
           'monitorPanePctOfWindow = liveview.PanePctOfWindow'; do
    if grep -qF "${sym}" "${SESSION_SRC}"; then
        report_pass "session status aliases ${sym%% *} rather than redefining it"
    else
        report_fail "session status aliases ${sym%% *}" "'${sym}'" "absent"
    fi
done

if [[ ! -f "${WT}/internal/sessionstatuscmd/faultbadge.go" ]] \
        && [[ -f "${WT}/internal/faultbadge/faultbadge.go" ]]; then
    report_pass "the fault badge moved to internal/faultbadge (no second copy)"
else
    report_fail "the fault badge moved to internal/faultbadge" \
        "faultbadge.go in internal/faultbadge only" "still in sessionstatuscmd, or missing"
fi

for src in "${SESSION_SRC}" "${BOARD_SRC}"; do
    if grep -q 'faultbadge.Render(' "${src}"; then
        report_pass "$(basename "${src}") renders the badge through the shared package"
    else
        report_fail "$(basename "${src}") renders the badge through the shared package" \
            "a faultbadge.Render call" "absent"
    fi
done

# liveview must not depend on the views it serves, or the extraction is a cycle
# waiting to happen.
if go list -deps ./internal/liveview | grep -qE 'endless/internal/(session|project)statuscmd'; then
    report_fail "internal/liveview does not depend on either view" \
        "no dependency on sessionstatuscmd/projectstatuscmd" "a cycle is forming"
else
    report_pass "internal/liveview does not depend on either view"
fi

# The board's status set must come from the vocabulary, not from a SQL literal.
# A status list inside a string is invisible to every tool, which is why it rots.
if grep -q 'taskstatus.SQLList(taskstatus.AwaitsUser)' "${QUERY_SRC}"; then
    report_pass "the board's status set is derived from taskstatus.AwaitsUser"
else
    report_fail "the board's status set is derived from taskstatus.AwaitsUser" \
        "a taskstatus.SQLList(taskstatus.AwaitsUser) call" "a hand-written status list"
fi

# CLAUDE.md's standing rule: Python reads SQLite in six files and no seventh.
if grep -qE '^\s*(import sqlite3|from endless import db)' "${PY_SRC}"; then
    report_fail "the Python pass-through is not a seventh SQLite reader" \
        "no sqlite3 / endless.db import" "it reads the database directly"
else
    report_pass "the Python pass-through is not a seventh SQLite reader"
fi

# ── 3. the rename landed whole ──────────────────────────────────────────────
section "3. project info is the card, project status is the board"

PY=$(uv run --project "${WT}" python -c 'import sys; print(sys.executable)' \
        2>"${RUN_DIR}/venv.log") || setup_error "could not resolve the venv (see ${RUN_DIR}/venv.log)"
BIN="$(dirname "${PY}")/endless"
[[ -x "${BIN}" ]] || setup_error "no endless console script at ${BIN}"

out=$("${BIN}" project --help 2>&1)
for verb in info status monitor; do
    if grep -qE "^  ${verb}\b" <<<"${out}"; then
        report_pass "endless project ${verb} exists"
    else
        report_fail "endless project ${verb} exists" "a '${verb}' row in project --help" "${out}"
    fi
done

out=$("${BIN}" project info --help 2>&1)
if grep -q "registration card" <<<"${out}"; then
    report_pass "project info is the metadata card"
else
    report_fail "project info is the metadata card" "'registration card' in its help" "${out}"
fi

out=$("${BIN}" project status --help 2>&1)
if grep -q "needs attention" <<<"${out}" && ! grep -q "Dependencies" <<<"${out}"; then
    report_pass "project status is the board, not the card"
else
    report_fail "project status is the board, not the card" \
        "'needs attention' and no card vocabulary" "${out}"
fi

# A stale doc is a rename that half-landed. The user-facing reference must not
# still tell someone to run the old spelling for the old purpose.
if grep -q "endless project info" "${WT}/docs/guide/reference.md"; then
    report_pass "the guide reference names project info"
else
    report_fail "the guide reference names project info" \
        "'endless project info' in docs/guide/reference.md" "absent"
fi
if grep -q "detailed status of current project" "${WT}/docs/guide/reference.md"; then
    report_fail "the guide no longer describes project status as the card" \
        "the old description gone" "still present"
else
    report_pass "the guide no longer describes project status as the card"
fi
if grep -q "endless project status <name>" "${WT}/src/endless/status.py"; then
    report_fail "the card's own error message points at its new name" \
        "'endless project info <name>'" "still names project status"
else
    report_pass "the card's own error message points at its new name"
fi

# ── setup: a hermetic database ──────────────────────────────────────────────
# The board pins the MAIN database unless an explicit --config-dir is given (see
# projectstatuscmd.resolveProject), which is the seam this fixture drives —
# exactly as e-698-verify.sh drives session-status's --task seam. Nothing here
# can reach the real database.
# In the runner's per-run dir, which it removes when the run ends — so there is
# no cleanup trap here to get wrong.
CFG="${RUN_DIR}/fixture/cfg"
FIXTURE_DB="${CFG}/endless.db"
PROJ="${RUN_DIR}/fixture/proj"
mkdir -p "${CFG}" "${PROJ}" || setup_error "mkdir failed"

sqlite3 "${FIXTURE_DB}" < "${SCHEMA_SQL}" >/dev/null 2>&1 \
    || setup_error "could not apply the schema"

# board <args...> — render against the fixture. Sets B_OUT / B_RC.
board() {
    B_OUT=$("${GO_BIN}" --config-dir "${CFG}" project-status --project demo "$@" 2>&1)
    B_RC=$?
}

# seed_base is the one-of-each fixture: every rank the board can draw, plus the
# statuses and states that must NOT draw one.
seed_base() {
    sqlite3 "${FIXTURE_DB}" "DELETE FROM sessions; DELETE FROM tasks; DELETE FROM projects;" \
        >/dev/null 2>&1 || return 1
    sqlite3 "${FIXTURE_DB}" "
      INSERT INTO projects (id,name,path,status) VALUES (1,'demo','${PROJ}','active');
      INSERT INTO tasks (id,project_id,title,status,phase,type_id,updated_at) VALUES
       (1,1,'verify me A','unverified','now',1,datetime('now','-1 hour')),
       (2,1,'verify me B','unverified','now',1,datetime('now','-2 hour')),
       (3,1,'read me','unreviewed','now',5,datetime('now','-3 hour')),
       (4,1,'approve me','submitted','now',1,datetime('now','-4 hour')),
       (5,1,'orphaned','underway','now',1,datetime('now','-5 hour')),
       (6,1,'spawnable','ready','now',1,datetime('now','-6 hour')),
       (7,1,'held by a session','underway','now',1,datetime('now','-7 hour')),
       (8,1,'already confirmed','confirmed','now',1,datetime('now','-8 hour')),
       (9,1,'not triaged yet','untriaged','now',1,datetime('now','-9 hour'));
      INSERT INTO sessions (id,session_id,project_id,state,task_id,started_at,last_activity) VALUES
       (10,'u-a',1,'working',7,datetime('now','-9 hour'),datetime('now','-10 minute')),
       (11,'u-b',1,'idle',NULL,datetime('now','-9 hour'),datetime('now','-20 minute')),
       (12,'u-c',1,'needs_input',NULL,datetime('now','-9 hour'),datetime('now','-30 minute')),
       (13,'u-d',1,'ended',NULL,datetime('now','-9 hour'),datetime('now','-40 minute'));
    " >/dev/null 2>&1
}

seed_base || setup_error "could not seed the fixture"

# ── 4. the board, end to end ────────────────────────────────────────────────
section "4. Every rank, rendered"

board --cols 110 --rows 40
if (( B_RC != 0 )); then
    setup_error "project-status failed against the fixture: ${B_OUT}"
fi

check_row() {
    local label="$1" pattern="$2"
    if grep -qE "${pattern}" <<<"${B_OUT}"; then
        report_pass "${label}"
    else
        report_fail "${label}" "a row matching /${pattern}/" "${B_OUT}"
    fi
}

check_row "☑ verify — an unverified task"           '^☑ T E-1 '
check_row "☰ read — an unreviewed outcome"          '^☰ B E-3 '
check_row "⚑ review — a submitted plan"             '^⚑ T E-4 '
check_row "◷ orphan — underway with no live session" '^◷ T E-5 '
check_row "‖ idle — a session whose turn ended"      '^‖ .*ES-11'
check_row "⟳ doing — a session working"              '^⟳ T E-7 .*ES-10'

# The board's defining join. A session working E-7 and the task E-7 are ONE row.
# Two lines saying the same thing is the failure this merge exists to prevent —
# and it would ALSO mis-rank, since E-7 would read as an orphan nobody holds.
if [[ "$(grep -c 'E-7 ' <<<"${B_OUT}")" == "1" ]] && ! grep -qE '^◷ T E-7 ' <<<"${B_OUT}"; then
    report_pass "a session and the task it claimed are ONE row, and not an orphan"
else
    report_fail "a session and the task it claimed are ONE row" \
        "exactly one E-7 row, classified ⟳ not ◷" "${B_OUT}"
fi

# Statuses and states that claim nothing must stay off the board.
for absent in 'E-6 ' 'E-8 ' 'E-9 ' 'ES-12' 'ES-13'; do
    if grep -qF "${absent}" <<<"${B_OUT}"; then
        report_fail "the default board omits ${absent}" "absent" "rendered anyway"
    else
        report_pass "the default board omits ${absent}"
    fi
done

# --all reaches exactly one more rank: `ready`, a claim on capacity rather than
# attention. It must not become a general amnesty.
board --cols 110 --rows 40 --all
if grep -qE '^▶ T E-6 ' <<<"${B_OUT}"; then
    report_pass "--all adds ▶ ready (spawnable work)"
else
    report_fail "--all adds ▶ ready" "a '▶ T E-6' row" "${B_OUT}"
fi
if grep -qE 'E-8 |E-9 ' <<<"${B_OUT}"; then
    report_fail "--all is not a general amnesty" \
        "confirmed and untriaged still absent" "${B_OUT}"
else
    report_pass "--all is not a general amnesty (confirmed/untriaged stay off)"
fi

# Rank order, observed through the render. This is the property that makes it a
# board rather than a list.
board --cols 110 --rows 40
order=$(grep -oE '^[☑☰⚑◷‖⟳]' <<<"${B_OUT}" | tr -d '\n')
if [[ "${order}" == "☑☑☰⚑◷‖⟳" ]]; then
    report_pass "ranks render loudest-first: verify, read, review, orphan, idle, doing"
else
    report_fail "ranks render loudest-first" "☑☑☰⚑◷‖⟳" "${order}"
fi

# Within a rank, newest first — because the cap makes the top of a group the only
# part most people read, and the newest event is the actionable one.
if [[ "$(grep -oE '^☑ T E-[0-9]+' <<<"${B_OUT}" | head -1)" == "☑ T E-1" ]]; then
    report_pass "within a rank the most recent row is on top"
else
    report_fail "within a rank the most recent row is on top" \
        "E-1 (1h) above E-2 (2h)" "${B_OUT}"
fi

# The board names the project it is looking at. An unlabelled board is ambiguous
# the moment a second one is open, and it is designed to be left open all day.
if head -1 <<<"${B_OUT}" | grep -q '^demo · '; then
    report_pass "the legend names the project"
else
    report_fail "the legend names the project" "a 'demo · ' prefix" "$(head -1 <<<"${B_OUT}")"
fi

# The legend names only what is on screen, so the eye learns the glyphs it sees.
if head -1 <<<"${B_OUT}" | grep -q '▶'; then
    report_fail "the legend names only present glyphs" "no ▶ without a ready row" \
        "$(head -1 <<<"${B_OUT}")"
else
    report_pass "the legend names only present glyphs"
fi

sqlite3 "${FIXTURE_DB}" "DELETE FROM sessions; DELETE FROM tasks;" >/dev/null 2>&1
board --cols 110 --rows 40
if grep -q "nothing needs attention in demo" <<<"${B_OUT}"; then
    report_pass "an empty board says so, and names the project it found nothing in"
else
    report_fail "an empty board says so" "'nothing needs attention in demo'" "${B_OUT}"
fi
seed_base || setup_error "could not reseed"

# ── 5. the two properties that make it readable ─────────────────────────────
section "5. The cap is per group; the frame fits its pane"

# 60 unverified tasks against one working session: the shape of the real
# database, where 57 unverified had buried everything else.
seed_bulk() {
    local sql=""
    sqlite3 "${FIXTURE_DB}" "DELETE FROM sessions; DELETE FROM tasks;" >/dev/null 2>&1 || return 1
    for ((i = 1; i <= 60; i++)); do
        sql+="INSERT INTO tasks (id,project_id,title,status,phase,type_id,updated_at)
              VALUES (${i},1,'backlog ${i}','unverified','now',1,
                      datetime('now','-${i} hours'));"
    done
    sql+="INSERT INTO tasks (id,project_id,title,status,phase,type_id,updated_at)
          VALUES (900,1,'held','underway','now',1,datetime('now','-1 hour'));"
    sql+="INSERT INTO sessions (id,session_id,project_id,state,task_id,started_at,last_activity)
          VALUES (900,'u-bulk',1,'working',900,datetime('now','-2 hours'),datetime('now','-1 minute'));"
    sqlite3 "${FIXTURE_DB}" "${sql}" >/dev/null 2>&1
}
seed_bulk || setup_error "could not seed the bulk fixture"

# THE defect the per-group cap exists to prevent. A board-wide cap of 10 renders
# ten unverified rows and stops; the session — the thing the board was built to
# triage — never appears.
board --cols 110 --rows 40 --limit 10
if grep -qE '^⟳ ' <<<"${B_OUT}"; then
    report_pass "60 unverified tasks do not crowd the working session off the board"
else
    report_fail "60 unverified tasks do not crowd the working session off the board" \
        "a ⟳ row alongside the backlog" "${B_OUT}"
fi
if grep -qF -- "more unverified (--no-limit)" <<<"${B_OUT}"; then
    report_pass "the truncated rank names what it dropped, and the flag"
else
    report_fail "the truncated rank names what it dropped" \
        "'… N more unverified (--no-limit)'" "${B_OUT}"
fi

# The noun is the GROUP's, not "rows": on this board what was dropped matters as
# much as how many.
if grep -qF -- "more rows (--no-limit)" <<<"${B_OUT}"; then
    report_fail "the footer names the rank, not 'rows'" \
        "'more unverified'" "the generic 'more rows'"
else
    report_pass "the footer names the rank, not 'rows'"
fi

# THE property a ranked board cannot do without. An overrun frame scrolls, and a
# scrolled frame loses its top — which is the loudest rows. Swept across the
# budget because the failure is a boundary.
overrun=""
for rows in 6 8 10 12 16 20 24 30 40 60; do
    board --cols 110 --rows "${rows}"
    lines=$(grep -c '' <<<"${B_OUT}")
    (( lines > rows )) && overrun="${overrun} ${rows}(${lines})"
done
if [[ -z "${overrun}" ]]; then
    report_pass "the frame never exceeds its height budget (swept 6–60 rows)"
else
    report_fail "the frame never exceeds its height budget" \
        "every frame ≤ its --rows budget" "overran at:${overrun}"
fi

# ...and it must not fit by dropping a whole rank. A board showing only the
# backlog has stopped answering the question it was built for.
board --cols 110 --rows 8
if grep -qE '^☑ ' <<<"${B_OUT}" && grep -qE '^⟳ ' <<<"${B_OUT}"; then
    report_pass "even an 8-row board keeps every rank present"
else
    report_fail "even an 8-row board keeps every rank present" \
        "both a ☑ and a ⟳ row" "${B_OUT}"
fi

# The top of the board is what the ranking is FOR. It must survive every budget.
for rows in 6 10 20 40; do
    board --cols 110 --rows "${rows}"
    if [[ "$(sed -n '2p' <<<"${B_OUT}" | cut -c1-1)" == "☑" ]]; then
        report_pass "at ${rows} rows the loudest rank is still the first row"
    else
        report_fail "at ${rows} rows the loudest rank is still the first row" \
            "a ☑ row directly under the legend" "${B_OUT}"
    fi
done

# The defect reported against the first landing: run in a plain terminal or any
# single-pane window, the board drew 27 lines into a 44-row pane and left 17 rows
# of dead space. `boardPctOfWindow` reserves a third of the window for the SHELL
# pane beneath the board — and reserves it for nothing when the board is alone.
if grep -q 'if n := monitor.PaneWindowPanes(pane); n == 1 {' "${LIVEVIEW_SRC}"; then
    report_pass "the window reservation applies only when the board SHARES its window"
else
    report_fail "the window reservation applies only when the board shares its window" \
        "a window_panes == 1 branch lifting the cap" \
        "the board reserves room for a pane that may not exist"
fi
if grep -q 'pct = 100' "${LIVEVIEW_SRC}"; then
    report_pass "alone in its window, the board takes the whole height"
else
    report_fail "alone in its window, the board takes the whole height" \
        "the solo case raising the share to 100" "absent"
fi

# ...and the behaviour, not just the branch: given rows to spend, a 44-row budget
# must produce a frame that fills it rather than one two-thirds its size.
#
# --limit 40 so the BUDGET is the binding constraint. Under the default 10 the
# per-group cap binds first on this two-group fixture and the frame is short for
# a reason that has nothing to do with the defect — which is what the first
# version of this check measured, and why it failed against correct code.
board --cols 110 --rows 44 --limit 40
lines=$(grep -c '' <<<"${B_OUT}")
if (( lines >= 40 && lines <= 44 )); then
    report_pass "a 44-row budget yields a ${lines}-line frame — the pane is filled"
else
    report_fail "a 44-row budget fills the pane" "40-44 lines" "${lines}"
fi

board --cols 110 --rows 200 --no-limit
if [[ "$(grep -cE '^☑ ' <<<"${B_OUT}")" == "60" ]]; then
    report_pass "--no-limit renders all 60 unverified rows"
else
    report_fail "--no-limit renders all 60 unverified rows" "60 ☑ rows" \
        "$(grep -cE '^☑ ' <<<"${B_OUT}")"
fi
if grep -qF -- "more unverified" <<<"${B_OUT}"; then
    report_fail "--no-limit prints no footer" "nothing omitted, nothing announced" \
        "footer still printed"
else
    report_pass "--no-limit prints no footer"
fi

# Same flags, same refusals as every other Endless listing. The board's cap is a
# different UNIT, never a different contract.
out=$("${BIN}" project status --limit 5 --no-limit 2>&1)
if grep -q "mutually exclusive" <<<"${out}"; then
    report_pass "--limit and --no-limit are refused together"
else
    report_fail "--limit and --no-limit are refused together" \
        "the shared rowcap refusal" "${out}"
fi
out=$("${BIN}" project status --limit 0 2>&1)
if grep -qF -- "--no-limit" <<<"${out}"; then
    report_pass "--limit 0 is refused, and points at the flag meant"
else
    report_fail "--limit 0 is refused, and points at --no-limit" "a pointer to --no-limit" "${out}"
fi

# ── 6. the rank deliberately withheld (E-2091) ──────────────────────────────
section "6. The waiting rank is built, and its producer is not"

seed_base || setup_error "could not reseed"

# The rank exists: declared first, glyphed, and bold — the one rank E-1815 calls
# load-bearing for auto-spawn's attention cap.
if grep -q 'actWaiting action = iota' "${BOARD_SRC}"; then
    report_pass "actWaiting is the FIRST rank — a blocked session is a hard stop"
else
    report_fail "actWaiting is the first rank" "'actWaiting action = iota'" "reordered or absent"
fi
if grep -qE 'actWaiting: \{"⚠", "waiting"' "${BOARD_SRC}"; then
    report_pass "the waiting rank has its glyph and label"
else
    report_fail "the waiting rank has its glyph and label" 'actWaiting in actionMeta' "absent"
fi
if grep -q 'case actWaiting:' "${BOARD_SRC}" && grep -q 'liveview.Strong' "${BOARD_SRC}"; then
    report_pass "the waiting rank renders bold — 'loud', theme-independently"
else
    report_fail "the waiting rank renders bold" "a liveview.Strong arm for actWaiting" "absent"
fi
if grep -q '"needs_input":' "${BOARD_SRC}"; then
    report_pass "classify() already routes needs_input to it (E-2091 needs no change here)"
else
    report_fail "classify() routes needs_input to the waiting rank" \
        "a needs_input case" "absent"
fi

# ...and the producer is withheld, in ONE place, on purpose. Today's needs_input
# rows are sessions that registered and never had a turn; ranking those loudest
# would make the top of every board permanent noise.
if grep -q "boardSessionStates = \`'working','idle'\`" "${QUERY_SRC}"; then
    report_pass "the query withholds needs_input in exactly one place"
else
    report_fail "the query withholds needs_input in exactly one place" \
        "boardSessionStates = 'working','idle'" "changed or scattered"
fi
if grep -q 'E-2091' "${QUERY_SRC}"; then
    report_pass "the withholding names the task that lifts it"
else
    report_fail "the withholding names the task that lifts it" "E-2091 cited" "absent"
fi

board --cols 110 --rows 40
if grep -q '⚠' <<<"${B_OUT}"; then
    report_fail "no ⚠ row renders while the state is dishonest" \
        "no waiting rows from a seeded needs_input session" "${B_OUT}"
else
    report_pass "no ⚠ row renders while the state is dishonest"
fi

# ── 7. machine output and the tmux layout ───────────────────────────────────
section "7. --json, and the layout E-1815 specified"

seed_bulk || setup_error "could not reseed the bulk fixture"
B_OUT=$("${GO_BIN}" --config-dir "${CFG}" project-status --project demo --json 2>&1)
n=$("${PY}" -c 'import json,sys; d=json.load(sys.stdin); print(len(d["rows"]))' <<<"${B_OUT}" 2>&1)
if [[ "${n}" == "61" ]]; then
    report_pass "--json is uncapped (61 rows) and parses"
else
    report_fail "--json is uncapped and parses" "61" "${n}"
fi
first=$("${PY}" -c 'import json,sys; print(json.load(sys.stdin)["rows"][0]["action"])' <<<"${B_OUT}" 2>&1)
if [[ "${first}" == "verify" ]]; then
    report_pass "--json keeps the rank order, and names the action as a label"
else
    report_fail "--json keeps the rank order" "first row action 'verify'" "${first}"
fi

# The tmux argv builders are pure functions precisely so the layout can be pinned
# without a server — creating a real session to inspect it would be both slow and
# a side effect on the user's machine. What they encode is E-1815's topology: its
# OWN session, created DETACHED so it never steals focus, a bare shell beside the
# board, and exact session targeting.
#
# Driven as named Go tests rather than re-asserted here as greps. A grep for a
# source line is a second, weaker copy of an assertion that already exists, and
# it fails on a reformat rather than on a regression — which is how three of
# these read on their first draft.
layout_check() {
    local label="$1" test_name="$2"
    if go test ./internal/projectstatuscmd/ -run "^${test_name}$" -count=1 \
            >"${RUN_DIR}/${test_name}.log" 2>&1; then
        report_pass "${label}"
    else
        report_fail "${label}" "pass" "failed — see ${RUN_DIR}/${test_name}.log"
    fi
}
layout_check "the session is created DETACHED, and its first pane is the SHELL" \
    TestNewSessionArgsIsADetachedShell
layout_check "the board is inserted ABOVE the shell, guessing no height" \
    TestSplitBoardArgsInsertsAboveTheShell
layout_check "focus is handed back to the shell (the board is read, not typed in)" \
    TestFocusReturnsToTheShell
layout_check "the ownership mark carries BOTH facts: whose, and which project" \
    TestOwnershipMarkCarriesBothFacts
layout_check "the ownership argv omits the = prefix tmux refuses on option commands" \
    TestOwnershipMarkTargetsCarryNoEqualsPrefix
layout_check "every session target is EXACT (=name, so a prefix cannot match another session)" \
    TestSessionTargetsAreExact
layout_check "the monitor pane names its project rather than trusting cwd" \
    TestMonitorCommandNamesTheProject

# Naming. A tmux status line truncates a session name to nine characters on the
# machine this was reported from, so what distinguishes two boards has to be in
# the FIRST nine — not merely somewhere in the full name. Two shapes failed that
# before the current default, and both looked fine in `tmux ls`:
# `{{project}}-monitor` showed `endless-m`, a mangled twin of the user's own
# `endless` session beside it; a fixed `e-monitor` with a `-{{project}}` suffix
# showed `e-monitor` for every project.
#
# Driven as Go tests, which call the shipped functions. Restating an expected
# name in this file would assert only that the copy agrees with itself.
name_check() {
    local label="$1" pkg="$2" test_name="$3"
    if go test "${pkg}" -run "^${test_name}$" -count=1 \
            >"${RUN_DIR}/${test_name}.log" 2>&1; then
        report_pass "${label}"
    else
        report_fail "${label}" "pass" "failed — see ${RUN_DIR}/${test_name}.log"
    fi
}
name_check "the DEFAULT name leads with the PROJECT, inside the 9-column tab" \
    ./internal/projectstatuscmd/ TestDefaultNameLeadsWithTheProject
name_check "a user-chosen project name is folded into a legal tmux name" \
    ./internal/monitor/ TestSanitizeTmuxName
name_check "{{project}} and {{.Project}} are both accepted" \
    ./internal/projectstatuscmd/ TestSessionNameTemplateForms
name_check "the fold applies to the RENDERED name, not just the substitution" \
    ./internal/projectstatuscmd/ TestSessionNameIsFoldedAfterRendering
name_check "a broken template falls back to the default AND says why" \
    ./internal/projectstatuscmd/ TestBadTemplateFallsBackAndSaysSo
name_check "tmux.session_name is actually READ from layered config" \
    ./internal/projectstatuscmd/ TestSessionNameTemplateReadsProjectConfig
name_check "an unparseable config file cannot stop the board from opening" \
    ./internal/projectstatuscmd/ TestSessionNameTemplateSurvivesABrokenConfig
name_check "the tmux config field merges per field, project over global" \
    ./internal/config/ TestTmuxSessionNameMerge

# go-cfgstore PANICS without a package-global logger, and reaches it on the path
# where it CREATES a missing config file. Nothing set that logger: the two
# pre-existing config.Load call sites survived only because a developed machine
# has ~/.config/endless/config.json already. A fresh install did not — and
# neither does this suite, which the runner gives a temp HOME. Found by that
# temp HOME, fixed at the binary's entry point.
if grep -q "cfgstore.SetLogger" "${WT}/cmd/endless-go/main.go"; then
    report_pass "the binary sets cfgstore's logger before anything can load config"
else
    report_fail "the binary sets cfgstore's logger" \
        "a cfgstore.SetLogger call in main()" \
        "absent — a fresh install panics on the first config read"
fi

# The two defects the first landing shipped, reported live and fixed here.
#
# Ordering: the board shrinks its own pane on first paint, so building the board
# first and splitting a shell off it races that shrink — E-1851 learned this in
# spawnlaunchcmd.buildSpawnLayout and this launcher shipped with it backwards.
# When the split loses, the window has no second pane at all, which is exactly
# what was reported.
if grep -q '"split-window", "-v", "-b", "-t", shellPane' "${WINDOW_SRC}"; then
    report_pass "the board is inserted above an EXISTING shell (E-1851's ordering, restored)"
else
    report_fail "the board is inserted above an existing shell" \
        "a -b split targeting the shell pane" \
        "the board is built first and the shell split off it — the racing order"
fi

# The argv builders above are shape tests, and a shape test cannot see a contract
# it never exercises. That is not hypothetical here: an earlier draft of the
# per-project guard stamped `@endless_project` on the session using tmux's
# exact-match `=` target, every argv test agreed with it, and tmux refused the
# argv outright — so the guard never wrote anything and nothing noticed.
#
# The stamp is gone (the naming makes the collision impossible), but the lesson
# stands: drive the real binary against a real tmux server for the layout, and
# skip cleanly where there is none.
if command -v tmux >/dev/null && tmux start-server 2>/dev/null; then
    probe_dir="${RUN_DIR}/tmuxprobe"
    mkdir -p "${probe_dir}"
    probe_session=$("${GO_BIN}" project-status --project endless --json >/dev/null 2>&1 && echo ok)
    probe="e1976-layout-$$"
    tmux kill-session -t "=${probe}" 2>/dev/null
    # Build the layout by hand with the SAME argv the launcher uses, so what is
    # tested is the shape those builders produce, not a paraphrase of it.
    if shell_pane=$(tmux new-session -d -s "${probe}" -c "${probe_dir}" -P -F '#{pane_id}' 2>/dev/null); then
        if tmux split-window -v -b -t "${shell_pane}" -c "${probe_dir}" \
                -P -F '#{pane_id}' -- sleep 30 >/dev/null 2>&1; then
            report_pass "tmux ACCEPTS the launcher's argv: a board inserted above an existing shell"
        else
            report_fail "tmux accepts the launcher's argv" \
                "split-window -v -b -t <shell pane> to succeed" "tmux refused it"
        fi
        n=$(tmux list-panes -t "=${probe}" 2>/dev/null | grep -c '.')
        if [[ "${n}" == "2" ]]; then
            report_pass "the layout really has TWO panes (the defect reported was one)"
        else
            report_fail "the layout really has two panes" "2" "${n}"
        fi
        tmux select-pane -t "${shell_pane}" 2>/dev/null
        active=$(tmux list-panes -t "=${probe}" -F '#{pane_id}:#{pane_active}' 2>/dev/null | grep ':1$' | cut -d: -f1)
        if [[ "${active}" == "${shell_pane}" ]]; then
            report_pass "tmux really leaves focus on the shell, not on the redraw loop"
        else
            report_fail "tmux really leaves focus on the shell" "${shell_pane}" "${active:-none}"
        fi
        # The ownership mark, round-tripped through a real server. This is the
        # check that could not exist as a shape test: an earlier draft's argv
        # carried tmux's exact-match `=` prefix, which the option commands
        # REFUSE, so the mark was never written and nothing noticed.
        if tmux set-option -t "${probe}" @endless_monitor demo 2>/dev/null \
                && [[ "$(tmux show-options -v -t "${probe}" @endless_monitor 2>/dev/null)" == "demo" ]]; then
            report_pass "tmux accepts the ownership mark and reads the value back"
        else
            report_fail "tmux accepts the ownership mark and reads the value back" \
                "set-option then show-options round-trips 'demo'" \
                "tmux refused the argv the launcher builds"
        fi
        tmux kill-session -t "=${probe}" 2>/dev/null

        # An UNMARKED session must read as foreign, not as ours. tmux reports an
        # unset user option as an ERROR rather than an empty string, so "no mark"
        # and "cannot read the mark" arrive alike — and both must land on the
        # cautious side, or the launcher is back to adopting a stranger's window.
        if tmux new-session -d -s "${probe}" 2>/dev/null; then
            if ! tmux show-options -v -t "${probe}" @endless_monitor >/dev/null 2>&1; then
                report_pass "an unmarked session cannot be mistaken for one Endless built"
            else
                report_fail "an unmarked session cannot be mistaken for one Endless built" \
                    "show-options to fail on an unset user option" "it succeeded"
            fi
            tmux kill-session -t "=${probe}" 2>/dev/null
        fi
    else
        report_skip "tmux accepts the launcher's argv" "could not create a probe session"
    fi
else
    report_skip "tmux accepts the launcher's argv" "no tmux server available"
fi

out=$("${BIN}" project monitor --help 2>&1)
if grep -q -- "--tmux" <<<"${out}" && grep -q "two-pane" <<<"${out}"; then
    report_pass "project monitor --tmux is the documented way into the layout"
else
    report_fail "project monitor --tmux is documented" "--tmux and 'two-pane' in the help" "${out}"
fi

# The E-698 trigger. The board fires the fire-once runner on every refresh, the
# same way session monitor does — and the code says plainly that this alone does
# NOT make it auto-spawn's on/off switch, because session monitor fires it too.
if grep -q 'FireJobs: true' "${WT}/internal/projectstatuscmd/project_status.go"; then
    report_pass "the live board fires the E-698 job runner on every refresh"
else
    report_fail "the live board fires the E-698 job runner" "FireJobs: true" "absent"
fi

# ── 8. nothing legal became collateral damage ───────────────────────────────
section "8. The neighbours are unchanged"

if "${GO_BIN}" session-status --help >/dev/null 2>&1 \
        || "${GO_BIN}" session-status --task 1 >/dev/null 2>&1; then
    report_pass "session-status still runs"
else
    # --task 1 against a sandbox with no such task exits non-zero legitimately;
    # what matters is that the subcommand still dispatches at all.
    if "${GO_BIN}" session-status --task 1 2>&1 | grep -qv 'unknown subcommand'; then
        report_pass "session-status still runs"
    else
        report_fail "session-status still runs" "the subcommand dispatches" "gone"
    fi
fi

# The sixteen surfaces that share rowcap must be untouched by its new parameter.
out=$("${BIN}" task list --help 2>&1)
if grep -q -- "--no-limit" <<<"${out}" && grep -q "default: 20" <<<"${out}"; then
    report_pass "the ordinary listings keep the 20-row default and both flags"
else
    report_fail "the ordinary listings keep the 20-row default" \
        "'default: 20' in task list --help" "${out}"
fi

if grep -q 'limit_options = limit_options_for()' "${ROWCAP_SRC}"; then
    report_pass "the bare decorator is built from the factory (one spelling, not two)"
else
    report_fail "the bare decorator is built from the factory" \
        "limit_options = limit_options_for()" "a forked second copy"
fi

# The per-group default lives on BOTH sides of the Python/Go boundary: Python
# resolves the cap (so the flags behave like every other listing) and passes a
# number, while Go keeps a default for a direct `endless-go project-status` call.
# Two constants that must agree, and nothing in either language can see the
# other — so the agreement is asserted here or not at all.
go_cap=$(grep -oE 'defaultGroupCap = [0-9]+' "${BOARD_SRC}" | grep -oE '[0-9]+')
py_cap=$(grep -oE 'DEFAULT_GROUP_CAP = [0-9]+' "${PY_SRC}" | grep -oE '[0-9]+')
if [[ -n "${go_cap}" && "${go_cap}" == "${py_cap}" ]]; then
    report_pass "the per-group default agrees across the Python/Go boundary (${go_cap})"
else
    report_fail "the per-group default agrees across the Python/Go boundary" \
        "the same number in board.go and project_status_cmd.py" \
        "Go=${go_cap:-absent} Python=${py_cap:-absent}"
fi

# ── summary ─────────────────────────────────────────────────────────────────
summary
