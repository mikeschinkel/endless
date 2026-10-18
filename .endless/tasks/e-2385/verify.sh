#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2385 and records what was true when E-2385
# landed. Edit it only if you ARE E-2385. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2385: `endless stop` winds Endless's tmux servers down gracefully, and
# spawns refuse rather than fall back to another tmux server when
# endless-tasks is not running.
#
# Proved against real tmux servers in a socket directory of this run's own
# (tmux.socket_dir, set through the run's temporary HOME), a throwaway
# database, a stub terminal command and a fake Claude that quits on `/exit`:
#   A. Fail-fast: this task's Go and Python tests (among them the default
#      terminal per platform and PATH, a pure function).
#   B. With endless-tasks down, a spawn refuses and creates nothing — outside
#      tmux, inside another tmux server, and naming a session.
#   C. No command but start starts a server: project-window refuses.
#   D. --tmux-session names a session on endless-tasks, never the caller's.
#   E. The record keeps tab order; a reorder alone rewrites it.
#   F. stop tasks refuses while a session there is working, listing it.
#   G. stop tasks types /exit, waits for every Claude to quit, stops the
#      server, and leaves the record as it was when the stop began; start
#      restores those windows in that order.
#   H. A Claude still running at the timeout keeps the server up, and stop all
#      then leaves the console running; --force stops it.
#   I. stop console; stopping servers that are not running; bare stop.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

command -v tmux >/dev/null 2>&1 || setup_error "tmux is required"
command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required to seed the probe database"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
TMP="$(cd "${TMP}" && pwd -P)"
# Short and per-run: socket paths are limited to ~104 bytes.
SOCK="/tmp/endless-verify-$$"
CONSOLE="${SOCK}/endless-console"
TASKS="${SOCK}/endless-tasks"

cleanup() {
    for s in "${SOCK}"/*; do
        [[ -S "${s}" ]] && env -u TMUX tmux -S "${s}" kill-server 2>/dev/null
    done
    rm -rf "${TMP}" "${SOCK}"
}
trap cleanup EXIT

# The user config in the runner's temporary HOME, under both spellings: the
# loader reads ~/.config on macOS and XDG_CONFIG_HOME on Linux. A stub terminal
# is configured throughout: with none, start would now open Terminal.app.
CFG_DIR="${HOME}/.config/endless"
XDG_CFG_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/endless"
mkdir -p "${CFG_DIR}" "${XDG_CFG_DIR}" || setup_error "cannot create the config directories"
TERMLOG="${TMP}/terminal.log"
printf '#!/bin/sh\necho "$*" >> %s\n' "${TERMLOG}" >"${TMP}/term.sh"
chmod +x "${TMP}/term.sh"
printf '{"tmux":{"socket_dir":"%s","terminal":"%s {cmd}"}}\n' "${SOCK}" "${TMP}/term.sh" \
    | tee "${CFG_DIR}/config.json" >"${XDG_CFG_DIR}/config.json"
state() { cat "${CFG_DIR}/tmux-task-windows.json" 2>/dev/null || cat "${XDG_CFG_DIR}/tmux-task-windows.json" 2>/dev/null; }

t() { env -u TMUX -u TMUX_PANE tmux -S "$1" "${@:2}"; }
# The task windows of a session on endless-tasks, in tab order.
names() { t "${TASKS}" list-windows -t "=${1:-tasks}:" -F '#{window_name}' 2>/dev/null | grep '^E-' | tr '\n' ' ' | sed 's/ $//'; }

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

PKGS=(./internal/tmuxstopcmd ./internal/tmuxservers ./internal/tmuxstartcmd ./internal/spawnlaunchcmd
      ./internal/autospawnjob ./internal/projectstatuscmd ./internal/monitor ./internal/tmuxcmd)
if out=$(go test "${PKGS[@]}" 2>&1); then
    report_pass "go test: tmuxstopcmd, tmuxservers, tmuxstartcmd, spawnlaunchcmd, autospawnjob, projectstatuscmd, monitor, tmuxcmd"
else
    report_fail "go test" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi
if out=$(uv run pytest tests/test_tmux_stop.py tests/test_tmux_start.py tests/test_spawn_foreground.py \
        tests/test_claim_launches_a_session.py tests/test_task_claim_worktree.py tests/test_claim_output_format.py \
        tests/test_prime.py tests/test_guide_map.py -q 2>&1); then
    report_pass "pytest: stop, start, spawn and claim gates, prime, guide map"
else
    report_fail "pytest" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go || setup_error "cannot build cmd/endless-go from this tree"
# This tree's binary first on PATH, for everything the servers' panes run too.
export PATH="${TMP}:${PATH}"

# A fake Claude: quits on reading the line /exit; `stay` never quits.
mkdir -p "${TMP}/fake"
cat >"${TMP}/fake/main.go" <<'GO'
package main

import (
	"bufio"
	"os"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "stay" {
		time.Sleep(time.Hour)
	}
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		if sc.Text() == "/exit" {
			return
		}
	}
}
GO
mkdir -p "${TMP}/fakebin"
go build -o "${TMP}/fakebin/claude" "${TMP}/fake/main.go" || setup_error "cannot build the fake claude"
CLAUDE="${TMP}/fakebin/claude"

PROBE="${TMP}/db"
mkdir -p "${PROBE}" "${TMP}/proj"
GO=("${BIN}" --db-dir "${PROBE}")
"${GO[@]}" session-status --task 1 >/dev/null 2>&1 || true
[[ -f "${PROBE}/endless.db" ]] || setup_error "the binary did not create the probe database"
sqlite3 "${PROBE}/endless.db" <<SQL || setup_error "cannot seed the probe database"
INSERT INTO projects (id, name, path) VALUES (1, 'probe', '${TMP}/proj');
INSERT INTO tasks (id, project_id, title, status, phase) VALUES
  (11,1,'eleven','underway','now'), (12,1,'twelve','underway','now'),
  (13,1,'thirteen','underway','now'), (14,1,'fourteen','underway','now');
SQL
: >"${TMP}/handoff"

go_() { (cd "${TMP}/proj" && env -u TMUX -u TMUX_PANE "${GO[@]}" "$@" 2>&1); }
spawn() {
    "${GO[@]}" spawn-window --claude-bin "${CLAUDE}" --handoff-file "${TMP}/handoff" \
        --task-id "$1" --project-id 1 --spawned-by verify --window-name "E-$1" --cwd "${TMP}/proj" "${@:2}" 2>&1
}
start() { (cd "${TMP}" && env -u TMUX -u TMUX_PANE "${GO[@]}" tmux start "$@" 2>&1); }
stop_() { (cd "${TMP}" && env -u TMUX -u TMUX_PANE "${GO[@]}" tmux stop "$@" 2>&1); }

# ---------------------------------------------------------------------------
section "B. With endless-tasks down, spawns refuse and create nothing"
# ---------------------------------------------------------------------------

# The runner has already stripped TMUX and TMUX_PANE: this is outside tmux.
OUT="$(cd "${TMP}/proj" && spawn 11)"; rc=$?
assert_eq "outside tmux: spawn-window exits 1" "1" "${rc}"
assert_contains "and says Endless isn't running" "Endless isn't running. Run \`endless start\`, then retry." "${OUT}"
assert_eq "and no server was started" "" "$(ls -A "${SOCK}" 2>/dev/null)"

with_tmux true # with_tmux makes its fixture session on first use: count after it
before="$(env -u TMUX -u TMUX_PANE tmux list-windows -a 2>/dev/null | wc -l | tr -d ' ')"
OUT="$(cd "${TMP}/proj" && with_tmux spawn 11)"; rc=$?
assert_eq "inside another tmux server: spawn-window exits 1" "1" "${rc}"
assert_contains "and refuses the same way" "Endless isn't running" "${OUT}"
assert_eq "and opened no window on the server it is in" "${before}" \
    "$(env -u TMUX -u TMUX_PANE tmux list-windows -a 2>/dev/null | wc -l | tr -d ' ')"

OUT="$(cd "${TMP}/proj" && with_tmux spawn 11 --tmux-session endless-verify)"; rc=$?
assert_eq "naming a session there: still refused" "1" "${rc}"
assert_contains "with the same words" "Endless isn't running" "${OUT}"
assert_eq "and still no window" "${before}" \
    "$(env -u TMUX -u TMUX_PANE tmux list-windows -a 2>/dev/null | wc -l | tr -d ' ')"
assert_eq "no server was started by any of them" "" "$(ls -A "${SOCK}" 2>/dev/null | grep -v '\.conf$')"

# ---------------------------------------------------------------------------
section "C. Only start starts a server: project-window refuses"
# ---------------------------------------------------------------------------

OUT="$(go_ project-window --project probe --no-switch)"; rc=$?
assert_eq "project-window with the console down exits 1" "1" "${rc}"
assert_contains "and says Endless isn't running" "Endless isn't running" "${OUT}"
[[ -S "${CONSOLE}" ]] && report_fail "and starts no console" "no socket" "${CONSOLE}" \
    || report_pass "and starts no console"

# ---------------------------------------------------------------------------
section "D. --tmux-session names a session on endless-tasks"
# ---------------------------------------------------------------------------

OUT="$(start --no-restore)"
[[ -S "${TASKS}" ]] || setup_error "start did not use this run's tmux.socket_dir: ${OUT}"
t "${TASKS}" new-session -d -s work -c "${TMP}"
OUT="$(cd "${TMP}/proj" && with_tmux spawn 11 --tmux-session work)"; rc=$?
assert_eq "a session on endless-tasks is found from inside another server" "0" "${rc}"
assert_contains "and the window is in it" "E-11" "$(names work)"
OUT="$(cd "${TMP}/proj" && with_tmux spawn 12 --tmux-session endless-verify)"; rc=$?
assert_eq "a session only the caller's server has is refused" "1" "${rc}"
assert_contains "naming the task-session server" "task-session server is named \"endless-verify\"" "${OUT}"
assert_not_contains "and no window opened on the caller's server" "E-12" \
    "$(env -u TMUX -u TMUX_PANE tmux list-windows -a -F '#{window_name}' 2>/dev/null)"
t "${TASKS}" kill-session -t '=work'

# ---------------------------------------------------------------------------
section "E. The record keeps tab order; a reorder rewrites it"
# ---------------------------------------------------------------------------

# Windows whose only pane is the fake Claude: when it quits, the window closes,
# which fires the hook that would rewrite the record.
win() { t "${TASKS}" new-window -d -t '=tasks:' -n "E-$1" -c "${TMP}/proj" -- "${CLAUDE}" "${@:2}"; }
win 13; win 11; win 12
sleep 1
assert_eq "the record lists the windows in tab order" '{"windows":[13,11,12]}' "$(state)"
t "${TASKS}" swap-window -d -s '=tasks:E-13' -t '=tasks:E-12'
sleep 1
assert_eq "a swap alone rewrites the record" '{"windows":[12,11,13]}' "$(state)"
t "${TASKS}" swap-window -d -s '=tasks:E-13' -t '=tasks:E-12'
sleep 1
assert_eq "and swapping back rewrites it again" '{"windows":[13,11,12]}' "$(state)"

# ---------------------------------------------------------------------------
section "F. stop tasks refuses while a session there is working"
# ---------------------------------------------------------------------------

UT="$(t "${TASKS}" show -gv @server_uuid)"
P13="$(t "${TASKS}" list-panes -t '=tasks:E-13' -F '#{pane_id}')"
P11="$(t "${TASKS}" list-panes -t '=tasks:E-11' -F '#{pane_id}')"
sqlite3 "${PROBE}/endless.db" <<SQL || setup_error "cannot seed the sessions"
INSERT INTO processes (id, kind_id, server_uuid, address) VALUES (901, 1, '${UT}', '${P13}'), (902, 1, '${UT}', '${P11}');
INSERT INTO sessions (id, project_id, state, task_id, process_id) VALUES
  (901, 1, 'working', 13, 901), (902, 1, 'idle', 11, 902);
SQL
OUT="$(stop_ tasks)"; rc=$?
assert_eq "stop tasks exits 1" "1" "${rc}"
assert_contains "and lists the working session" "E-13  ES-901  working" "${OUT}"
assert_not_contains "but not the idle one" "ES-902" "${OUT}"
assert_eq "the server is still running" "E-13 E-11 E-12" "$(names)"
assert_eq "and nothing was typed into it" "" "$(t "${TASKS}" capture-pane -p -t "${P13}" | tr -d '[:space:]')"

# ---------------------------------------------------------------------------
section "G. stop tasks: /exit, wait, stop; start restores the order"
# ---------------------------------------------------------------------------

sqlite3 "${PROBE}/endless.db" "UPDATE sessions SET state = 'idle' WHERE id = 901;"
OUT="$(stop_ tasks --timeout 10s)"; rc=$?
assert_eq "stop tasks exits 0" "0" "${rc}"
assert_contains "it asked each Claude to quit" "asked 3 Claude session(s) to quit" "${OUT}"
assert_contains "and stopped the server" "tasks: stopped" "${OUT}"
t "${TASKS}" list-sessions >/dev/null 2>&1 && report_fail "the server is gone" "not running" "running" \
    || report_pass "the server is gone"
assert_eq "every window closed as its Claude quit, yet the record holds them, in order" \
    '{"windows":[13,11,12]}' "$(state)"
assert_eq "the console was left alone" "0" "$(t "${CONSOLE}" list-sessions >/dev/null 2>&1; echo $?)"

OUT="$(start)"
assert_contains "start restores all three" "restored 3 task window(s)" "${OUT}"
assert_eq "in the recorded tab order" "E-13 E-11 E-12" "$(names)"

# ---------------------------------------------------------------------------
section "H. A Claude that will not quit"
# ---------------------------------------------------------------------------

win 14 stay
for w in E-13 E-11 E-12; do t "${TASKS}" kill-window -t "=tasks:${w}" 2>/dev/null; done
sleep 0.5
OUT="$(stop_ all --timeout 1s)"; rc=$?
assert_eq "stop all exits 1" "1" "${rc}"
assert_contains "and lists the pane Claude is still running in" "Claude has not quit in 1 pane(s)" "${OUT}"
assert_contains "the console is left running too" "console: left running" "${OUT}"
assert_eq "the task-session server is still running" "0" "$(t "${TASKS}" list-sessions >/dev/null 2>&1; echo $?)"
assert_eq "and so is the console" "0" "$(t "${CONSOLE}" list-sessions >/dev/null 2>&1; echo $?)"
assert_eq "the record is not frozen on a server left running" "" "$(t "${TASKS}" show -gqv @endless_stopping)"
OUT="$(stop_ tasks --force --timeout 1s)"; rc=$?
assert_eq "--force stops it anyway" "0" "${rc}"
t "${TASKS}" list-sessions >/dev/null 2>&1 && report_fail "the server is gone" "not running" "running" \
    || report_pass "the server is gone"

# ---------------------------------------------------------------------------
section "I. stop console; already stopped; bare stop"
# ---------------------------------------------------------------------------

OUT="$(stop_ console)"; rc=$?
assert_eq "stop console exits 0" "0" "${rc}"
assert_contains "and says so" "console: stopped" "${OUT}"
t "${CONSOLE}" list-sessions >/dev/null 2>&1 && report_fail "the console is gone" "not running" "running" \
    || report_pass "the console is gone"
OUT="$(stop_ --all)"; rc=$?
assert_eq "stop --all with nothing running exits 0" "0" "${rc}"
assert_contains "the task-session server reads already stopped" "tasks: already stopped" "${OUT}"
assert_contains "and the console too" "console: already stopped" "${OUT}"
OUT="$(stop_)"; rc=$?
assert_eq "a bare stop exits 2" "2" "${rc}"
assert_contains "and prints usage" "Usage: endless stop tasks|console|all" "${OUT}"
OUT="$(uv run endless stop 2>&1)"; rc=$?
assert_eq "endless stop with no argument exits 2" "2" "${rc}"
assert_contains "and prints usage" "Usage: endless stop" "${OUT}"

summary
