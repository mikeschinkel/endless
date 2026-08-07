#!/usr/bin/env bash
#
# E-1914 verification script — the `session list` rework + per-session task hiding.
#
# Two threads:
#   A. `session list` drops the discussion Summary column, adds the active task
#      id and its title, renders state as a fixed-width glyph with a legend
#      (the fix for the ragged table `needs_input` used to cause), defaults to
#      the project enclosing cwd with --all-projects/--project around it, and
#      shows the Project column only when the output spans >1 project.
#   B. `session hide/unhide [<session>] --task <id>...` suppresses task rows from
#      ONE session's `session status`/`monitor` view, with an always-on
#      '… N hidden' footer, --show-hidden, --only-hidden, and --json.
#
# Storage is the session_hidden_tasks table, not a hidden_at column on
# session_tasks (ED-1545, revising E-1912's design): `session status` renders
# rows that have no session_tasks row at all — read-time children, dependents and
# upstream blockers — so a column would have forced hide to fabricate a touch
# that `task show` would then report as real.
#
# Run from anywhere inside the worktree:
#   esu && ./tests/tasks/e-1914-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Isolation: a throwaway git repo as project root under a temp dir, a temp
# XDG_CONFIG_HOME (its own DB) and XDG_CACHE_HOME. No real DB/ledger/cache is
# touched. The freshly-built worktree binary is exercised both ways: the Go
# renderer is driven directly via ./bin/endless-go session-status in its headless
# --task/--session mode, and the Python CLI runs from the worktree source with
# <worktree>/bin prepended to PATH.

set -u

# ─── output ─────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section()     { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}
summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'; return 1
}

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"; else report_fail "$1" "$2" "$3"; fi
}
# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output contains: $2" "$3"; fi
}
# assert_not_contains DESC NEEDLE HAYSTACK
assert_not_contains() {
    if [[ "$3" != *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output must NOT contain: $2" "$3"; fi
}

# ─── glyphs the renderers use (internal/sessionstatuscmd, src/endless/session_cmd.py) ──

HIDDEN_GLYPH="⊘"
WORKING_GLYPH="⟳"
NEEDS_INPUT_GLYPH="?"

# ─── shared fixture ──────────────────────────────────────────────────────────

WT=""; TMP=""; REPO=""; EGO=""; DBDIR=""

# E: the worktree's Python CLI against the isolated DB, from the repo dir.
E() { ( cd "$REPO" && uv run --project "$WT" endless "$@" ); }
# Q SQL: one scalar/row out of the isolated DB.
Q() { E sql "$1" --tsv 2>/dev/null; }
# GO_STATUS ...: the candidate Go renderer, headless, against the isolated DB.
# NO_COLOR + a wide --cols keep the output ANSI-free and untruncated so the
# row/glyph greps are reliable.
GO_STATUS() { NO_COLOR=1 "$EGO" --config-dir "$DBDIR" session-status --cols 200 "$@" 2>&1; }

# Task/session ids are fixed so every assertion can name them literally.
FOCAL=7001      # the claimed goal both sessions are on
SHARED=7002     # a task BOTH sessions touched — the per-session guarantee's subject
SOLO=7003       # a task only session 9001 touched
S1=9001
S2=9002

setup_fixture() {
    TMP="$(mktemp -d)"
    REPO="$TMP/repo"
    DBDIR="$TMP/xdg/endless"
    export XDG_CONFIG_HOME="$TMP/xdg"
    export XDG_CACHE_HOME="$TMP/cache"
    # Prepend the freshly-built worktree binary so the Python event bridge's
    # PATH fallback (cwd is the /tmp repo, not a self-dev worktree) execs
    # candidate code, not the stale global endless-go.
    export PATH="$WT/bin:$PATH"
    mkdir -p "$REPO" "$DBDIR"

    git -C "$REPO" init -q
    git -C "$REPO" config user.email verify@test
    git -C "$REPO" config user.name verify
    git -C "$REPO" commit -q --allow-empty -m "initial commit"

    E project register "$REPO" --name probe --label Probe --desc d --lang Go --status active >/dev/null 2>&1
    # A second registered project so the multi-project Project column is reachable.
    mkdir -p "$TMP/other"
    git -C "$TMP/other" init -q
    git -C "$TMP/other" config user.email verify@test
    git -C "$TMP/other" config user.name verify
    git -C "$TMP/other" commit -q --allow-empty -m "initial commit"
    E project register "$TMP/other" --name otherproj --label Other --desc d --lang Go --status active >/dev/null 2>&1

    local probe_id other_id
    probe_id="$(Q "SELECT id FROM projects WHERE name='probe'")"
    other_id="$(Q "SELECT id FROM projects WHERE name='otherproj'")"
    [[ -n "$probe_id" && -n "$other_id" ]] || return 1

    # Tasks. Titles are distinctive so the list/table greps cannot false-positive.
    E sql "INSERT INTO tasks (id, project_id, title, status, phase) VALUES
             ($FOCAL,  $probe_id, 'Focal goal task',  'underway', 'now'),
             ($SHARED, $probe_id, 'Shared noisy task','ready',    'now'),
             ($SOLO,   $probe_id, 'Solo quiet task',  'ready',    'now'),
             (7004,    $other_id,'Other project task','ready',    'now')" --write >/dev/null 2>&1

    # Two sessions on the SAME focal task — the setup the per-session guarantee
    # needs: both see $SHARED, and a hide in one must not move the other.
    E sql "INSERT INTO sessions (id, session_id, project_id, state, kind_id, active_task_id) VALUES
             ($S1, 'uuid-$S1', $probe_id, 'working',     1, $FOCAL),
             ($S2, 'uuid-$S2', $probe_id, 'needs_input', 1, $FOCAL),
             (9003,'uuid-9003',$other_id,'idle',         1, 7004)" --write >/dev/null 2>&1

    # session_tasks: $SHARED touched by both sessions, $SOLO by $S1 only.
    E sql "INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at) VALUES
             ($S1, $SHARED, 3, '2026-08-07T00:00:00', '2026-08-07T00:00:00'),
             ($S2, $SHARED, 3, '2026-08-07T00:00:00', '2026-08-07T00:00:00'),
             ($S1, $SOLO,   3, '2026-08-07T00:00:00', '2026-08-07T00:00:00')" --write >/dev/null 2>&1

    # Messages, so the sessions are not filtered out as empty by `session list`.
    E sql "INSERT INTO session_messages (session_id, role, content, created_at) VALUES
             ('uuid-$S1','user','hello','2026-08-07T00:00:00'),
             ('uuid-$S2','user','hello','2026-08-07T00:00:00'),
             ('uuid-9003','user','hello','2026-08-07T00:00:00')" --write >/dev/null 2>&1

    [[ "$(Q "SELECT count(*) FROM sessions")" == "3" ]] || return 1
    [[ "$(Q "SELECT count(*) FROM session_tasks")" == "3" ]] || return 1
    return 0
}

# ─── section A: the migration ────────────────────────────────────────────────

# The pre-E-1914 shape, built INLINE with sqlite3 (deterministic, no git
# archaeology): a session_tasks table and nothing else. The change file must add
# session_hidden_tasks beside it without touching it.
OLD_DDL="
CREATE TABLE session_tasks (
    id INTEGER PRIMARY KEY,
    session_id INTEGER NOT NULL,
    task_id INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    do_order INTEGER,
    relation_id INTEGER,
    UNIQUE(session_id, task_id)
);
INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at)
VALUES (42, 500, 3, '2026-01-01T00:00:00', '2026-01-01T00:00:00');
"

has_table() { sqlite3 "$1" "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='$2';"; }

test_migration() {
    section "A. Migration: session_hidden_tasks lands beside session_tasks, idempotently"
    local change="$WT/internal/schema/changes/e-1914-add-session-hidden-tasks.sql"
    if [[ ! -f "$change" ]]; then
        report_fail "change file exists" "$change" "missing"
        return
    fi
    report_pass "change file exists (e-1914-add-session-hidden-tasks.sql)"

    # A1 — the raw SQL against a pre-change DB, applied straight through sqlite3
    # so nothing else (schema.sql on connect) can have created the table first.
    local raw="$TMP/raw"; mkdir -p "$raw"
    printf '%s' "$OLD_DDL" | sqlite3 "$raw/endless.db" >/dev/null
    assert_eq "PRE: pre-change DB has session_tasks and no session_hidden_tasks" \
        "1|0" "$(has_table "$raw/endless.db" session_tasks)|$(has_table "$raw/endless.db" session_hidden_tasks)"

    sqlite3 "$raw/endless.db" < "$change" >/dev/null 2>&1
    assert_eq "change SQL creates session_hidden_tasks on an existing DB" \
        "1" "$(has_table "$raw/endless.db" session_hidden_tasks)"
    assert_eq "change SQL leaves session_tasks and its rows untouched" \
        "1|1" "$(has_table "$raw/endless.db" session_tasks)|$(sqlite3 "$raw/endless.db" 'SELECT count(*) FROM session_tasks;')"
    assert_eq "change SQL creates the task-side index" \
        "1" "$(sqlite3 "$raw/endless.db" "SELECT count(*) FROM sqlite_master WHERE type='index' AND name='idx_session_hidden_tasks_task';")"

    local rerun rc
    rerun=$(sqlite3 "$raw/endless.db" < "$change" 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "re-running the change SQL is a clean no-op (IF NOT EXISTS)"
    else report_fail "re-running the change SQL" "exit 0" "exit=$rc: $rerun"; fi

    # A2 — through the real dispatcher, which also records the _schema_version
    # marker, so a second apply-change is skipped outright.
    local mdir="$TMP/migrate"; mkdir -p "$mdir"
    printf '%s' "$OLD_DDL" | sqlite3 "$mdir/endless.db" >/dev/null
    local applied again
    applied=$("$EGO" --config-dir "$mdir" event apply-change "$change" 2>&1)
    assert_contains "apply-change succeeds on the pre-change DB" '"status":"applied"' "$applied"
    assert_eq "POST: session_hidden_tasks present" \
        "1" "$(has_table "$mdir/endless.db" session_hidden_tasks)"
    assert_eq "POST: the pre-existing session_tasks row survives" \
        "42|500" "$(sqlite3 "$mdir/endless.db" "SELECT session_id||'|'||task_id FROM session_tasks;")"
    again=$("$EGO" --config-dir "$mdir" event apply-change "$change" 2>&1)
    assert_contains "re-apply is a recorded no-op (idempotent)" '"status":"skipped"' "$again"
}

# ─── section B: the per-session guarantee ────────────────────────────────────

test_per_session_hiding() {
    section "B. A hide in one session leaves every other session's view untouched"

    local before_s1 before_s2
    before_s1="$(GO_STATUS --task "$FOCAL" --session "$S1")"
    before_s2="$(GO_STATUS --task "$FOCAL" --session "$S2")"
    assert_contains "PRE: session $S1 sees the shared task" "E-$SHARED" "$before_s1"
    assert_contains "PRE: session $S2 sees the shared task" "E-$SHARED" "$before_s2"

    local out
    out="$(E session hide "ES-$S1" --task "E-$SHARED" 2>&1)"
    assert_contains "hide reports what it hid, for whom" "ES-$S1" "$out"
    assert_eq "hide writes exactly one session_hidden_tasks row" \
        "1" "$(Q "SELECT count(*) FROM session_hidden_tasks")"

    local after_s1 after_s2
    after_s1="$(GO_STATUS --task "$FOCAL" --session "$S1")"
    after_s2="$(GO_STATUS --task "$FOCAL" --session "$S2")"
    assert_not_contains "hidden task is gone from session $S1's view" "E-$SHARED" "$after_s1"
    assert_contains "session $S2's view is UNTOUCHED (the core guarantee)" "E-$SHARED" "$after_s2"
    assert_contains "session $S1 still sees its other work" "E-$SOLO" "$after_s1"

    # Hiding is display-scoped: nothing about the task, and no session_tasks
    # touch fabricated for the sake of holding the state (ED-1545).
    assert_eq "the task's own status is unchanged" \
        "ready" "$(Q "SELECT status FROM tasks WHERE id=$SHARED")"
    assert_eq "no session_tasks row was fabricated" \
        "3" "$(Q "SELECT count(*) FROM session_tasks")"
}

# ─── section C: the footer ───────────────────────────────────────────────────

test_footer() {
    section "C. The '… N hidden' footer — a hidden task never vanishes silently"

    local s1 s2
    s1="$(GO_STATUS --task "$FOCAL" --session "$S1")"
    s2="$(GO_STATUS --task "$FOCAL" --session "$S2")"
    assert_contains "footer states the count and names the flag" "1 hidden (--show-hidden)" "$s1"
    assert_not_contains "no footer for a session with nothing hidden" "hidden (--show-hidden)" "$s2"

    E session hide "ES-$S1" --task "E-$SOLO" >/dev/null 2>&1
    s1="$(GO_STATUS --task "$FOCAL" --session "$S1")"
    assert_contains "footer count tracks a second hide" "2 hidden (--show-hidden)" "$s1"
    E session unhide "ES-$S1" --task "E-$SOLO" >/dev/null 2>&1

    # The footer must survive the monitor's redraw loop. Piped stdout degrades
    # --monitor to a single frame (it is not a terminal), which is exactly the
    # frame the loop repaints.
    local mon
    mon="$(GO_STATUS --task "$FOCAL" --session "$S1" --monitor)"
    assert_contains "the monitor frame carries the footer too" "1 hidden (--show-hidden)" "$mon"
}

# ─── section D: --show-hidden / --only-hidden / unhide ───────────────────────

test_show_only_unhide() {
    section "D. --show-hidden marks, --only-hidden discovers, unhide restores"

    local shown only
    shown="$(GO_STATUS --task "$FOCAL" --session "$S1" --show-hidden)"
    assert_contains "--show-hidden renders the hidden row" "E-$SHARED" "$shown"
    assert_contains "--show-hidden marks it ${HIDDEN_GLYPH}" "${HIDDEN_GLYPH}" "$shown"
    assert_contains "--show-hidden documents ${HIDDEN_GLYPH} in the legend" "${HIDDEN_GLYPH} hidden" "$shown"
    assert_not_contains "--show-hidden prints no omission footer" "(--show-hidden)" "$shown"

    only="$(GO_STATUS --task "$FOCAL" --session "$S1" --only-hidden)"
    assert_contains "--only-hidden finds the hidden task" "E-$SHARED" "$only"
    assert_not_contains "--only-hidden omits the visible work" "E-$SOLO" "$only"

    local out
    out="$(E session unhide "ES-$S1" --task "E-$SHARED" 2>&1)"
    assert_contains "unhide reports the restore" "Unhid" "$out"
    assert_eq "unhide removes the row" "0" "$(Q "SELECT count(*) FROM session_hidden_tasks")"

    local restored
    restored="$(GO_STATUS --task "$FOCAL" --session "$S1")"
    assert_contains "the task is back in the view" "E-$SHARED" "$restored"
    assert_not_contains "and the footer is gone with it" "hidden (--show-hidden)" "$restored"

    local empty
    empty="$(GO_STATUS --task "$FOCAL" --session "$S1" --only-hidden)"
    assert_contains "--only-hidden with nothing hidden says so" "nothing hidden" "$empty"
}

# ─── section E: no-ops and mutually-exclusive flags ──────────────────────────

test_noops_and_exclusions() {
    section "E. No-ops are no-ops; contradictory flags are refused"

    E session hide "ES-$S1" --task "E-$SHARED" >/dev/null 2>&1
    local again rc
    again="$(E session hide "ES-$S1" --task "E-$SHARED" 2>&1)"; rc=$?
    assert_eq "hiding an already-hidden task exits 0" "0" "$rc"
    assert_contains "...and says it was already hidden" "already hidden" "$again"
    assert_eq "...and does not duplicate the row" \
        "1" "$(Q "SELECT count(*) FROM session_hidden_tasks")"

    E session unhide "ES-$S1" --task "E-$SHARED" >/dev/null 2>&1
    again="$(E session unhide "ES-$S1" --task "E-$SHARED" 2>&1)"; rc=$?
    assert_eq "unhiding a not-hidden task exits 0" "0" "$rc"
    assert_contains "...and says it was not hidden" "not hidden" "$again"

    local out
    out="$(E session status --show-hidden --only-hidden 2>&1)"; rc=$?
    assert_eq "--show-hidden with --only-hidden fails" "1" "$rc"
    assert_contains "...with a mutually-exclusive message" "mutually exclusive" "$out"

    # The Go layer refuses it independently — the Python check is a nicety, not
    # the enforcement point.
    GO_STATUS --task "$FOCAL" --session "$S1" --show-hidden --only-hidden >/dev/null 2>&1
    assert_eq "the Go renderer refuses the pair on its own" "2" "$?"

    out="$(E session list --project probe --all-projects 2>&1)"; rc=$?
    assert_eq "--project with --all-projects fails" "1" "$rc"
    assert_contains "...with a mutually-exclusive message" "mutually exclusive" "$out"
}

# ─── section F: the no-flag path is verbatim ─────────────────────────────────

test_session_level_hiding_unchanged() {
    section "F. Without --task, hide/unhide keep their session-level behavior"

    E session hide "$S1" >/dev/null 2>&1
    assert_eq "bare hide sets sessions.hidden" "1" "$(Q "SELECT hidden FROM sessions WHERE id=$S1")"
    assert_eq "...and writes no per-session task hide" \
        "0" "$(Q "SELECT count(*) FROM session_hidden_tasks")"

    # Assert on the session ID, not its task title: both sessions here share the
    # same focal task, so the title stays on screen via the OTHER session's row.
    local listed
    listed="$(list_body "$(E session list --project probe 2>&1)")"
    assert_eq "the hidden session drops out of session list" \
        "0" "$(printf '%s\n' "$listed" | grep -c "^$S1 ")"
    assert_eq "...and the other session stays" \
        "1" "$(printf '%s\n' "$listed" | grep -c "^$S2 ")"

    E session unhide "$S1" >/dev/null 2>&1
    assert_eq "bare unhide clears sessions.hidden" "0" "$(Q "SELECT hidden FROM sessions WHERE id=$S1")"
}

# ─── section G: --json carries the hidden state ──────────────────────────────

test_json() {
    section "G. session status --json emits every row with its hidden state"

    E session hide "ES-$S1" --task "E-$SHARED" >/dev/null 2>&1
    local out
    out="$(GO_STATUS --task "$FOCAL" --session "$S1" --json)"

    assert_contains "--json names the viewer the hidden flags belong to" \
        "\"viewer_session\": $S1" "$out"
    local hidden_flag visible_flag
    hidden_flag="$(printf '%s' "$out" | python3 -c '
import json,sys
frame = json.load(sys.stdin)
rows = {r["id"]: r for r in frame["rows"]}
print(rows.get('"$SHARED"', {}).get("hidden"))
' 2>&1)"
    visible_flag="$(printf '%s' "$out" | python3 -c '
import json,sys
frame = json.load(sys.stdin)
rows = {r["id"]: r for r in frame["rows"]}
print(rows.get('"$SOLO"', {}).get("hidden"))
' 2>&1)"
    assert_eq "--json marks the hidden row hidden" "True" "$hidden_flag"
    assert_eq "--json emits the visible row unfiltered, unhidden" "False" "$visible_flag"

    E session unhide "ES-$S1" --task "E-$SHARED" >/dev/null 2>&1
}

# ─── section H: session list rendering ───────────────────────────────────────

# The data rows of a `session list` render: between the ─── separator and the
# blank line before the legend.
list_body() {
    printf '%s\n' "$1" | awk '
        /^─/ {inbody=1; next}
        inbody && NF==0 {exit}
        inbody {print}
    '
}

test_session_list_render() {
    section "H. session list: task column, glyphs, alignment, project scoping"

    local out
    out="$(E session list --project probe 2>&1)"

    assert_not_contains "the Summary column is gone" "Summary" "$out"
    assert_contains "the Task column is present" "Task" "$out"
    assert_contains "...carrying the active task id" "E-$FOCAL" "$out"
    assert_contains "...and that task's title" "Focal goal task" "$out"
    assert_contains "state renders as a glyph" "$WORKING_GLYPH" "$out"
    assert_not_contains "...not the raw state word" "needs_input" "$out"
    assert_contains "a legend explains the glyphs" "$WORKING_GLYPH working" "$out"

    # Column alignment: the working row and the needs_input row must place the
    # task id at the SAME offset. Printing the raw state word (11 chars vs 4)
    # is exactly what used to make this fail.
    local body offsets
    body="$(list_body "$out")"
    offsets="$(printf '%s\n' "$body" | awk -v FS="" '{
        for (i = 1; i <= NF; i++) if ($i == "E" && $(i+1) == "-") { print i; break }
    }' | sort -u | wc -l | tr -d ' ')"
    assert_eq "a needs_input row does not skew the Task column" "1" "$offsets"
    assert_eq "both sessions rendered" "2" "$(printf '%s\n' "$body" | grep -c .)"
    assert_contains "the needs-input glyph is used" "$NEEDS_INPUT_GLYPH" "$body"

    # Single-project output names the project in the header and omits the column.
    assert_not_contains "single-project output has no Project column" "Project" "$out"
    assert_contains "...naming the project in the header instead" "project: probe" "$out"

    # Multi-project output gets the column back.
    local all
    all="$(E session list --all-projects 2>&1)"
    assert_contains "--all-projects spans both projects" "Other project task" "$all"
    assert_contains "...and restores the Project column" "Project" "$all"
    assert_not_contains "...without also claiming a single project" "project: probe" "$all"

    # --json gains the task id and keeps the raw state word.
    local js
    js="$(E session list --project probe --json 2>&1)"
    assert_contains "--json carries the new task_id field" "\"task_id\": $FOCAL" "$js"
    assert_contains "--json keeps the raw state (no glyph substitution)" '"state": "needs_input"' "$js"
}

# ─── section I: unit + regression suites ─────────────────────────────────────

test_regression() {
    section "I. Go + Python suites"
    local out rc

    out=$(cd "$WT" && go test ./internal/sessionstatuscmd/ ./internal/monitor/ ./internal/schema/ 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go test sessionstatuscmd + monitor + schema passes"
    else report_fail "go test sessionstatuscmd + monitor + schema" "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -20)"; fi

    out=$(cd "$WT" && uv run pytest tests/test_session_hide_tasks.py tests/test_session_list_render.py -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "pytest session hide-tasks + list-render suites pass"
    else report_fail "pytest session hide-tasks + list-render" "exit 0" "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -20)"; fi
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    WT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -n "${WT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    EGO="${WT}/bin/endless-go"

    command -v go >/dev/null      || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }
    command -v uv >/dev/null      || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    command -v sqlite3 >/dev/null || { printf 'ERROR: sqlite3 not on PATH\n' >&2; exit 2; }
    command -v python3 >/dev/null || { printf 'ERROR: python3 not on PATH\n' >&2; exit 2; }
    [[ -x "${EGO}" ]] || { printf 'ERROR: %s missing — run `just build`\n' "${EGO}" >&2; exit 2; }

    printf '%sE-1914 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"

    if ! setup_fixture; then
        printf 'ERROR: fixture setup failed (isolated DB seeding)\n' >&2
        [[ -n "$TMP" ]] && rm -rf "$TMP"
        exit 2
    fi

    test_migration
    test_per_session_hiding
    test_footer
    test_show_only_unhide
    test_noops_and_exclusions
    test_session_level_hiding_unchanged
    test_json
    test_session_list_render
    test_regression

    [[ -n "$TMP" ]] && rm -rf "$TMP"
    summary
}

main "$@"
