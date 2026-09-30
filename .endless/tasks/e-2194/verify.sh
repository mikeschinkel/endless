#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2194 and records what was true when E-2194
# landed. Edit it only if you ARE E-2194. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2194: each session monitor tags its own tmux pane with its UPID, and
# `endless session monitor --restart` respawns every live tagged pane in place.
#
# What is verified here:
#   A. Fail-fast: this task's own Go and Python tests.
#   B. Live, on a private tmux server (`tmux -L`), through the real Python CLI
#      and a binary built from this tree:
#      - a running monitor tags its pane with its own UPID;
#      - `--restart` outside tmux with no scope is a usage error;
#      - `--dry-run` names the pane and changes nothing;
#      - `--restart` puts a NEW monitor in the SAME pane, tagged;
#      - a monitor quit cleanly (Ctrl-C at a shell prompt) leaves no tag;
#      - a monitor killed with SIGKILL leaves a stale tag, which `--restart`
#        clears while leaving the shell in that pane alone.
#
# B's argv and decision rules are mirrored into
# internal/sessionmonitorcmd/*_test.go (including a private-server tag test),
# so the coverage outlives this suite.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

SOCK="endless-e2194-$$"
WORK="$(mktemp -d)" || setup_error "cannot create a scratch directory"
cleanup() {
    tmux -L "${SOCK}" kill-server 2>/dev/null
    rm -rf "${WORK}"
}
trap cleanup EXIT

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

if out=$(go test -count=1 ./internal/upid/ ./internal/sessionmonitorcmd/ ./internal/spawnlaunchcmd/ ./internal/sessionstatuscmd/ 2>&1); then
    report_pass "go test: upid, sessionmonitorcmd, spawnlaunchcmd, sessionstatuscmd"
else
    report_fail "go test: upid, sessionmonitorcmd, spawnlaunchcmd, sessionstatuscmd" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

if out=$(uv run --quiet pytest -q tests/test_session_monitor_restart.py 2>&1); then
    report_pass "pytest: session monitor --restart pass-through"
else
    report_fail "pytest: session monitor --restart pass-through" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# ---------------------------------------------------------------------------
section "B. Tag, restart, clean quit, stale tag — on a private tmux server"
# ---------------------------------------------------------------------------

command -v tmux >/dev/null || setup_error "tmux is not installed"
[[ -x "${WT}/.venv/bin/endless" ]] || setup_error "no ${WT}/.venv/bin/endless — run: uv sync"

# One directory holds exactly the two programs a monitor pane runs: the
# worktree's Python CLI and an endless-go built from this tree. Every pane and
# every command below puts it first on PATH, so nothing reaches an installed
# endless.
BIN="${WORK}/bin"
mkdir -p "${BIN}"
go build -o "${BIN}/endless-go" ./cmd/endless-go || setup_error "cannot build endless-go from this tree"
ln -s "${WT}/.venv/bin/endless" "${BIN}/endless"
export PATH="${BIN}:${PATH}"

tm() { tmux -L "${SOCK}" "$@"; }
tag_of() { tm show-options -p -v -t "$1" @endless_session_monitor 2>/dev/null; }
monitor_pid() { # the endless-go process in pane $1, via its tty
    ps -o pid=,command= -t "$(tm display-message -p -t "$1" '#{pane_tty}' | sed 's|^/dev/||')" \
        | awk '/endless-go session-status --monitor/ {print $1}'
}
wait_for() { # wait_for <seconds> <command...>: poll until the command succeeds
    local n=$(( $1 * 5 )); shift
    while (( n-- > 0 )); do "$@" && return 0; sleep 0.2; done
    return 1
}
tagged() { [[ -n "$(tag_of "$1")" ]]; }
untagged() { [[ -z "$(tag_of "$1")" ]]; }

# The first pane is a plain bash, not the login shell: under the runner's empty
# HOME a zsh would stop at its new-user wizard and swallow the keys sent below.
tm -f /dev/null new-session -d -s act -x 160 -y 50 \
    -e "PATH=${PATH}" -e "HOME=${HOME}" -e "XDG_CONFIG_HOME=${XDG_CONFIG_HOME:-${HOME}/.config}" \
    -- /bin/bash --noprofile --norc \
    || setup_error "cannot start a private tmux server"
tm new-session -d -s other -e "PATH=${PATH}" -e "HOME=${HOME}"
SHELL_PANE=$(tm display-message -p -t act '#{pane_id}')
MON_PANE=$(tm split-window -t "${SHELL_PANE}" -P -F '#{pane_id}' -- endless session monitor)
OTHER_PANE=$(tm split-window -t other -P -F '#{pane_id}' -- endless session monitor)
# $TMUX is how every bare `tmux` — the restart's included — finds a server.
export TMUX="$(tm display-message -p -t act '#{socket_path}'),0,0"
unset TMUX_PANE

wait_for 10 tagged "${MON_PANE}" || setup_error "monitor pane ${MON_PANE} never tagged itself"
wait_for 10 tagged "${OTHER_PANE}" || setup_error "monitor pane ${OTHER_PANE} never tagged itself"
OLD_PID=$(monitor_pid "${MON_PANE}")
OTHER_PID=$(monitor_pid "${OTHER_PANE}")
assert_eq "a running monitor tags its pane with its own pid" "${OLD_PID}" "$(tag_of "${MON_PANE}" | cut -d@ -f1)"
assert_contains "the tag is a UPID (pid@start)" "${OLD_PID}@" "$(tag_of "${MON_PANE}")"
assert_eq "an untagged shell pane carries no tag" "" "$(tag_of "${SHELL_PANE}")"

out=$(endless session monitor --restart 2>&1); rc=$?
assert_eq "--restart outside tmux with no scope exits 2" "2" "${rc}"
assert_contains "and says why" "not inside tmux" "${out}"

out=$(endless session monitor --restart --tmux-session act --dry-run 2>&1)
assert_contains "--dry-run names the monitor pane" "would restart ${MON_PANE}" "${out}"
assert_not_contains "--dry-run leaves the shell pane out" "${SHELL_PANE} " "${out}"
assert_eq "--dry-run changes nothing" "${OLD_PID}" "$(monitor_pid "${MON_PANE}")"

out=$(endless session monitor --restart --tmux-session act 2>&1)
assert_contains "--restart reports the pane restarted" "restarted ${MON_PANE}" "${out}"
new_pid() { local p; p=$(monitor_pid "${MON_PANE}"); [[ -n "${p}" && "${p}" != "${OLD_PID}" ]]; }
wait_for 10 new_pid || true
NEW_PID=$(monitor_pid "${MON_PANE}")
if [[ -n "${NEW_PID}" && "${NEW_PID}" != "${OLD_PID}" ]]; then
    report_pass "the SAME pane now holds a NEW monitor"
else
    report_fail "the SAME pane now holds a NEW monitor" "a pid other than ${OLD_PID}" "${NEW_PID:-none}"
fi
retagged() { [[ "$(tag_of "${MON_PANE}" | cut -d@ -f1)" == "${NEW_PID}" ]]; }
wait_for 10 retagged || true
assert_eq "the new monitor tagged the pane with its own pid" "${NEW_PID}" "$(tag_of "${MON_PANE}" | cut -d@ -f1)"
assert_eq "a monitor in another tmux session was not restarted" "${OTHER_PID}" "$(monitor_pid "${OTHER_PANE}")"

# The `esm` case: a monitor typed at a shell prompt, quit with Ctrl-C.
tm send-keys -t "${SHELL_PANE}" "endless session monitor" Enter
wait_for 10 tagged "${SHELL_PANE}" || setup_error "monitor typed at the shell never tagged its pane"
tm send-keys -t "${SHELL_PANE}" C-c
wait_for 10 untagged "${SHELL_PANE}" || true
assert_eq "a monitor quit cleanly leaves no tag" "" "$(tag_of "${SHELL_PANE}")"

# The same, killed outright: the tag survives on a pane that is a shell again.
tm send-keys -t "${SHELL_PANE}" "endless session monitor" Enter
wait_for 10 tagged "${SHELL_PANE}" || setup_error "second shell monitor never tagged its pane"
KILLED=$(tag_of "${SHELL_PANE}" | cut -d@ -f1)
kill -9 "${KILLED}"
shell_back() { [[ -z "$(monitor_pid "${SHELL_PANE}")" ]]; }
wait_for 10 shell_back || setup_error "SIGKILLed monitor ${KILLED} did not go away"
assert_contains "a SIGKILLed monitor leaves its tag behind" "${KILLED}@" "$(tag_of "${SHELL_PANE}")"
SHELL_PID=$(tm display-message -p -t "${SHELL_PANE}" '#{pane_pid}')

out=$(endless session monitor --restart --all-tmux-sessions 2>&1)
assert_contains "--restart skips the stale pane and says why" "skipped ${SHELL_PANE}" "${out}"
assert_contains "naming the dead process" "process ${KILLED} is gone" "${out}"
assert_eq "the stale tag is cleared" "" "$(tag_of "${SHELL_PANE}")"
assert_eq "the shell in that pane was not respawned" "${SHELL_PID}" "$(tm display-message -p -t "${SHELL_PANE}" '#{pane_pid}')"
assert_contains "--all-tmux-sessions reaches the other session's monitor" "restarted ${OTHER_PANE}" "${out}"

summary
