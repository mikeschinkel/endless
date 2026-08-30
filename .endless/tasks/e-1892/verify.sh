#!/usr/bin/env bash
#
# E-1892 verification — `session monitor` re-resolves its focal task until one
# appears, instead of resolving once at startup and showing the claim/bind hint
# forever.
#
# Run from anywhere inside the worktree:
#   esu
#   ./tests/tasks/e-1892-verify.sh
#
# WHAT LANDED
#   sessionstatuscmd.Run used to resolve focal/parentSession/emittingSession ONCE
#   and hand the three values to monitorLoop, which never re-resolved them. A
#   monitor started before its session was registered therefore rendered the
#   no-task hint for its entire life. E-1851's spawned layout launches the
#   monitor pane milliseconds BEFORE Claude's hook writes the session row, so
#   that was the common case, not an edge case.
#
#   Now Run passes an anchor FUNCTION and monitorLoop's anchorTracker calls it
#   every tick while focal == 0, freezing on the first hit:
#     - all three ids re-anchor together (they share one race: @endless_spawned_by
#       is written immediately before spawn-launch's syscall.Exec, and
#       emittingSession is consulted only while focal == 0, so a stale 0 would
#       leave the E-1802 no-goal view permanently empty)
#     - freezing on focal != 0 preserves E-1698's anchor-once contract: the view
#       stays pinned to THIS window's task as other sessions come and go
#     - a resolver error while still unanchored is non-fatal (retry next tick),
#       unlike a render error, which still exits
#
# SUPERSEDED PLAN DETAIL (recorded so it is not relitigated)
#   The plan called for a test-intended `--process` flag, on the premise that
#   sessionstatuscmd.Run pins the main DB UNCONDITIONALLY, making pane→session
#   resolution untestable against a seeded DB. That premise was already stale
#   when the plan was written: c186df7d (E-698, on main) changed the pin to
#   `if !monitor.HasExplicitDBContext() { PinMainDB() }`, the E-1429/E-1700
#   contract that an explicit --config-dir beats the env-driven main pin. Layer B
#   below therefore drives the REAL resolution path with --config-dir and the
#   pane's own $TMUX_PANE — no new production flag, and nothing on main reverted.
#   The plan's companion assertion (that --config-dir must NOT redirect the pin)
#   was dropped for the same reason: it now contradicts main. The routing
#   contract that DOES hold is pinned in layer A via monitor's own gate test.
#
# ISOLATION
#   Layer B runs against a PRIVATE tmux server (`tmux -L <socket>`), killed on
#   exit — your real tmux server, windows, and focus are untouched. It seeds a
#   THROWAWAY --config-dir DB; the real ledger is never read or written.
#
# Layers:
#   A. FAIL-FAST unit tests — the anchor lifecycle (re-resolve, freeze, whole-set,
#      non-fatal error, resolve→render), plus the render/sizing contracts and the
#      DB-routing gate this must not regress. If these are broken, stop before
#      spending time on tmux servers.
#   B. Recovery E2E — a monitor started with NO session row shows the hint, and
#      recovers to the task's rows (at the exact E-1851 pane fit) once the session
#      row is INSERTed mid-flight.
#   C. Project-wide regression — `go test ./...` and the full Python suite.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 setup error.

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

BIN=""        # the worktree's endless-go (rebuilt in layer A)
TMPDIR_B=""   # layer B scratch (throwaway --config-dir DB)
SOCK_B=""     # layer B private tmux socket

# Window geometry for the E2E layer. Tall enough that the monitor's
# 80%-of-window cap never binds on the fitted frame.
WIN_COLS=200
WIN_ROWS=50

# The seeded focal task and its children. A focal row plus four surfaced child
# rows (E-1691) under one legend line = a 6-line frame, so the fitted pane is 7.
FOCAL_TASK=100

# monitorPaneEmptyHeight — the height the monitor holds while it has NO task
# rows, rather than exact-fitting the 1-line hint into a 2-row sliver (E-1851).
# Recovery is therefore a SHRINK here (8 → 7), not a grow: the assertions below
# pin the exact fit either way, so which direction it moves is incidental.
EMPTY_HEIGHT=8

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'
    BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

# ─── output ─────────────────────────────────────────────────────────────────

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
note()    { printf '  %s%s%s\n' "${DIM}" "$1" "${RESET}"; }

report_pass() {
    PASS_COUNT=$((PASS_COUNT + 1))
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
}

report_fail() {
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sactual:  %s %s\n' "${DIM}" "${RESET}" "$3"
}

summary() {
    section "Summary"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s, 0 failed\n\n' "${GREEN}" "${PASS_COUNT}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" \
        "${RED}${BOLD}" "${RESET}"
    local t
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'
    return 1
}

# ─── assertions ─────────────────────────────────────────────────────────────

# assert_succeeds DESC CMD [ARGS...]
assert_succeeds() {
    local desc="$1"; shift
    local output rc
    output=$("$@" 2>&1); rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return; fi
    report_fail "${desc}" "exit == 0" "exit=${rc} | $(printf '%s' "${output}" | tail -20)"
}

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"
    else report_fail "$1" "$2" "$3"; fi
}

# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "contains: $2" "$3"; fi
}

# assert_not_contains DESC NEEDLE HAYSTACK
assert_not_contains() {
    if [[ "$3" != *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "does NOT contain: $2" "$3"; fi
}

# ─── setup / teardown ───────────────────────────────────────────────────────

cleanup() {
    [[ -n "${SOCK_B}" ]] && tmux -L "${SOCK_B}" kill-server 2>/dev/null
    [[ -n "${TMPDIR_B}" ]] && rm -rf "${TMPDIR_B}"
    return 0
}

# ─── layer A — unit tests (FAIL-FAST) ───────────────────────────────────────

run_unit_layer() {
    section "A — build + unit contracts (FAIL-FAST)"
    note "the anchor lifecycle this task changed, plus the render/sizing and"
    note "DB-routing contracts it must not regress"

    local before="${FAIL_COUNT}"

    assert_succeeds "just go (builds bin/endless-go)" just go

    # The fix itself.
    assert_succeeds "anchor: re-resolves until a focal task appears" \
        go test ./internal/sessionstatuscmd/ -count=1 \
            -run 'TestAnchorTrackerReresolvesUntilFocalAppears'
    assert_succeeds "anchor: freezes on the first hit (E-1698 anchor-once)" \
        go test ./internal/sessionstatuscmd/ -count=1 \
            -run 'TestAnchorTrackerFreezesAfterFirstHit'
    assert_succeeds "anchor: focal/parent/emitting re-anchor on the SAME tick" \
        go test ./internal/sessionstatuscmd/ -count=1 \
            -run 'TestAnchorTrackerUpdatesWholeSetOnTheSameTick|TestAnchorTrackerKeepsResolvingEmittingSessionWhileUnanchored'
    assert_succeeds "anchor: a resolver error while unanchored is non-fatal" \
        go test ./internal/sessionstatuscmd/ -count=1 \
            -run 'TestAnchorTrackerResolverErrorIsNonFatal|TestAnchorTrackerStartsWithTheClaimBindHint'
    assert_succeeds "monitorFrame: renders the task's rows once focal resolves" \
        go test ./internal/sessionstatuscmd/ -count=1 \
            -run 'TestMonitorFrameRendersRowsOnceFocalAppears'

    # Contracts this refactor routes around and must leave intact.
    assert_succeeds "renderer unchanged (hints, no-goal rows, legend, columns)" \
        go test ./internal/sessionstatuscmd/ -count=1 \
            -run 'TestRenderEmptyFocal|TestRenderNoGoalSurfacesRows|TestBuildLegend|TestRenderColumnsAndTruncation'
    assert_succeeds "E-1851 pane sizing rule still holds (floor, cap, frame lines)" \
        go test ./internal/sessionstatuscmd/ -count=1 \
            -run 'TestFrameLines|TestPaneHeightForFrame|TestFitPaneToFrame'
    assert_succeeds "DB routing gate unchanged: cwd self-detect pins main, --config-dir wins" \
        go test ./internal/monitor/ -count=1 \
            -run 'TestSelfDetectVsExplicit_MainPinRouting'

    if [[ "${FAIL_COUNT}" -ne "${before}" ]]; then
        printf '\n  %sFail-fast: the unit contracts are broken; skipping the tmux E2E layer.%s\n' \
            "${RED}${BOLD}" "${RESET}"
        return 1
    fi
    return 0
}

# ─── layer B — recovery E2E ─────────────────────────────────────────────────

# b_capture PANE — the pane's visible text.
b_capture() {
    tmux -L "${SOCK_B}" capture-pane -p -t "$1" 2>/dev/null
}

# b_height PANE — the pane's current row count.
b_height() {
    tmux -L "${SOCK_B}" display-message -p -t "$1" '#{pane_height}' 2>/dev/null
}

run_recovery_layer() {
    section "B — recovery E2E: a monitor that starts before its session exists"
    note "seeds a THROWAWAY --config-dir DB (real ledger untouched) and runs the"
    note "real monitor in a \`tmux -L ${SOCK_B}\` pane"

    TMPDIR_B=$(mktemp -d) || { report_fail "layer B scratch dir" "mktemp -d ok" "failed"; return; }

    # Materialize + migrate the throwaway DB via a headless one-shot, then seed a
    # project and the focal task with four children — but deliberately NO session
    # row: that is the state a spawned monitor starts in.
    "${BIN}" --config-dir "${TMPDIR_B}" session-status --task 999999 >/dev/null 2>&1
    if [[ ! -f "${TMPDIR_B}/endless.db" ]]; then
        report_fail "throwaway DB is created" "endless.db in --config-dir" "missing"
        return
    fi
    report_pass "throwaway DB is created"

    sqlite3 "${TMPDIR_B}/endless.db" <<SQL 2>/dev/null
INSERT INTO projects (id,name,path) VALUES (1,'e1892probe','${TMPDIR_B}/proj');
INSERT INTO tasks (id,project_id,parent_id,title,phase,status,type_id) VALUES
 (${FOCAL_TASK},1,NULL,'Focal probe task','now','underway',1),
 (101,1,${FOCAL_TASK},'Child one','now','ready',1),
 (102,1,${FOCAL_TASK},'Child two','now','ready',1),
 (103,1,${FOCAL_TASK},'Child three','now','ready',1),
 (104,1,${FOCAL_TASK},'Child four','now','ready',1);
SQL

    local session_rows
    session_rows=$(sqlite3 "${TMPDIR_B}/endless.db" 'SELECT COUNT(*) FROM sessions;' 2>/dev/null)
    assert_eq "seeded DB has NO session row (the pre-registration state)" "0" "${session_rows}"

    # The one-shot render is the ground truth the recovered pane must match.
    local frame_lines
    frame_lines=$("${BIN}" --config-dir "${TMPDIR_B}" session-status --task "${FOCAL_TASK}" --cols 100 \
                    2>/dev/null | grep -c .)
    assert_eq "seeded frame is 1 legend + 5 task rows" "6" "${frame_lines}"

    if ! tmux -L "${SOCK_B}" new-session -d -s recover -x "${WIN_COLS}" -y "${WIN_ROWS}" 2>/dev/null; then
        report_fail "private tmux server starts" "tmux -L new-session exit 0" "failed"
        return
    fi
    report_pass "private tmux server starts"

    # Launch the monitor with NO --task/--session: it must resolve its focal task
    # from its own $TMUX_PANE through the real ResolveSessionStatusFocal path,
    # against the seeded DB (--config-dir beats the main pin, E-1429/E-1700).
    local p0 mon
    p0=$(tmux -L "${SOCK_B}" display-message -p -t recover '#{pane_id}' 2>/dev/null)
    mon=$(tmux -L "${SOCK_B}" split-window -v -t "${p0}" -P -F '#{pane_id}' -- \
            "${BIN}" --config-dir "${TMPDIR_B}" session-status --monitor 2>/dev/null)
    if [[ -z "${mon}" ]]; then
        report_fail "monitor pane starts" "a pane id" "split-window failed"
        return
    fi
    report_pass "monitor pane starts"

    # ── before the session row exists ────────────────────────────────────────
    # Give it a couple of ticks to paint and self-fit.
    local tries=0 h=""
    while (( tries < 40 )); do
        h=$(b_height "${mon}")
        [[ "${h}" == "${EMPTY_HEIGHT}" ]] && break
        sleep 0.25; tries=$((tries + 1))
    done

    local pre
    pre=$(b_capture "${mon}")
    assert_contains "unresolved monitor shows the claim/bind hint" "claim or bind" "${pre}"
    assert_not_contains "unresolved monitor shows NO task rows" "E-${FOCAL_TASK}" "${pre}"
    assert_eq "hint-only frame holds monitorPaneEmptyHeight (E-1851)" \
        "${EMPTY_HEIGHT}" "$(b_height "${mon}")"

    # ── register the session mid-flight ──────────────────────────────────────
    # Exactly what Claude's SessionStart hook does moments after the pane exists:
    # a live session row whose `process` is this pane, claiming the focal task.
    sqlite3 "${TMPDIR_B}/endless.db" <<SQL 2>/dev/null
INSERT INTO sessions (id,session_id,project_id,platform,state,active_task_id,process,last_activity)
VALUES (1,'e1892-probe-session',1,'claude','working',${FOCAL_TASK},'${mon}',
        strftime('%Y-%m-%dT%H:%M:%S','now'));
SQL

    session_rows=$(sqlite3 "${TMPDIR_B}/endless.db" \
        "SELECT COUNT(*) FROM sessions WHERE process='${mon}' AND active_task_id=${FOCAL_TASK};" 2>/dev/null)
    assert_eq "session row registered mid-flight for this pane" "1" "${session_rows}"

    # ── the fix: the monitor must pick it up on a later tick ─────────────────
    # monitorInterval is 2s; allow well over the plan's ~3 ticks before failing.
    local post=""
    local want_h=$(( frame_lines + 1 ))
    tries=0
    while (( tries < 60 )); do
        post=$(b_capture "${mon}")
        [[ "${post}" == *"E-${FOCAL_TASK}"* ]] && break
        sleep 0.25; tries=$((tries + 1))
    done

    assert_contains "monitor RECOVERS: focal task rows appear without a restart" \
        "E-${FOCAL_TASK}" "${post}"
    assert_contains "recovered view lists the surfaced child rows too" "E-104" "${post}"
    assert_not_contains "recovered view drops the claim/bind hint" "claim or bind" "${post}"

    # Composes with E-1851: the pane regrows to the exact fit for the new frame.
    tries=0; h=""
    while (( tries < 40 )); do
        h=$(b_height "${mon}")
        [[ "${h}" == "${want_h}" ]] && break
        sleep 0.25; tries=$((tries + 1))
    done
    assert_eq "recovered pane refits to the 6-line frame (+1 slack row)" "${want_h}" "${h}"
}

# ─── layer C — project-wide regression ──────────────────────────────────────

run_regression_layer() {
    section "C — project-wide regression"
    note "the full Go and Python suites; nothing in E-1892 may regress them"
    assert_succeeds "go test ./..." go test ./...
    assert_succeeds "just test (full Python suite)" just test
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    WT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${WT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    cd "${WT}" || exit 2

    local tool
    for tool in just go uv sqlite3 tmux; do
        command -v "${tool}" >/dev/null 2>&1 || {
            printf 'ERROR: %s not on PATH\n' "${tool}" >&2; exit 2; }
    done

    BIN="${WT}/bin/endless-go"
    SOCK_B="e1892b$$"
    trap cleanup EXIT

    printf '%sE-1892 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"
    printf '  tmux:     private server (%s), killed on exit\n' "${SOCK_B}"
    printf '  db:       throwaway --config-dir; real ledger untouched\n'

    if run_unit_layer; then
        run_recovery_layer
    fi
    run_regression_layer

    summary
}

main "$@"
