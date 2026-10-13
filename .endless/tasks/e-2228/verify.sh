#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2228 and records what was true when E-2228
# landed. Edit it only if you ARE E-2228. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2228: make an idle session monitor actually idle.
#
#   A. The task's unit tests (fail-fast): git argv, the per-frame status memo,
#      the dead-socket precheck, the job trigger's zero-write idle tick, the
#      pane fit's memory, and the log-only count's stat memo.
#   B. git itself: `--no-optional-locks status` leaves a stale index alone,
#      where plain `status` rewrites it. This is the behaviour runGit now relies
#      on to stop the .git/index churn fseventsd was reporting.
#   C. End to end: the worktree's endless-go runs `session-status --monitor` in
#      a pane on a private tmux server whose socket directory also holds 40
#      dead sockets, with every job seeded not-due, under the
#      ENDLESS_MONITOR_PROFILE diagnostic. Its idle ticks must cost a handful of
#      tmux execs (not two per dead socket) and no job-scheduling writes.
#
# Everything runs against throwaway state: a temp git repo, a temp --db-dir
# database, and a tmux server under a temp TMUX_TMPDIR. All removed on exit.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(cd "${ENDLESS_VERIFY_DIR}/../../.." && pwd)"
BIN="${WT}/bin/endless-go"
SCRATCH=""
SERVER_STARTED=""

cleanup() {
    if [[ -n "${SERVER_STARTED}" ]]; then
        TMUX_TMPDIR="${SCRATCH}/tmx" tmux kill-server 2>/dev/null
    fi
    [[ -n "${SCRATCH}" ]] && rm -rf "${SCRATCH}"
    return 0
}
trap cleanup EXIT

for tool in go git tmux sqlite3 python3; do
    command -v "${tool}" >/dev/null 2>&1 || setup_error "${tool} is required"
done
# A short path: a unix socket's whole path must fit in ~104 bytes.
SCRATCH="$(mktemp -d /tmp/e2228v.XXXXXX)" || setup_error "mktemp failed"

# ─────────────────────────────────────────────────────────────────────
section "A — unit tests (fail-fast)"

out="$(cd "${WT}" && go test -count=1 \
    -run 'TestGitArgv|TestFrameMemo|TestSocketAccepts' ./internal/monitor 2>&1 && \
    go test -count=1 -run 'TestRunDue|TestDueJobs' ./internal/jobs 2>&1 && \
    go test -count=1 -run 'TestPaneFit' ./internal/liveview 2>&1 && \
    go test -count=1 -run 'TestLogOnlyCount' ./internal/faults 2>&1)"
rc=$?
if (( rc == 0 )); then
    report_pass "monitor, jobs, liveview and faults unit tests pass"
else
    report_fail "monitor, jobs, liveview and faults unit tests pass" "exit 0" \
        "exit ${rc}: $(printf '%s' "${out}" | tail -20)"
    summary
fi

(cd "${WT}" && just build >/dev/null 2>&1) || setup_error "just build failed"

# ─────────────────────────────────────────────────────────────────────
section "B — git: a read-only status no longer rewrites the index"

repo="${SCRATCH}/repo"
git init -q "${repo}" && git -C "${repo}" -c user.email=v@x -c user.name=v \
    commit -q --allow-empty -m init || setup_error "temp repo"
printf 'x\n' >"${repo}/f"
git -C "${repo}" add f
git -C "${repo}" -c user.email=v@x -c user.name=v commit -q -m f
# Same content, new mtime, and an index older than both: the stat data git
# cached is stale, which is exactly when a plain status refreshes and rewrites.
touch -t 202001010000 "${repo}/.git/index"
touch "${repo}/f"
before="$(stat -f %m "${repo}/.git/index" 2>/dev/null || stat -c %Y "${repo}/.git/index")"
git --no-optional-locks -C "${repo}" status --porcelain >/dev/null
after="$(stat -f %m "${repo}/.git/index" 2>/dev/null || stat -c %Y "${repo}/.git/index")"
assert_eq "--no-optional-locks status leaves .git/index untouched" "${before}" "${after}"

git -C "${repo}" status --porcelain >/dev/null
control="$(stat -f %m "${repo}/.git/index" 2>/dev/null || stat -c %Y "${repo}/.git/index")"
if [[ "${control}" != "${before}" ]]; then
    report_pass "control: plain status does rewrite the stale index"
else
    report_fail "control: plain status does rewrite the stale index" \
        "mtime changes" "unchanged (${control}) — the check above proves nothing"
fi

# ─────────────────────────────────────────────────────────────────────
section "C — an idle monitor among dead tmux sockets"

dbdir="${SCRATCH}/db"
mkdir -p "${dbdir}"
"${BIN}" --db-dir "${dbdir}" session-status --task 999999 >/dev/null 2>&1
[[ -f "${dbdir}/endless.db" ]] || setup_error "throwaway database was not created"
{
    printf "INSERT INTO projects (id,name,path) VALUES (1,'e2228probe','%s/proj');\n" "${SCRATCH}"
    printf "INSERT INTO tasks (id,project_id,title,status,phase,type_id) VALUES
        (100,1,'probe focal','underway','now',(SELECT id FROM task_types WHERE slug='todo'));\n"
    printf "INSERT INTO sessions (id,session_id,project_id,task_id) VALUES (7,'probe-session',1,100);\n"
    for j in auto-spawn db-backup doc-mirrors fault-triage main-sync minimizer-loop \
             prime rater worktree-paths worktree-unlanded; do
        printf "INSERT INTO jobs (name,next_due_at,created_at,updated_at) VALUES
            ('%s','2999-01-01T00:00:00','2000-01-01T00:00:00','2000-01-01T00:00:00');\n" "${j}"
    done
} | sqlite3 "${dbdir}/endless.db" || setup_error "seeding the throwaway database"

export TMUX_TMPDIR="${SCRATCH}/tmx"
mkdir -p "${TMUX_TMPDIR}"
unset TMUX TMUX_PANE
tmux -f /dev/null new-session -d -s probe -x 160 -y 40 sh || setup_error "tmux server"
SERVER_STARTED=1
sockdir="$(dirname "$(tmux display-message -p '#{socket_path}')")"
for i in $(seq 1 40); do
    python3 -I -c 'import socket,sys; socket.socket(socket.AF_UNIX).bind(sys.argv[1])' \
        "${sockdir}/dead-${i}" || setup_error "planting a dead socket"
done

prof="${SCRATCH}/prof"
tmux split-window -v -t probe -- env ENDLESS_MONITOR_PROFILE="${prof}" \
    "${BIN}" --db-dir "${dbdir}" session-status --monitor --task 100 --session 7 \
    || setup_error "starting the monitor"
sleep 9
pid="$(pgrep -f "db-dir ${dbdir} session-status --monitor" | head -1)"
[[ -n "${pid}" ]] || setup_error "the monitor is not running"
kill -INT "${pid}"
for _ in $(seq 1 20); do kill -0 "${pid}" 2>/dev/null || break; sleep 0.5; done

ticks="$(cat "${prof}/ticks.log" 2>/dev/null)"
n="$(printf '%s\n' "${ticks}" | grep -c '^tick=')"
if (( n >= 4 )); then
    report_pass "the diagnostic wrote one line per tick (${n} ticks)"
else
    report_fail "the diagnostic wrote one line per tick" ">= 4 tick lines" "${ticks:-none}"
fi
if [[ -s "${prof}/cpu.pprof" ]]; then
    report_pass "the diagnostic flushed a CPU profile on Ctrl-C"
else
    report_fail "the diagnostic flushed a CPU profile on Ctrl-C" "non-empty cpu.pprof" "empty or missing"
fi

# Idle ticks: everything after the first two, which create the frame and fit
# the pane.
idle="$(printf '%s\n' "${ticks}" | tail -n +3)"
max_tmux="$(printf '%s\n' "${idle}" | sed -n 's/.* tmux=\([0-9]*\).*/\1/p' | sort -n | tail -1)"
if [[ -n "${max_tmux}" ]] && (( max_tmux <= 5 )); then
    report_pass "an idle tick costs ${max_tmux} tmux execs among 40 dead sockets (was 2 per socket)"
else
    report_fail "an idle tick costs a handful of tmux execs among 40 dead sockets" \
        "<= 5" "${max_tmux:-none}: ${idle}"
fi
writes="$(printf '%s\n' "${ticks}" | sed -n 's/.* job_writes=\([0-9]*\).*/\1/p' | sort -n | tail -1)"
assert_eq "no job-scheduling writes while nothing is due" "0" "${writes:-none}"

summary
