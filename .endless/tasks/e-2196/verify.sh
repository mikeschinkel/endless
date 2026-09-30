#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2196 and records what was true when E-2196
# landed. Edit it only if you ARE E-2196. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2196 verification — one command resumes every task window a tmux crash
# restored:
#
#     endless session resume --tmux-session NAME   (or --all-tmux-sessions)
#
# Per window named E-NNNN (optionally `*`-marked): skip it if a pane already
# holds a live session; otherwise keep one pane at a shell prompt (the one in
# the task's worktree when there is one), kill the rest, and type
# `cd <project root> && endless session resume E-NNNN --rebind` into it —
# with `--review` when the task's worktree was dropped after landing.
#
# And the single-window wrong-task bug it depends on: after a crash, tmux
# reissues pane ids, and a pre-crash session bound to "%N" on the DEAD server
# was reported by `list-live` with that bare pane id. Python matched it against
# $TMUX_PANE, so resume in a restored pane believed the pane was "working" the
# old session's task and refused. Section 3 reproduces that shape.
#
# Everything below runs against a REAL tmux server on a private socket and a
# REAL SQLite database with the real schema. Only `endless` itself — the
# command typed into each pane — is a stub that records its cwd and argv.
#
#   endless task verify E-2196
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

command -v tmux >/dev/null 2>&1 || setup_error "tmux is not installed"

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
TMP="$(cd "${TMP}" && pwd -P)"
SOCK="${TMP}/restored.sock"
cleanup() {
    local i
    if [[ -S "${SOCK}" ]]; then
        tmux -S "${SOCK}" kill-server 2>/dev/null
        for i in $(seq 1 25); do
            [[ -S "${SOCK}" ]] || break
            sleep 0.2
        done
    fi
    rm -rf "${TMP}"
}
trap cleanup EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
section "1. Unit gate (fail fast)"

if go test ./internal/resumewindowscmd/ >"${TMP}/gounit.log" 2>&1 \
    && go test ./internal/monitor/ -run 'ListLiveSessions' >>"${TMP}/gounit.log" 2>&1; then
    report_pass "go test: resume-windows decisions + list-live server scoping"
else
    report_fail "go test resumewindowscmd / monitor" "exit 0" "$(tail -25 "${TMP}/gounit.log")"
    summary
fi

if uv run pytest -q \
        tests/test_session_resume_tmux_windows.py \
        tests/test_session_resume_crash_recovery.py \
        tests/test_session_resume_clobber_gate.py \
        tests/test_session_resume_window_options.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest: --tmux-session options + resume regression"
else
    report_fail "pytest --tmux-session + resume" "exit 0" "$(tail -25 "${TMP}/py.log")"
    summary
fi

# Built from THIS worktree: an installed binary would prove something about a
# different tree.
mkdir -p "${TMP}/bin" "${TMP}/stub"
if go build -o "${TMP}/bin/endless-go" ./cmd/endless-go >"${TMP}/go.log" 2>&1; then
    report_pass "go build ./cmd/endless-go"
else
    report_fail "go build ./cmd/endless-go" "exit 0" "$(tail -25 "${TMP}/go.log")"
    summary
fi

# The stub `endless` typed into each pane: records where it ran and with what.
LOG="${TMP}/typed.log"
: >"${LOG}"
cat >"${TMP}/stub/endless" <<EOF
#!/bin/sh
printf '%s|%s\n' "\$PWD" "\$*" >>"${LOG}"
EOF
chmod +x "${TMP}/stub/endless"

# ── fixture: the project, and a tmux server shaped like a restore ──────────
CFG="${TMP}/cfg"
PROJ="${TMP}/proj"
mkdir -p "${CFG}" "${PROJ}/.endless/worktrees/e-4001/sub" \
    "${PROJ}/.endless/worktrees/e-4999"

# Plain bash panes, no rc files, with the stub first on PATH: a pane is "at a
# shell prompt" exactly as a restored zsh is, and nothing a developer's rc
# prints can race the keystrokes. PATH is set IN the pane command, not with
# tmux's `-e`: tmux runs the command through $SHELL -c, and a zsh user's
# ~/.zshenv would put the real `endless` back in front of the stub.
SH="env PATH='${TMP}/stub:${PATH}' PS1='$ ' bash --norc --noprofile"

new_window() {  # new_window <name> <cwd> → pane id
    tmux -S "${SOCK}" new-window -d -t r -n "$1" -c "$2" \
        -P -F '#{pane_id}' "${SH}"
}
split() {       # split <pane> <cwd> → pane id
    tmux -S "${SOCK}" split-window -d -t "$1" -c "$2" \
        -P -F '#{pane_id}' "${SH}"
}

# Window 0 is where the "user" is sitting; it must stay the active window.
tmux -S "${SOCK}" -f /dev/null new-session -d -s r -n home -c "${TMP}" \
    "${SH}" || setup_error "could not start tmux on ${SOCK}"
tmux -S "${SOCK}" set-option -g @server_uuid srv-restored >/dev/null \
    || setup_error "could not stamp the server uuid"

# W1 — the common restore: three panes, one in the task's worktree, one in the
# main checkout, one in ANOTHER task's worktree (seen after 2026-09-29).
W1_ROOT="$(new_window 'E-4001' "${PROJ}")" || setup_error "window E-4001"
W1_WT="$(split "${W1_ROOT}" "${PROJ}/.endless/worktrees/e-4001/sub")" || setup_error "split"
W1_OTHER="$(split "${W1_ROOT}" "${PROJ}/.endless/worktrees/e-4999")" || setup_error "split"
# W2 — attention-marked, single pane in the main checkout; its task landed and
# its worktree was reaped.
W2="$(new_window ' *E-4002' "${PROJ}")" || setup_error "window *E-4002"
# W3 — not a task window.
W3="$(new_window 'cat' "${TMP}")" || setup_error "window cat"
W3B="$(split "${W3}" "${TMP}")" || setup_error "split"
# W4 — already running Claude: one of its panes holds a live session.
W4="$(new_window 'E-4003' "${PROJ}")" || setup_error "window E-4003"
W4B="$(split "${W4}" "${PROJ}")" || setup_error "split"
# W5 — a task with no resumable session at all.
W5="$(new_window 'E-4004' "${PROJ}")" || setup_error "window E-4004"
W5B="$(split "${W5}" "${PROJ}")" || setup_error "split"
sleep 0.5

cat >"${TMP}/seed.py" <<'PY'
import os
from pathlib import Path
from endless import config
config.set_db_context(Path(os.environ["E2196_CFG"]))
from endless import db

e = os.environ
db.execute("INSERT INTO projects (id, name, path) VALUES (1, 'e2196', ?)", (e["E2196_PROJ"],))
for tid, status in ((4001, "underway"), (4002, "confirmed"), (4003, "underway"),
                    (4004, "underway"), (4999, "underway")):
    db.execute("INSERT INTO tasks (id, project_id, title, status) VALUES (?, 1, ?, ?)",
               (tid, f"task {tid}", status))

def pane(server, address):
    db.execute("INSERT OR IGNORE INTO processes (kind_id, server_uuid, address) VALUES (1, ?, ?)",
               (server, address))
    return db.query("SELECT id FROM processes WHERE kind_id = 1 AND server_uuid = ? AND address = ?",
                    (server, address))[0]["id"]

for pk, uuid, state, task, proc in (
    (701, "uuid-4001", "ended", 4001, None),
    (702, "uuid-4002", "ended", 4002, None),
    # Live NOW, on this server, in W4's second pane.
    (703, "uuid-4003", "working", 4003, pane("srv-restored", e["E2196_W4B"])),
    # From BEFORE the crash: bound to the very pane id tmux has since reissued
    # to W1's worktree pane, on a server that no longer exists. Still 'working'
    # — it never got to say it ended.
    (799, "uuid-4999", "working", 4999, pane("srv-before-crash", e["E2196_W1_WT"])),
):
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, platform, state, "
        "started_at, last_activity, task_id, process_id) "
        "VALUES (?, ?, 1, 'claude', ?, '2026-09-01T00:00:00', '2026-09-01T00:00:00', ?, ?)",
        (pk, uuid, state, task, proc))
PY
E2196_CFG="${CFG}" E2196_PROJ="${PROJ}" E2196_W4B="${W4B}" E2196_W1_WT="${W1_WT}" \
    uv run python "${TMP}/seed.py" >"${TMP}/seed.log" 2>&1 \
    || setup_error "could not seed the fixture database: $(tail -5 "${TMP}/seed.log")"

# Every bare `tmux` the code under test runs talks to the fixture server.
T=(env -u TMUX_PANE -u ENDLESS_SESSION_ID -u CLAUDECODE -u CLAUDE_CODE_SESSION_ID
   TMUX="${SOCK},1,0" PATH="${TMP}/bin:${PATH}")
sweep() { "${T[@]}" endless-go --db-dir "${CFG}" resume-windows "$@" 2>&1; }
panes_of() { tmux -S "${SOCK}" list-panes -t "$1" -F '#{pane_id}' | tr '\n' ' ' | sed 's/ $//'; }
active_window() { tmux -S "${SOCK}" display-message -p -t r '#{window_name}'; }

# ── 2. the fixture is what a restore leaves ────────────────────────────────
section "2. The restored server"
assert_eq "E-4001 came back with three panes" 3 "$(wc -w <<<"$(panes_of "${W1_ROOT}")" | tr -d ' ')"
assert_eq "the user is sitting in window 'home'" "home" "$(active_window)"

# ── 3. the wrong-task bug: a reissued pane id is not the old session's ─────
section "3. Pre-crash session vs. a reissued pane id"

LIVE="$("${T[@]}" endless-go --db-dir "${CFG}" session-query list-live --project-root "${PROJ}" 2>/dev/null)"
pane_for() {  # pane_for <claude uuid> → its reported pane_id, or null
    python3 -c 'import json,sys; rows=json.load(sys.stdin)["rows"]
print(next((str(r["pane_id"]) for r in rows if r["session_id"]==sys.argv[1]), "absent"))' "$1" <<<"${LIVE}"
}
assert_eq "the pre-crash session is still listed as an owner, with NO pane id" \
    "None" "$(pane_for uuid-4999)"
assert_eq "the live session on this server keeps its pane id" \
    "${W4B}" "$(pane_for uuid-4003)"

cat >"${TMP}/whoami.py" <<'PY'
import os
from pathlib import Path
from endless import config
config.set_db_context(Path(os.environ["E2196_CFG"]))
from endless.task_cmd import _current_endless_session_id
print(_current_endless_session_id())
PY
WHO="$(cd "${PROJ}" && "${T[@]}" TMUX_PANE="${W1_WT}" E2196_CFG="${CFG}" \
    uv run --project "${WT}" python "${TMP}/whoami.py" 2>/dev/null | tail -1)"
assert_eq "a restored pane holding a reissued id resolves to NO session (was ES-799, E-4999)" \
    "None" "${WHO}"

# ── 4. dry run changes nothing ─────────────────────────────────────────────
section "4. --dry-run"

BEFORE="$(tmux -S "${SOCK}" list-panes -s -t r -F '#{pane_id}' | tr '\n' ' ')"
OUT="$(sweep --tmux-session r --dry-run)"
assert_contains "dry run plans E-4001 in its worktree pane" \
    "would resume in ${W1_WT} (closing ${W1_ROOT} ${W1_OTHER})" "${OUT}"
assert_contains "dry run shows the typed line" \
    "cd '${PROJ}' && endless session resume E-4001 --rebind" "${OUT}"
assert_eq "no pane was closed" "${BEFORE}" \
    "$(tmux -S "${SOCK}" list-panes -s -t r -F '#{pane_id}' | tr '\n' ' ')"
sleep 0.5
assert_eq "nothing was typed" "" "$(cat "${LOG}")"

# A second client attached to a session makes a GROUPED twin (`active` and
# `active-6` on the machine this was built on): same windows, second name.
# Either name must reach them.
tmux -S "${SOCK}" new-session -d -t r -s r-6 || setup_error "could not group r-6 with r"
OUT6="$(sweep --tmux-session r-6 --dry-run)"
assert_contains "the grouped twin's name finds the same windows" \
    "E-4001     r-6:1        would resume in ${W1_WT}" "${OUT6}"

# ── 5. the sweep ───────────────────────────────────────────────────────────
section "5. --tmux-session r"

OUT="$(sweep --tmux-session r)"
for i in $(seq 1 25); do
    [[ "$(wc -l <"${LOG}" | tr -d ' ')" -ge 3 ]] && break
    sleep 0.2
done
TYPED="$(cat "${LOG}")"

assert_eq "E-4001: only the worktree pane is left" "${W1_WT}" "$(panes_of "${W1_WT}")"
assert_contains "E-4001: resume ran from the project root with --rebind" \
    "${PROJ}|session resume E-4001 --rebind" "${TYPED}"
assert_not_contains "E-4001: no --review while its worktree exists" \
    "session resume E-4001 --rebind --review" "${TYPED}"

assert_eq "*E-4002: its single pane is kept" "${W2}" "$(panes_of "${W2}")"
assert_contains "*E-4002: the reaped worktree is rebuilt with --review" \
    "${PROJ}|session resume E-4002 --rebind --review" "${TYPED}"

assert_eq "cat: not a task window, untouched" "${W3} ${W3B}" "$(panes_of "${W3}")"
assert_contains "cat: summary says why" "skipped: not named for a task" "${OUT}"

assert_eq "E-4003: already running Claude, untouched" "${W4} ${W4B}" "$(panes_of "${W4}")"
assert_contains "E-4003: summary names the live session" "already running Claude (ES-703)" "${OUT}"
assert_not_contains "E-4003: nothing typed" "E-4003" "${TYPED}"

assert_eq "E-4004: cleared to one pane anyway" "${W5}" "$(panes_of "${W5}")"
assert_contains "E-4004: resume is still typed, so its error shows in that window" \
    "${PROJ}|session resume E-4004 --rebind" "${TYPED}"
assert_contains "E-4004: the summary names the failure too" \
    "no resumable Claude session found for task E-4004" "${OUT}"

assert_contains "summary counts (home, cat, E-4003 skipped)" "3 dispatched, 3 skipped." "${OUT}"
assert_eq "focus did not move: the user is still in 'home'" "home" "$(active_window)"

summary
