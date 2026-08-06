#!/usr/bin/env bash
#
# E-1851 verification — `endless task spawn` builds the canonical 3-pane tmux
# layout, and `endless session monitor` sizes its own pane to the frame it
# renders.
#
# Run from anywhere inside the worktree:
#   esu
#   ./tests/tasks/e-1851-verify.sh
#
# WHAT LANDED
#   spawn-window, after `tmux new-window` returns, splits the window into:
#     left        50% width, full height — the spawned Claude session (pane 0,
#                 exec-replaced by spawn-launch; it ends FOCUSED). cwd = the
#                 WORKTREE: this pane is the branch's work.
#     top-right   `endless session monitor`
#     bottom-right a bare interactive $SHELL for ad-hoc endless commands
#   Both right panes start in the PROJECT dir, not the worktree. The Python CLI
#   routes its DB from cwd, so from inside a self_dev worktree every ad-hoc
#   `endless` command in the shell pane would need an explicit --db main. The
#   monitor pane follows the same rule for consistency only — session-status
#   pins the main DB regardless of cwd (E-698, c186df7d).
#   The monitor pane is created with -b (ABOVE the shell pane) rather than the
#   shell being split off the monitor, because `session monitor` shrinks its own
#   pane on first paint — splitting a pane that is concurrently resizing itself
#   would hand the shell whatever few rows survived the race.
#   Sizing is the monitor's own job (sessionstatuscmd.fitPaneToFrame): it knows
#   its row count, spawn does not, and the count changes as the ledger does.
#
# ISOLATION
#   The tmux E2E layers run against PRIVATE tmux servers (`tmux -L <socket>`),
#   killed on exit — your real tmux server, windows, and focus are untouched.
#   Those servers are started with `-f /dev/null` (no ~/.tmux.conf, so no user
#   tmux hook fires against them) and under a throwaway XDG_CONFIG_HOME, so no
#   endless-go process running inside them can reach the real ledger. That
#   second guard is the load-bearing one: inside a private server, $TMUX names a
#   3-pane fake, and any code deciding liveness from `tmux list-panes -a` while
#   pointed at the real DB would consider every real session dead (E-1898).
#   Layer C seeds a THROWAWAY --config-dir DB; the real ledger is never written.
#   Layer B drives `endless-go spawn-window` directly with a STUB claude binary,
#   so no Claude session is ever launched.
#
# Layers:
#   A. FAIL-FAST unit tests — the tmux argv builders (spawnlaunchcmd, monitor)
#      and the pure pane-sizing rule (sessionstatuscmd). If the contract these
#      pin is broken, stop before spending time on tmux servers.
#   B. Layout E2E — a real spawn-window against a private tmux server yields
#      exactly 3 panes in the expected geometry, with claude focused.
#   C. Self-sizing E2E — the monitor pane resizes to (frame lines + 1) against a
#      seeded DB, and floors at 2 rows for an empty frame.
#   D. Project-wide regression — `go test ./...` and the full Python suite.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 any failure,
# 2 setup error.

set -u

# ─── globals ────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

BIN=""        # the worktree's endless-go (rebuilt in layer A)
TMPDIR_B=""   # layer B scratch (stub claude, handoff, driver script)
TMPDIR_C=""   # layer C scratch (throwaway --config-dir DB)
SOCK_B=""     # layer B private tmux socket
SOCK_C=""     # layer C private tmux socket

# Window geometry the E2E layers create. Wide enough that a 50/50 split is
# unambiguous, tall enough that the monitor's 80%-of-window cap never binds.
WIN_COLS=200
WIN_ROWS=50

# The layout is anchored by resolving the window name to its active pane's id,
# so the name here carries the `[E-NNNN]` brackets every real spawn window has
# (_spawn_window_name). `[...]` are fnmatch metacharacters; tmux matches window
# names exactly before it tries fnmatch, and this pins that we depend on it.
WIN_NAME='e1851probe_build-layout[E-1851]'

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
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
    PASS_COUNT=$((PASS_COUNT + 1))
}

report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
}

summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
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

# assert_between DESC LO HI ACTUAL
assert_between() {
    if [[ "$4" =~ ^[0-9]+$ ]] && (( $4 >= $2 && $4 <= $3 )); then report_pass "$1"
    else report_fail "$1" "between $2 and $3" "$4"; fi
}

# ─── setup / teardown ───────────────────────────────────────────────────────

# kill_server NAME — kill a private tmux server AND remove its socket file.
#
# `tmux kill-server` leaves the socket behind, so a suite that runs often
# accumulates dead sockets in the SHARED per-uid tmux socket directory
# (`/tmp/tmux-<uid>/`). That directory is the one anything enumerating tmux
# servers has to walk, so the litter is not merely untidy — it is noise in the
# exact place a future guard will look.
#
# Resolve the path from tmux while the server is still up; fall back to the
# documented location if the query fails (server already gone, or never
# started).
kill_server() {
    local name="$1" path
    [[ -z "${name}" ]] && return 0
    path=$(tmux -L "${name}" display-message -p '#{socket_path}' 2>/dev/null)
    tmux -L "${name}" kill-server 2>/dev/null
    [[ -z "${path}" ]] && path="${TMUX_TMPDIR:-/tmp}/tmux-$(id -u)/${name}"
    rm -f "${path}" 2>/dev/null
    return 0
}

cleanup() {
    kill_server "${SOCK_B}"
    kill_server "${SOCK_C}"
    [[ -n "${TMPDIR_B}" ]] && rm -rf "${TMPDIR_B}"
    [[ -n "${TMPDIR_C}" ]] && rm -rf "${TMPDIR_C}"
    return 0
}

# ─── layer A — unit tests (FAIL-FAST) ───────────────────────────────────────

run_unit_layer() {
    section "A — build + unit contracts (FAIL-FAST)"
    note "the tmux argv builders and the pure pane-sizing rule; the tmux E2E"
    note "layers below drive the binary these produce"

    local before="${FAIL_COUNT}"

    assert_succeeds "just go (builds bin/endless-go)" just go

    assert_succeeds "spawnlaunchcmd: split/select/pane-id argv + monitor command" \
        go test ./internal/spawnlaunchcmd/ -count=1 \
            -run 'TestSplitWindowArgs|TestSelectPaneArgs|TestPanePaneIDArgs|TestMonitorCommand'
    assert_succeeds "spawnlaunchcmd: observation panes resolve to the project dir" \
        go test ./internal/spawnlaunchcmd/ -count=1 -run 'TestProjectDirFor'
    assert_succeeds "spawnlaunchcmd: pre-existing new-window/option argv unchanged" \
        go test ./internal/spawnlaunchcmd/ -count=1 \
            -run 'TestNewWindowArgs|TestWindowOptionCommands'
    assert_succeeds "monitor: resize-pane + window-height argv, empty-pane guards" \
        go test ./internal/monitor/ -count=1 \
            -run 'TestPaneWindowHeight|TestResizePaneHeight'
    assert_succeeds "sessionstatuscmd: frame line count + sizing rule (floor, cap)" \
        go test ./internal/sessionstatuscmd/ -count=1 \
            -run 'TestFrameLines|TestPaneHeightForFrame|TestFitPaneToFrame'

    if [[ "${FAIL_COUNT}" -ne "${before}" ]]; then
        printf '\n  %sFail-fast: the unit contracts are broken; skipping the tmux E2E layers.%s\n' \
            "${RED}" "${RESET}"
        summary
        exit 1
    fi
}

# ─── layer B — 3-pane layout E2E ────────────────────────────────────────────

# pane_field LEFT_PRED TOP_PRED FIELD — read one field of the pane matching the
# geometry predicate. Panes are addressed by GEOMETRY, never by index: pane
# indexes depend on the user's pane-base-index and shift as panes are added.
b_panes() {
    tmux -L "${SOCK_B}" list-panes -t "${WIN_NAME}" \
        -F '#{pane_left}|#{pane_top}|#{pane_width}|#{pane_height}|#{?pane_active,active,}|#{pane_start_command}|#{pane_current_path}' \
        2>/dev/null
}

run_layout_layer() {
    section "B — layout E2E: spawn-window builds 3 panes (private tmux server)"
    note "drives \`endless-go spawn-window\` with a STUB claude binary inside a"
    note "\`tmux -L ${SOCK_B}\` server; no Claude runs, your tmux is untouched"

    TMPDIR_B=$(mktemp -d) || { report_fail "layer B scratch dir" "mktemp -d ok" "failed"; return; }

    printf '#!/bin/sh\nexec /bin/cat\n' > "${TMPDIR_B}/claude-stub"
    chmod +x "${TMPDIR_B}/claude-stub"
    printf 'stub handoff\n' > "${TMPDIR_B}/handoff.txt"

    # Run spawn-window as the private session's own pane command rather than via
    # send-keys: send-keys races the pane shell's startup, a pane command cannot.
    cat > "${TMPDIR_B}/drive.sh" <<EOF
#!/bin/sh
"${BIN}" spawn-window \\
  --claude-bin "${TMPDIR_B}/claude-stub" \\
  --handoff-file "${TMPDIR_B}/handoff.txt" \\
  --permission-mode auto \\
  --task-id 1851 --project-id 1 --spawned-by 0 \\
  --window-name "${WIN_NAME}" --cwd "${WT}" > "${TMPDIR_B}/spawn.log" 2>&1
echo "rc=\$?" >> "${TMPDIR_B}/spawn.log"
EOF
    chmod +x "${TMPDIR_B}/drive.sh"

    # Isolation, belt AND braces (see the ISOLATION note in the header):
    #   -f /dev/null      the private server must not read ~/.tmux.conf, so no
    #                     user tmux hook (e.g. session-created -> `endless tmux
    #                     init`) can fire against it.
    #   XDG_CONFIG_HOME   every descendant of this server -- spawn-window, and
    #                     the `endless session monitor` it launches in a pane --
    #                     inherits a THROWAWAY config dir. Those processes run
    #                     with $TMUX pointing at a 3-pane fake server; anything
    #                     that decided liveness from `tmux list-panes -a` while
    #                     pointed at the real ledger could reap every live
    #                     session in it (E-1898). They must not be able to reach
    #                     the real ledger at all.
    mkdir -p "${TMPDIR_B}/config"
    if ! XDG_CONFIG_HOME="${TMPDIR_B}/config" \
         tmux -L "${SOCK_B}" -f /dev/null \
            new-session -d -s drv -x "${WIN_COLS}" -y "${WIN_ROWS}" \
            "${TMPDIR_B}/drive.sh" 2>/dev/null; then
        report_fail "private tmux server starts" "tmux -L new-session exit 0" "failed"
        return
    fi
    report_pass "private tmux server starts"

    # Give spawn-window time to create the window and both splits, and the
    # monitor pane's CLI time to come up.
    local tries=0
    while (( tries < 40 )); do
        [[ "$(b_panes | wc -l | tr -d ' ')" == "3" ]] && break
        sleep 0.25; tries=$((tries + 1))
    done

    local log; log=$(cat "${TMPDIR_B}/spawn.log" 2>/dev/null)
    assert_contains "spawn-window exits 0" "rc=0" "${log}"

    local panes; panes=$(b_panes)
    assert_eq "window has exactly 3 panes" "3" "$(printf '%s\n' "${panes}" | grep -c .)"

    if [[ "$(printf '%s\n' "${panes}" | grep -c .)" != "3" ]]; then
        note "pane dump: $(printf '%s' "${panes}" | tr '\n' ' ')"
        return
    fi

    local left top_right bottom_right
    left=$(printf '%s\n' "${panes}" | awk -F'|' '$1 == 0')
    top_right=$(printf '%s\n' "${panes}" | awk -F'|' '$1 > 0 && $2 == 0')
    bottom_right=$(printf '%s\n' "${panes}" | awk -F'|' '$1 > 0 && $2 > 0')

    # ── left pane: claude, half width, full height, focused ──
    assert_eq "exactly one pane in the left column" "1" \
        "$(printf '%s\n' "${left}" | grep -c .)"
    assert_between "left pane is ~50% of the window width" \
        $((WIN_COLS / 2 - 2)) $((WIN_COLS / 2 + 2)) \
        "$(printf '%s' "${left}" | cut -d'|' -f3)"
    assert_eq "left pane spans the full window height" "${WIN_ROWS}" \
        "$(printf '%s' "${left}" | cut -d'|' -f4)"
    assert_eq "left pane ends FOCUSED (select-pane returned to claude)" "active" \
        "$(printf '%s' "${left}" | cut -d'|' -f5)"
    assert_contains "left pane runs the claude launcher (spawn-launch --spec)" \
        "spawn-launch --spec" "$(printf '%s' "${left}" | cut -d'|' -f6)"

    # ── top-right pane: session monitor ──
    assert_eq "exactly one pane at the top of the right column" "1" \
        "$(printf '%s\n' "${top_right}" | grep -c .)"
    assert_contains "top-right pane runs \`session monitor\`" "session monitor" \
        "$(printf '%s' "${top_right}" | cut -d'|' -f6)"

    # ── bottom-right pane: bare shell ──
    assert_eq "exactly one pane below it in the right column" "1" \
        "$(printf '%s\n' "${bottom_right}" | grep -c .)"
    assert_eq "bottom-right pane is a BARE shell (no start command)" "" \
        "$(printf '%s' "${bottom_right}" | cut -d'|' -f6)"
    assert_eq "the two right-column panes share one column (same left offset)" \
        "$(printf '%s' "${top_right}" | cut -d'|' -f1)" \
        "$(printf '%s' "${bottom_right}" | cut -d'|' -f1)"

    # ── pane cwd: claude gets the worktree, the observation panes get the
    # project dir. The Python CLI routes its DB from cwd, so from inside a
    # self_dev worktree every ad-hoc command in the shell pane would otherwise
    # need an explicit --db main. The monitor pane follows for consistency only;
    # session-status pins main regardless of cwd (E-698, c186df7d).
    local project_dir
    project_dir=$(cd "$(dirname "$(git rev-parse --git-common-dir)")" && pwd)

    # A pane's #{pane_current_path} is not settled the instant the pane exists:
    # tmux reports the inherited cwd until the pane's shell has actually chdir'd
    # into the -c directory. Poll for steady state before asserting, with a bound
    # so a genuine regression still fails on the real value rather than hanging.
    local settle=0
    while (( settle < 40 )); do
        [[ "$(b_panes | awk -F"|" "\$1 > 0 && \$2 > 0" | cut -d"|" -f7)" == "${project_dir}" ]] && break
        sleep 0.25; settle=$((settle + 1))
    done
    panes=$(b_panes)
    left=$(printf '%s\n' "${panes}" | awk -F'|' '$1 == 0')
    top_right=$(printf '%s\n' "${panes}" | awk -F'|' '$1 > 0 && $2 == 0')
    bottom_right=$(printf '%s\n' "${panes}" | awk -F'|' '$1 > 0 && $2 > 0')
    assert_eq "claude pane keeps the WORKTREE cwd (it is the branch's work)" \
        "${WT}" "$(printf '%s' "${left}" | cut -d'|' -f7)"
    assert_eq "monitor pane starts in the PROJECT dir (consistent with the shell)" \
        "${project_dir}" "$(printf '%s' "${top_right}" | cut -d'|' -f7)"
    assert_eq "shell pane starts in the PROJECT dir (no --db main needed)" \
        "${project_dir}" "$(printf '%s' "${bottom_right}" | cut -d'|' -f7)"

    # The right column's two panes plus their separator row fill the window.
    local th bh
    th=$(printf '%s' "${top_right}" | cut -d'|' -f4)
    bh=$(printf '%s' "${bottom_right}" | cut -d'|' -f4)
    assert_eq "right column partitions the full window height" "${WIN_ROWS}" \
        "$(( th + bh + 1 ))"
}

# ─── layer C — monitor self-sizing E2E ──────────────────────────────────────

# c_monitor_height FOCAL — split a fresh private window, run the monitor against
# the seeded throwaway DB in the new pane, and echo "<monitor_h> <sibling_h>".
c_monitor_height() {
    # Separate `local` statements: a single one expands every word before any
    # assignment happens, so `win` could not reference `focal` there.
    local focal="$1"
    local win="w${focal}" p0 mon tries=0 h="" sib=""
    tmux -L "${SOCK_C}" new-window -d -n "${win}" 2>/dev/null || { echo "ERR ERR"; return; }
    p0=$(tmux -L "${SOCK_C}" display-message -p -t "${win}" '#{pane_id}' 2>/dev/null)
    mon=$(tmux -L "${SOCK_C}" split-window -v -t "${p0}" -P -F '#{pane_id}' -- \
            "${BIN}" --config-dir "${TMPDIR_C}" session-status --monitor --task "${focal}" \
            2>/dev/null)
    [[ -z "${mon}" ]] && { echo "ERR ERR"; return; }
    # Wait for the self-fit. The pane starts at tmux's even split; poll until the
    # height MOVES OFF that starting value rather than until it is merely "small"
    # — the even split of a 50-row window is already 24 rows, so a magnitude test
    # would pass before the monitor had painted anything.
    local start
    start=$(tmux -L "${SOCK_C}" display-message -p -t "${mon}" '#{pane_height}' 2>/dev/null)
    h="${start}"
    while (( tries < 40 )); do
        h=$(tmux -L "${SOCK_C}" display-message -p -t "${mon}" '#{pane_height}' 2>/dev/null)
        [[ -n "${h}" && "${h}" != "${start}" ]] && break
        sleep 0.25; tries=$((tries + 1))
    done
    sib=$(tmux -L "${SOCK_C}" display-message -p -t "${p0}" '#{pane_height}' 2>/dev/null)
    echo "${h:-ERR} ${sib:-ERR}"
}

run_selfsize_layer() {
    section "C — self-sizing E2E: the monitor pane fits its own frame"
    note "seeds a THROWAWAY --config-dir DB (real ledger untouched) and runs the"
    note "monitor in a \`tmux -L ${SOCK_C}\` pane"

    TMPDIR_C=$(mktemp -d) || { report_fail "layer C scratch dir" "mktemp -d ok" "failed"; return; }

    # Materialize + migrate the throwaway DB, then seed a focal task with four
    # children (children are surfaced rows, E-1691) for a known 6-line frame.
    "${BIN}" --config-dir "${TMPDIR_C}" session-status --task 999999 >/dev/null 2>&1
    if [[ ! -f "${TMPDIR_C}/endless.db" ]]; then
        report_fail "throwaway DB is created" "endless.db in --config-dir" "missing"
        return
    fi
    report_pass "throwaway DB is created"

    sqlite3 "${TMPDIR_C}/endless.db" <<SQL 2>/dev/null
INSERT INTO projects (id,name,path) VALUES (1,'e1851probe','${TMPDIR_C}/proj');
INSERT INTO tasks (id,project_id,parent_id,title,phase,status,type_id) VALUES
 (100,1,NULL,'Focal probe task','now','underway',1),
 (101,1,100,'Child one','now','ready',1),
 (102,1,100,'Child two','now','ready',1),
 (103,1,100,'Child three','now','ready',1),
 (104,1,100,'Child four','now','ready',1);
SQL

    # The one-shot render is the ground truth the monitor pane must fit.
    local frame_lines
    frame_lines=$("${BIN}" --config-dir "${TMPDIR_C}" session-status --task 100 --cols 100 \
                    2>/dev/null | grep -c .)
    assert_eq "seeded frame is 1 legend + 5 task rows" "6" "${frame_lines}"

    # Same isolation as layer B. This layer already passes --config-dir on every
    # endless-go call, but the server-level guard covers anything it spawns.
    if ! XDG_CONFIG_HOME="${TMPDIR_C}/config" \
         tmux -L "${SOCK_C}" -f /dev/null \
            new-session -d -s fit -x "${WIN_COLS}" -y "${WIN_ROWS}" 2>/dev/null; then
        report_fail "private tmux server starts" "tmux -L new-session exit 0" "failed"
        return
    fi
    report_pass "private tmux server starts"

    # Populated frame: pane height == frame lines + 1 slack row.
    local out mh sh
    out=$(c_monitor_height 100); mh=${out%% *}; sh=${out##* }
    assert_eq "monitor pane fits the 6-line frame (+1 slack row)" \
        "$(( frame_lines + 1 ))" "${mh}"
    assert_eq "the sibling pane absorbs the rest of the window" \
        "${WIN_ROWS}" "$(( mh + sh + 1 ))"

    # Empty frame (unknown focal → the no-task hint). NOT an exact fit: a 2-row
    # sliver reads as a broken pane rather than an empty monitor, and leaves no
    # room to grow into the moment a task resolves.
    out=$(c_monitor_height 999999); mh=${out%% *}; sh=${out##* }
    assert_eq "hint-only frame holds the empty height, not a 2-row sliver" "8" "${mh}"
    assert_eq "the sibling pane absorbs the rest for the empty fit" \
        "${WIN_ROWS}" "$(( mh + sh + 1 ))"
}

# ─── layer D — project-wide regression ──────────────────────────────────────

run_regression_layer() {
    section "D — project-wide regression"
    note "the full Go and Python suites; nothing in E-1851 may regress them"
    assert_succeeds "go test ./..." go test ./...
    assert_succeeds "just test (full Python suite)" just test
}

# ─── main ───────────────────────────────────────────────────────────────────

main() {
    WT=$(git rev-parse --show-toplevel 2>/dev/null)
    [[ -z "${WT}" ]] && { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    cd "${WT}" || exit 2

    local tool
    for tool in just go uv sqlite3 tmux endless; do
        command -v "${tool}" >/dev/null 2>&1 || {
            printf 'ERROR: %s not on PATH\n' "${tool}" >&2; exit 2; }
    done

    BIN="${WT}/bin/endless-go"
    SOCK_B="e1851b$$"
    SOCK_C="e1851c$$"
    trap cleanup EXIT

    printf '%sE-1851 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"
    printf '  tmux:     private servers (%s, %s), killed on exit\n' "${SOCK_B}" "${SOCK_C}"
    printf '  db:       throwaway --config-dir; real ledger untouched\n'

    run_unit_layer
    run_layout_layer
    run_selfsize_layer
    run_regression_layer

    summary
}

main "$@"
