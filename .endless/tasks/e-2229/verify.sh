#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2229 and records what was true when E-2229
# landed. Edit it only if you ARE E-2229. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2229: gate monitor work on tmux window visibility.
#
#   A. The task's tests (fail-fast): the Loop state machine, the visibility
#      check, hook install and wake argv, and monitorvis's own end-to-end runs
#      on a private tmux server.
#   B. The real thing: the worktree's endless-go runs `session-status
#      --monitor` in two windows of a private tmux server, against a throwaway
#      database, each under the ENDLESS_MONITOR_PROFILE diagnostic. A
#      control-mode client is attached, so exactly one window is on screen.
#      Only that window's monitor renders; `select-window` moves the work to
#      the other one at once, through the hooks the monitors installed.
#   C. The safety net: the hooks are removed by hand, and the next poll puts
#      them back and records WARN-0034. Across that wait, the hidden monitor's
#      CPU stays under 0.1%.
#   D. `endless-go monitor-wake` outside tmux is silent and exits 0.
#
# Everything runs against throwaway state: a temp --db-dir database and a tmux
# server under a temp TMUX_TMPDIR. All removed on exit.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(cd "${ENDLESS_VERIFY_DIR}/../../.." && pwd)"
BIN="${WT}/bin/endless-go"
SCRATCH=""
SERVER_STARTED=""
CLIENT_PID=""

cleanup() {
    [[ -n "${CLIENT_PID}" ]] && kill "${CLIENT_PID}" 2>/dev/null
    if [[ -n "${SERVER_STARTED}" ]]; then
        TMUX_TMPDIR="${SCRATCH}/tmx" tmux kill-server 2>/dev/null
    fi
    [[ -n "${SCRATCH}" ]] && rm -rf "${SCRATCH}"
    return 0
}
trap cleanup EXIT

for tool in go tmux sqlite3; do
    command -v "${tool}" >/dev/null 2>&1 || setup_error "${tool} is required"
done
# A short path: a unix socket's whole path must fit in ~104 bytes.
SCRATCH="$(mktemp -d /tmp/e2229v.XXXXXX)" || setup_error "mktemp failed"

# ─────────────────────────────────────────────────────────────────────
section "A — the task's tests (fail-fast)"

out="$(cd "${WT}" && go test -count=1 ./internal/monitorvis 2>&1 && \
    go test -count=1 -run 'TestLoop_|TestRestarter' ./internal/liveview 2>&1 && \
    go test -count=1 -run 'TestCatalog' ./internal/faults 2>&1 && \
    go test -count=1 ./internal/tmuxcmd 2>&1)"
rc=$?
if (( rc == 0 )); then
    report_pass "monitorvis, liveview, faults and tmuxcmd tests pass"
else
    report_fail "monitorvis, liveview, faults and tmuxcmd tests pass" "exit 0" \
        "exit ${rc}: $(printf '%s' "${out}" | tail -20)"
    summary
fi

(cd "${WT}" && just build >/dev/null 2>&1) || setup_error "just build failed"

# ─────────────────────────────────────────────────────────────────────
section "B — two real monitors, one window on screen"

dbdir="${SCRATCH}/db"
mkdir -p "${dbdir}"
"${BIN}" --db-dir "${dbdir}" session-status --task 999999 >/dev/null 2>&1
[[ -f "${dbdir}/endless.db" ]] || setup_error "throwaway database was not created"
{
    printf "INSERT INTO projects (id,name,path) VALUES (1,'e2229probe','%s/proj');\n" "${SCRATCH}"
    printf "INSERT INTO tasks (id,project_id,title,status,phase,type_id) VALUES
        (100,1,'probe focal','underway','now',(SELECT id FROM task_types WHERE slug='todo'));\n"
    printf "INSERT INTO sessions (id,session_id,project_id,task_id) VALUES (7,'probe-session',1,100);\n"
    # Every job not due, so a visible monitor's trigger runs nothing.
    for j in auto-spawn db-backup doc-mirrors fault-triage main-sync minimizer-loop \
             prime rater worktree-paths worktree-unlanded; do
        printf "INSERT INTO jobs (name,next_due_at,created_at,updated_at) VALUES
            ('%s','2999-01-01T00:00:00','2000-01-01T00:00:00','2000-01-01T00:00:00');\n" "${j}"
    done
} | sqlite3 "${dbdir}/endless.db" || setup_error "seeding the throwaway database"

export TMUX_TMPDIR="${SCRATCH}/tmx"
mkdir -p "${TMUX_TMPDIR}"
unset TMUX TMUX_PANE

monitor_cmd() {
    printf '%s' "env ENDLESS_MONITOR_PROFILE=${SCRATCH}/prof-$1 ${BIN} --db-dir ${dbdir} session-status --monitor --task 100 --session 7"
}
tmux -f /dev/null new-session -d -s probe -n w0 -x 160 -y 40 "$(monitor_cmd w0)" \
    || setup_error "tmux server"
SERVER_STARTED=1
tmux new-window -d -t probe: -n w1 "$(monitor_cmd w1)" || setup_error "second window"

for _ in $(seq 1 50); do
    tagged="$(tmux list-panes -a -F '#{@endless_monitor_upid}' | grep -c @)"
    (( tagged == 2 )) && break
    sleep 0.1
done
assert_eq "both monitors tag their panes for the wake" "2" "${tagged}"

# A control-mode client: a real attached client, needing no terminal.
sleep 600 | tmux -C attach -t probe >/dev/null 2>&1 &
CLIENT_PID=$!

visible_lines() { grep -c 'state=visible' "${SCRATCH}/prof-$1/ticks.log" 2>/dev/null || true; }

sleep 3
w0="$(visible_lines w0)"
w1="$(visible_lines w1)"
if (( w0 >= 1 )); then
    report_pass "the window on screen renders (${w0} ticks)"
else
    report_fail "the window on screen renders" ">= 1 visible tick" "${w0}"
fi
assert_eq "the window off screen renders nothing" "0" "${w1}"

tmux select-window -t probe:w1
sleep 1.5
w1="$(visible_lines w1)"
if (( w1 >= 1 )); then
    report_pass "switching to w1 makes its monitor render within 1.5s (${w1} ticks)"
else
    report_fail "switching to w1 makes its monitor render within 1.5s" ">= 1 visible tick" "${w1}"
fi
a="$(visible_lines w0)"
sleep 2
b="$(visible_lines w0)"
assert_eq "the monitor switched away from stops rendering" "${a}" "${b}"

hooks="$(tmux show-hooks -g session-window-changed)"
assert_eq "two monitors starting together leave one wake hook" "1" \
    "$(printf '%s\n' "${hooks}" | grep -c 'monitor-wake')"

# ─────────────────────────────────────────────────────────────────────
section "C — hooks removed by hand come back at the next poll"

w0_pid="$(tmux list-panes -t probe:w0 -F '#{pane_pid}')"
cpu_seconds() {
    ps -o cputime= -p "$1" | awk -F: '{ s = 0; for (i = 1; i <= NF; i++) s = s * 60 + $i; print s }'
}
cpu_before="$(cpu_seconds "${w0_pid}")"
t_before="$(date +%s)"

for h in session-window-changed client-session-changed client-attached client-detached; do
    tmux set-hook -gu "${h}"
done
back=""
for _ in $(seq 1 30); do
    if tmux show-hooks -g client-detached | grep -q monitor-wake; then back=1; break; fi
    sleep 1
done
if [[ -n "${back}" ]]; then
    report_pass "the safety-net poll reinstalled the hooks"
else
    report_fail "the safety-net poll reinstalled the hooks" "monitor-wake on client-detached" \
        "$(tmux show-hooks -g client-detached)"
fi
assert_eq "the reinstall recorded WARN-0034" "1" \
    "$(sqlite3 "${dbdir}/endless.db" "SELECT COUNT(*) FROM errors WHERE code='WARN-0034'")"

cpu_after="$(cpu_seconds "${w0_pid}")"
elapsed=$(( $(date +%s) - t_before ))
pct="$(awk -v a="${cpu_before}" -v b="${cpu_after}" -v e="${elapsed}" 'BEGIN { printf "%.3f", (b - a) * 100 / e }')"
if awk -v p="${pct}" 'BEGIN { exit !(p < 0.1) }'; then
    report_pass "the hidden monitor used ${pct}% CPU over ${elapsed}s, including a poll"
else
    report_fail "the hidden monitor uses under 0.1% CPU" "< 0.1%" "${pct}% over ${elapsed}s"
fi
hidden="$(grep 'state=hidden' "${SCRATCH}/prof-w0/ticks.log" | tail -1)"
if [[ "${hidden}" == *" git=0 tmux=1 "* ]]; then
    report_pass "a hidden poll costs one tmux exec and no git: ${hidden}"
else
    report_fail "a hidden poll costs one tmux exec and no git" "git=0 tmux=1" "${hidden:-no hidden poll line}"
fi

# ─────────────────────────────────────────────────────────────────────
section "D — monitor-wake outside tmux"

out="$(env -u TMUX "${BIN}" monitor-wake 2>&1)"
rc=$?
assert_eq "monitor-wake outside tmux exits 0" "0" "${rc}"
assert_eq "monitor-wake outside tmux prints nothing" "" "${out}"

summary
