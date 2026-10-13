#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2240 and records what was true when E-2240
# landed. Edit it only if you ARE E-2240. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2240: `endless tmux start` (alias `endless start`) launches Endless's own
# console and task-session tmux servers, addressed by full socket path.
#
# What is verified here, against real tmux servers in a socket directory of
# this run's own (tmux.socket_dir, set through the run's temporary HOME), a
# throwaway database, a stub terminal command and a stub Claude:
#   A. Fail-fast: this task's Go and Python tests.
#   B. Both sockets land in tmux.socket_dir whatever TMUX_TMPDIR says; with no
#      terminal to open, both attach commands are printed; both config layers
#      load; both servers carry @server_uuid.
#   C. A second start reuses both servers.
#   D. A socket directory owned by someone else, or writable by others, is
#      refused; configured socket names and session are used.
#   E. A console started from a project opens with that project's window.
#   F. `project-window` adds a window named for the project on every run, and
#      opens a terminal onto a console nobody is attached to.
#   G. A spawn from outside tmux lands in `tasks` on the task-session server.
#   H. After the task-session server is killed, start recreates exactly the
#      task windows that were open and resumes them; --no-restore recreates
#      none.
#   I. Sessions bound to panes on both servers read `live`.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

command -v tmux >/dev/null 2>&1 || setup_error "tmux is required"
command -v sqlite3 >/dev/null 2>&1 || setup_error "sqlite3 is required to seed the probe database"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
TMP="$(cd "${TMP}" && pwd -P)"
# Short and per-run, as the plan asks: socket paths are limited to ~104 bytes.
SOCK="/tmp/endless-verify-$$"
CONSOLE="${SOCK}/endless-console"
TASKS="${SOCK}/endless-tasks"

cleanup() {
    for s in "${SOCK}"/* "${SOCK}-names"/*; do
        [[ -S "${s}" ]] && env -u TMUX tmux -S "${s}" kill-server 2>/dev/null
    done
    rm -rf "${TMP}" "${SOCK}" "${SOCK}-names"
}
trap cleanup EXIT

# The user config in the runner's temporary HOME; every tmux setting is read
# from it alone. Written under BOTH spellings: the config loader reads
# ~/.config on macOS and XDG_CONFIG_HOME on Linux.
CFG_DIR="${HOME}/.config/endless"
XDG_CFG_DIR="${XDG_CONFIG_HOME:-${HOME}/.config}/endless"
mkdir -p "${CFG_DIR}" "${XDG_CFG_DIR}" || setup_error "cannot create the config directories"
write_config() { printf '%s\n' "$1" | tee "${CFG_DIR}/config.json" >"${XDG_CFG_DIR}/config.json"; }
# Where the record lives is the loader's choice, so it is found, not assumed.
state() { cat "${CFG_DIR}/tmux-task-windows.json" 2>/dev/null || cat "${XDG_CFG_DIR}/tmux-task-windows.json" 2>/dev/null; }

# The user's own tmux.conf, which both servers must load before Endless's layer.
printf 'set -g @user_conf_loaded yes\n' >"${HOME}/.tmux.conf"

# A stub terminal: records the command it was asked to run.
TERMLOG="${TMP}/terminal.log"
printf '#!/bin/sh\necho "$*" >> %s\n' "${TERMLOG}" >"${TMP}/term.sh"
chmod +x "${TMP}/term.sh"
# A stub Claude: a process that stays up.
printf '#!/bin/sh\nexec sleep 600\n' >"${TMP}/claude"
chmod +x "${TMP}/claude"
: >"${TMP}/handoff"

t() { env -u TMUX -u TMUX_PANE tmux -S "$1" "${@:2}"; }

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

PKGS=(./internal/tmuxservers ./internal/tmuxstartcmd ./internal/projectstatuscmd
      ./internal/resumewindowscmd ./internal/spawnlaunchcmd ./internal/config
      ./internal/monitor ./internal/tmuxcmd)
if out=$(go test "${PKGS[@]}" 2>&1); then
    report_pass "go test: tmuxservers, tmuxstartcmd, projectstatuscmd, resumewindowscmd, spawnlaunchcmd, config, monitor, tmuxcmd"
else
    report_fail "go test" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi
if out=$(uv run pytest tests/test_tmux_start.py tests/test_spawn_foreground.py tests/test_guide_map.py -q 2>&1); then
    report_pass "pytest: tmux start, spawn routing, guide map"
else
    report_fail "pytest" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go || setup_error "cannot build cmd/endless-go from this tree"
# This tree's binary first on PATH, for everything the servers' panes run too
# (they inherit this environment). An older endless-go loading this run's
# config would not know the tmux keys, read it as empty, and go-cfgstore then
# rewrites an empty config over it — taking tmux.socket_dir, and with it this
# run's isolation, away mid-run.
export PATH="${TMP}:${PATH}"
PROBE="${TMP}/db"
mkdir -p "${PROBE}" "${TMP}/proj" "${TMP}/elsewhere" "${TMP}/tmuxtmp"
GO=("${BIN}" --db-dir "${PROBE}")
"${GO[@]}" session-status --task 1 >/dev/null 2>&1 || true
[[ -f "${PROBE}/endless.db" ]] || setup_error "the binary did not create the probe database"
sqlite3 "${PROBE}/endless.db" <<SQL || setup_error "cannot seed the probe database"
INSERT INTO projects (id, name, path) VALUES (1, 'probe', '${TMP}/proj');
INSERT INTO tasks (id, project_id, title, status, phase) VALUES
  (11,1,'eleven','underway','now'), (12,1,'twelve','underway','now'),
  (13,1,'thirteen','underway','now');
SQL

# start runs with no terminal detectable and no tmux around it, and with
# TMUX_TMPDIR pointed somewhere else entirely.
start() {
    (cd "$1" && env -u TMUX -u TMUX_PANE -u TERM_PROGRAM TERM=dumb \
        TMUX_TMPDIR="${TMP}/tmuxtmp" "${GO[@]}" tmux start "${@:2}" 2>&1)
}

# ---------------------------------------------------------------------------
section "B. Sockets, attach commands, config layers, identity"
# ---------------------------------------------------------------------------

write_config "{\"tmux\":{\"socket_dir\":\"${SOCK}\"}}"
OUT="$(start "${TMP}/elsewhere")"; rc=$?
# Nothing below may run unless the binary read this run's socket directory:
# the default is the user's real one.
[[ -S "${CONSOLE}" ]] || setup_error "start did not use this run's tmux.socket_dir: ${OUT}"
assert_eq "start exits 0" "0" "${rc}"
[[ -S "${CONSOLE}" ]] && report_pass "the console socket is at <socket_dir>/endless-console" \
    || report_fail "the console socket is at <socket_dir>/endless-console" "${CONSOLE}" "$(ls -la "${SOCK}" 2>&1)"
[[ -S "${TASKS}" ]] && report_pass "the task-session socket is at <socket_dir>/endless-tasks" \
    || report_fail "the task-session socket is at <socket_dir>/endless-tasks" "${TASKS}" "$(ls -la "${SOCK}" 2>&1)"
assert_eq "nothing was created under TMUX_TMPDIR" "" "$(ls -A "${TMP}/tmuxtmp")"
assert_eq "the socket directory is 0700" "drwx------" "$(ls -ld "${SOCK}" | cut -c1-10)"
assert_contains "the console attach command is printed" \
    "tmux -S ${CONSOLE} attach-session -t '=console'" "${OUT}"
assert_contains "the task-session attach command is printed" \
    "tmux -S ${TASKS} attach-session -t '=tasks'" "${OUT}"
assert_contains "outside a project the console opens on a shell" "with a shell window" "${OUT}"
for s in "${CONSOLE}" "${TASKS}"; do
    n="$(basename "${s}")"
    assert_eq "${n}: the user's tmux.conf loaded" "yes" "$(t "${s}" show -gv @user_conf_loaded 2>&1)"
    role="$(t "${s}" show -gv @endless_server 2>&1)"
    [[ "${role}" == console || "${role}" == tasks ]] && report_pass "${n}: Endless's layer loaded (@endless_server=${role})" \
        || report_fail "${n}: Endless's layer loaded" "console|tasks" "${role}"
    uuid="$(t "${s}" show -gv @server_uuid 2>/dev/null)"
    [[ -n "${uuid}" ]] && report_pass "${n}: carries @server_uuid" || report_fail "${n}: carries @server_uuid" "a uuid" "(empty)"
done
assert_eq "the sessions are console and tasks" "console tasks" \
    "$(t "${CONSOLE}" ls -F '#{session_name}') $(t "${TASKS}" ls -F '#{session_name}')"
UUID_TASKS="$(t "${TASKS}" show -gv @server_uuid)"
UUID_CONSOLE="$(t "${CONSOLE}" show -gv @server_uuid)"

# ---------------------------------------------------------------------------
section "C. A second start reuses both servers"
# ---------------------------------------------------------------------------

OUT="$(start "${TMP}/elsewhere")"
assert_contains "the console is reused" "console: running, reused" "${OUT}"
assert_contains "the task-session server is reused" "tasks: running, reused" "${OUT}"
assert_eq "same task-session server" "${UUID_TASKS}" "$(t "${TASKS}" show -gv @server_uuid)"
assert_eq "same console server" "${UUID_CONSOLE}" "$(t "${CONSOLE}" show -gv @server_uuid)"
assert_eq "the console still has one window" "1" "$(t "${CONSOLE}" list-windows -t '=console:' | wc -l | tr -d ' ')"

# ---------------------------------------------------------------------------
section "D. Unsafe socket directories; configured names"
# ---------------------------------------------------------------------------

write_config '{"tmux":{"socket_dir":"/usr"}}'
OUT="$(start "${TMP}/elsewhere")"; rc=$?
[[ ${rc} -ne 0 ]] && report_pass "a directory owned by someone else is refused (exit ${rc})" \
    || report_fail "a directory owned by someone else is refused" "non-zero exit" "exit 0"
assert_contains "and says whose it is" "is owned by uid 0, not by you" "${OUT}"

mkdir -p "${TMP}/open" && chmod 0777 "${TMP}/open"
write_config "{\"tmux\":{\"socket_dir\":\"${TMP}/open\"}}"
OUT="$(start "${TMP}/elsewhere")"; rc=$?
[[ ${rc} -ne 0 ]] && report_pass "a directory others can write is refused (exit ${rc})" \
    || report_fail "a directory others can write is refused" "non-zero exit" "exit 0"
assert_contains "and says why" "is writable by others" "${OUT}"
assert_eq "and no server was started in it" "" "$(ls -A "${TMP}/open")"

write_config "{\"tmux\":{\"socket_dir\":\"${SOCK}-names\",\"console_server\":\"c1\",\"tasks_server\":\"t1\",\"tasks_session\":\"work\"}}"
OUT="$(start "${TMP}/elsewhere")"
[[ -S "${SOCK}-names/c1" && -S "${SOCK}-names/t1" ]] && report_pass "configured socket names are used" \
    || report_fail "configured socket names are used" "c1 and t1" "$(ls -A "${SOCK}-names" 2>&1)"
assert_eq "the configured tasks session is used" "work" "$(t "${SOCK}-names/t1" ls -F '#{session_name}' 2>&1)"
t "${SOCK}-names/c1" kill-server; t "${SOCK}-names/t1" kill-server

# ---------------------------------------------------------------------------
section "E. A console started in a project opens on that project"
# ---------------------------------------------------------------------------

write_config "{\"tmux\":{\"socket_dir\":\"${SOCK}\",\"terminal\":\"${TMP}/term.sh {cmd}\"}}"
t "${CONSOLE}" kill-server
OUT="$(start "${TMP}/proj")"
assert_contains "the start says so" "with a monitor window for probe" "${OUT}"
assert_eq "the console's window is named for the project" "probe" \
    "$(t "${CONSOLE}" list-windows -t '=console:' -F '#{window_name}')"
assert_contains "a configured terminal is opened on the console" \
    "tmux -S ${CONSOLE} attach-session -t =console" "$(cat "${TERMLOG}")"
assert_contains "and on the task-session server" \
    "tmux -S ${TASKS} attach-session -t =tasks" "$(cat "${TERMLOG}")"

# ---------------------------------------------------------------------------
section "F. project-window adds a window per run and opens a terminal"
# ---------------------------------------------------------------------------

: >"${TERMLOG}"
pw() { (cd "${TMP}/elsewhere" && env -u TMUX -u TMUX_PANE "${GO[@]}" project-window --project probe 2>&1); }
OUT="$(pw)"
assert_eq "a second window named for the project" "2" \
    "$(t "${CONSOLE}" list-windows -t '=console:' -F '#{window_name}' | grep -cx probe)"
OUT="$(pw)"
assert_eq "and a third on the next run" "3" \
    "$(t "${CONSOLE}" list-windows -t '=console:' -F '#{window_name}' | grep -cx probe)"
assert_eq "with no terminal attached, each run opens one onto the console" "2" \
    "$(grep -c "attach-session -t =console" "${TERMLOG}")"
t "${CONSOLE}" kill-server
OUT="$(pw)"
assert_contains "it starts the console when none is running" "started the console" "${OUT}"
assert_eq "with the project's window" "probe" "$(t "${CONSOLE}" list-windows -t '=console:' -F '#{window_name}')"
assert_eq "and @server_uuid" "1" "$([[ -n "$(t "${CONSOLE}" show -gv @server_uuid 2>/dev/null)" ]] && echo 1)"

# ---------------------------------------------------------------------------
section "G. A spawn from outside tmux lands in tasks"
# ---------------------------------------------------------------------------

spawn() {
    (cd "${TMP}/proj" && env -u TMUX -u TMUX_PANE "${GO[@]}" spawn-window --claude-bin "${TMP}/claude" \
        --handoff-file "${TMP}/handoff" --task-id "$1" --project-id 1 --spawned-by verify \
        --window-name "E-$1" --cwd "${TMP}/proj" 2>&1)
}
OUT="$(spawn 11)"; rc=$?
assert_eq "spawn-window exits 0 outside tmux" "0" "${rc}"
assert_contains "the window is in tasks on the task-session server" "E-11" \
    "$(t "${TASKS}" list-windows -t '=tasks:' -F '#{window_name}')"
spawn 12 >/dev/null; spawn 13 >/dev/null
sleep 1
assert_eq "the record holds the open task windows" '{"windows":[11,12,13]}' "$(state)"

# ---------------------------------------------------------------------------
section "H. Restore after the task-session server is killed"
# ---------------------------------------------------------------------------

t "${TASKS}" kill-window -t '=tasks:E-13'
sleep 1
assert_eq "closing a window drops it from the record" '{"windows":[11,12]}' "$(state)"
t "${TASKS}" kill-server
sleep 0.5
assert_eq "killing the server leaves the record alone" '{"windows":[11,12]}' "$(state)"

OUT="$(start "${TMP}/elsewhere")"
assert_contains "start reports the restore" "restored 2 task window(s): E-11 E-12" "${OUT}"
assert_eq "exactly the windows that were open are back" "E-11 E-12" \
    "$(t "${TASKS}" list-windows -t '=tasks:' -F '#{window_name}' | sort | tr '\n' ' ' | sed 's/ $//')"
sleep 1
assert_contains "Claude is resumed in each" "session resume E-11" "$(t "${TASKS}" capture-pane -p -t '=tasks:E-11')"
assert_contains "and in the other" "session resume E-12" "$(t "${TASKS}" capture-pane -p -t '=tasks:E-12')"

t "${TASKS}" kill-server
sleep 0.5
OUT="$(start "${TMP}/elsewhere" --no-restore)"
assert_not_contains "--no-restore restores nothing" "restored" "${OUT}"
assert_eq "and no task window exists" "" \
    "$(t "${TASKS}" list-windows -t '=tasks:' -F '#{window_name}' | grep '^E-')"

# ---------------------------------------------------------------------------
section "I. Sessions on both servers read live"
# ---------------------------------------------------------------------------

spawn 11 >/dev/null
TPANE="$(t "${TASKS}" list-panes -t '=tasks:E-11' -F '#{pane_id}' | head -1)"
CPANE="$(t "${CONSOLE}" list-panes -t '=console:' -F '#{pane_id}' | head -1)"
UT="$(t "${TASKS}" show -gv @server_uuid)"
UC="$(t "${CONSOLE}" show -gv @server_uuid)"
sqlite3 "${PROBE}/endless.db" <<SQL || setup_error "cannot seed the liveness sessions"
INSERT INTO processes (id, kind_id, server_uuid, address) VALUES
  (901, 1, '${UT}', '${TPANE}'), (902, 1, '${UC}', '${CPANE}');
INSERT INTO sessions (id, project_id, state, task_id, process_id) VALUES
  (901, 1, 'working', 11, 901), (902, 1, 'idle', NULL, 902);
SQL
LIVE="$(cd "${TMP}/elsewhere" && env -u TMUX -u TMUX_PANE "${GO[@]}" session-query session-liveness 901 902 2>&1)"
assert_contains "the task-session server's session is live, and the console's" '"live","live"' \
    "$(printf '%s' "${LIVE}" | tr -d ' \n')"

summary
