#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2106 and records what was true when E-2106
# landed. Edit it only if you ARE E-2106. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2106 verification — a resume whose Claude transcript is gone is refused,
# and the user is routed.
#
# Before: Endless resolved a resume target out of `sessions.session_id` and
# handed the UUID to `claude --resume` without ever checking the transcript was
# still on disk. `session goto <ref> --resume` therefore APPEARED to do nothing
# — `tmux new-window` succeeded, so Endless reported `(new window)` and returned
# a pane id, and the claude inside then exited on the missing transcript and
# took the window down with it. `session resume` surfaced only Claude's own
# error. Neither named a route anywhere.
#
# After: both verbs stat the transcript first and refuse with a route;
# `--new-transcript` is the deliberate give-up route on both; both build the
# same pane layout `task spawn` builds; and `task show` marks a session whose
# transcript has left the disk.
#
# Sections 2 onward run against a REAL tmux server on a private socket, so
# "no window was created" is counted from tmux rather than asserted from a
# mock — which is the whole point, since the defect was a window that really
# did get created.
#
#   endless task verify E-2106
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

command -v tmux >/dev/null 2>&1 || setup_error "tmux is not installed"

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
cleanup() {
    local sock
    for sock in "${TMP}"/*.sock; do
        [[ -S "${sock}" ]] && tmux -S "${sock}" kill-server 2>/dev/null
    done
    rm -rf "${TMP}"
}
trap cleanup EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
# The durable coverage for this task, plus every suite whose fixtures this
# change moved: the resume verbs' own tests, the claim output, and the
# `Touched by:` block. A failure here makes everything below meaningless, so
# the suite stops rather than reporting a cascade.
section "1. Unit gate (fail fast)"

if uv run pytest -q \
        tests/test_transcript_gone.py \
        tests/test_claim_launches_a_session.py \
        tests/test_task_claim_worktree.py \
        tests/test_task_show_sessions.py \
        tests/test_session_resume_window_options.py \
        tests/test_session_resume_clobber_gate.py \
        tests/test_session_resume_recover.py \
        tests/test_session_resume_taskless.py \
        tests/test_session_goto_back.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest — transcript gate, claim outcome, resume/goto regression"
else
    report_fail "pytest" "exit 0" "$(tail -30 "${TMP}/py.log")"
    summary
fi

# The layout builder moved out of spawn_window.go so three verbs could share it.
# Its geometry and split ordering are pinned in Go; if that moved, the parity
# every caller below depends on is comparing against something else.
if go test ./internal/spawnlaunchcmd/ >"${TMP}/go.log" 2>&1; then
    report_pass "go test ./internal/spawnlaunchcmd/ (the shared layout builder)"
else
    report_fail "go test ./internal/spawnlaunchcmd/" "exit 0" \
        "$(tail -25 "${TMP}/go.log")"
    summary
fi

# ── shared fixtures ─────────────────────────────────────────────────────────

# The layout is built by `endless-go spawn-layout`, resolved off PATH here:
# the verify runner gives the suite a temp HOME with no sandbox context, so
# `_resolve_endless_go` falls through to PATH and would otherwise find the
# machine's global binary — main's build, not this change's. Same reason
# tests/conftest.py prepends this directory.
go build -o "${WT}/bin/endless-go" ./cmd/endless-go \
    || setup_error "could not build endless-go"

# A private Claude transcript home. Empty to begin with, which is the
# situation this whole task is about.
CLAUDE_HOME="${TMP}/claude-home"
mkdir -p "${CLAUDE_HOME}/projects"
export CLAUDE_CONFIG_DIR="${CLAUDE_HOME}"

# A fake `claude` that just sleeps, so a window opened to run it stays alive
# long enough to be counted, and a fake `endless` so the layout's monitor pane
# does too (the real one would exit against this suite's temp config, and a
# pane whose command exits closes — which would make a pane count say the
# layout failed when it had not).
printf '#!/bin/sh\nexec sleep 120\n' >"${TMP}/claude"
printf '#!/bin/sh\nexec sleep 120\n' >"${TMP}/endless"
chmod +x "${TMP}/claude" "${TMP}/endless"
mkdir -p "${TMP}/wt"

cat >"${TMP}/target.py" <<'PY'
TARGET = {
    "endless_id": 4242, "session_id": "uuid-for-e2106", "task_id": 2106,
    "project_id": 7, "worktree_path": None, "state": "ended",
    "task_type": "todo", "task_status": "underway",
    "task_title": "refuse a gone transcript", "landed_sha": "",
}
PY

# drive_resume.py — `session resume` to the exec, with ONLY the exec and the
# target lookup replaced. tmux, the filesystem and the transcript check are
# real. Prints the argv on exec, "REFUSED: <msg>" short of it.
cat >"${TMP}/drive_resume.py" <<'PY'
import sys
sys.path.insert(0, sys.argv[3])
from target import TARGET
from endless import session_cmd

TARGET["worktree_path"] = sys.argv[1]
new_transcript = len(sys.argv) > 4 and sys.argv[4] == "NEW"


class Exec(Exception):
    pass


session_cmd._resume_target = lambda ref: TARGET
session_cmd._current_pane_task = lambda: None
session_cmd._require_claude = lambda: sys.argv[2]
session_cmd.os.execvp = lambda file, argv: (_ for _ in ()).throw(Exec(argv))
try:
    session_cmd.resume_session("E-2106", new_transcript=new_transcript)
except Exec as e:
    print(" ".join(e.args[0]))
    sys.exit(0)
except Exception as e:
    print("REFUSED: " + " ".join(str(e).split()))
    sys.exit(3)
print("NO-EXEC")
sys.exit(1)
PY

# drive_goto.py — `session goto --resume`'s new-window path. Real tmux, real
# new-window; only the target lookup and the claude binary are replaced.
cat >"${TMP}/drive_goto.py" <<'PY'
import sys
sys.path.insert(0, sys.argv[3])
from target import TARGET
from endless import session_cmd

TARGET["worktree_path"] = sys.argv[1]
new_transcript = len(sys.argv) > 4 and sys.argv[4] == "NEW"
session_cmd._resume_target = lambda ref: TARGET
session_cmd._require_claude = lambda: sys.argv[2]
try:
    pane, label = session_cmd._resume_new_window_pane(
        "E-2106", new_transcript=new_transcript
    )
except Exception as e:
    print("REFUSED: " + " ".join(str(e).split()))
    sys.exit(3)
print(pane)
PY

# windows/panes <socket> — how many windows the server holds, and how many
# panes its newest window holds. Counted from tmux, not inferred.
windows() { tmux -S "$1" list-windows -a -F '#{window_id}' 2>/dev/null | wc -l | tr -d ' '; }
panes()   { tmux -S "$1" list-panes -t "$2" -F '#{pane_id}' 2>/dev/null | wc -l | tr -d ' '; }
opt()     { tmux -S "$1" display-message -p -t "$2" "#{@endless_$3}" 2>/dev/null; }

# ── 2. locating a transcript ────────────────────────────────────────────────
# The lookup globs every project directory rather than deriving the slug from
# the worktree: a session that ran `/cd` is filed under the slug of wherever it
# ENDED. Deriving reported a confident, false "missing" for exactly that case,
# observed live while recovering E-1934.
section "2. A transcript is found wherever it was filed"

cat >"${TMP}/locate.py" <<'PY'
import sys
from endless import session_cmd
found = session_cmd.transcript_path(sys.argv[1])
print("NONE" if found is None else found.name)
PY

assert_eq "an absent transcript reads as absent" \
    "NONE" "$(uv run python "${TMP}/locate.py" uuid-for-e2106)"

# File it under a slug that has nothing to do with the task's worktree — the
# case slug-derivation gets wrong.
mkdir -p "${CLAUDE_HOME}/projects/-Users-someone-somewhere-else"
printf '{}\n' >"${CLAUDE_HOME}/projects/-Users-someone-somewhere-else/uuid-for-e2106.jsonl"

assert_eq "found under a project slug the worktree could not predict" \
    "uuid-for-e2106.jsonl" "$(uv run python "${TMP}/locate.py" uuid-for-e2106)"

# Back to gone for the sections that are about the loss.
rm -rf "${CLAUDE_HOME}/projects/-Users-someone-somewhere-else"

# ── 3. session resume refuses, and reaches no exec ──────────────────────────
section "3. session resume refuses a gone transcript"

sock="${TMP}/resume.sock"
pane="$(tmux -S "${sock}" new-session -d -s p -P -F '#{pane_id}' 'sleep 120')" \
    || setup_error "could not start a tmux server on ${sock}"

out="$(env TMUX="${sock},1,0" TMUX_PANE="${pane}" PATH="${TMP}:${WT}/bin:${PATH}" \
    uv run python "${TMP}/drive_resume.py" "${TMP}/wt" "${TMP}/claude" "${TMP}" \
    2>"${TMP}/r1.err" | tail -1)"

assert_contains "it refuses instead of exec'ing claude" "REFUSED" "${out}"
assert_contains "the refusal names the transcript file it looked for" \
    "uuid-for-e2106.jsonl" "${out}"
assert_contains "…and says the loss may still be recoverable" "backup" "${out}"
assert_contains "…and names the give-up route" "--new-transcript" "${out}"
assert_eq "the pane's identity is left exactly as found — nothing was bound" \
    "" "$(opt "${sock}" "${pane}" task_id)"
assert_eq "the window was not laid out around a session that never started" \
    "1" "$(panes "${sock}" "${pane}")"

# ── 4. goto --resume creates NO window ──────────────────────────────────────
# The defect this task was filed for. `tmux new-window` succeeded, so the
# caller was told `(new window)` and handed a pane id — while the window died
# with the claude inside it and nothing reached the user at all.
section "4. session goto --resume opens no window it cannot fill"

before="$(windows "${sock}")"
out="$(env TMUX="${sock},1,0" TMUX_PANE="${pane}" PATH="${TMP}:${WT}/bin:${PATH}" \
    uv run python "${TMP}/drive_goto.py" "${TMP}/wt" "${TMP}/claude" "${TMP}" \
    2>"${TMP}/g1.err" | tail -1)"

assert_contains "it refuses" "REFUSED" "${out}"
assert_eq "no window was created (counted from tmux, not from the return value)" \
    "${before}" "$(windows "${sock}")"

# ── 5. --new-transcript is the way on, from both verbs ──────────────────────
section "5. --new-transcript starts a fresh session instead"

out="$(env TMUX="${sock},1,0" TMUX_PANE="${pane}" PATH="${TMP}:${WT}/bin:${PATH}" \
    uv run python "${TMP}/drive_resume.py" "${TMP}/wt" "${TMP}/claude" \
    "${TMP}" NEW 2>"${TMP}/r2.err" | tail -1)"

assert_eq "session resume launches a plain claude — no --resume" \
    "claude" "${out}"
assert_eq "the pane is bound to the task, so SessionStart can attach the new session" \
    "2106" "$(opt "${sock}" "${pane}" task_id)"
assert_eq "the DEAD session's uuid is not republished as the window's identity" \
    "" "$(opt "${sock}" "${pane}" session_uuid)"

# A second server, so the window count starts from a known place.
sock2="${TMP}/goto.sock"
pane2="$(tmux -S "${sock2}" new-session -d -s g -P -F '#{pane_id}' 'sleep 120')" \
    || setup_error "could not start a tmux server on ${sock2}"

newpane="$(env TMUX="${sock2},1,0" TMUX_PANE="${pane2}" PATH="${TMP}:${WT}/bin:${PATH}" \
    uv run python "${TMP}/drive_goto.py" "${TMP}/wt" "${TMP}/claude" \
    "${TMP}" NEW 2>"${TMP}/g2.err" | tail -1)"

assert_contains "session goto --resume returns a real pane id" "%" "${newpane}"
assert_eq "…in a window that was actually created" "2" "$(windows "${sock2}")"
assert_eq "…carrying the task, and not the dead session's uuid" \
    "" "$(opt "${sock2}" "${newpane}" session_uuid)"
assert_eq "…and the task it IS for" "2106" "$(opt "${sock2}" "${newpane}" task_id)"

# ── 6. the window is laid out, by the builder task spawn uses ───────────────
# Three panes: claude, a bare shell, and `session monitor`. The count is read
# from the real server, so it also proves the Go `spawn-layout` verb is wired
# and reachable from Python.
section "6. Both verbs build spawn's layout"

assert_eq "goto --resume's new window holds the full three-pane layout" \
    "3" "$(panes "${sock2}" "${newpane}")"

# ── 7. resume refuses a populated window ────────────────────────────────────
# Resume execs in place and lays the window out around the pane it took over,
# which is only coherent in a window that holds that pane alone.
section "7. session resume refuses a window it would rearrange"

mkdir -p "${CLAUDE_HOME}/projects/-present"
printf '{}\n' >"${CLAUDE_HOME}/projects/-present/uuid-for-e2106.jsonl"

sock3="${TMP}/crowded.sock"
pane3="$(tmux -S "${sock3}" new-session -d -s c -P -F '#{pane_id}' 'sleep 120')" \
    || setup_error "could not start a tmux server on ${sock3}"
tmux -S "${sock3}" split-window -t "${pane3}" -d 'sleep 120' \
    || setup_error "could not split the crowded window"

out="$(env TMUX="${sock3},1,0" TMUX_PANE="${pane3}" PATH="${TMP}:${WT}/bin:${PATH}" \
    uv run python "${TMP}/drive_resume.py" "${TMP}/wt" "${TMP}/claude" "${TMP}" \
    2>"${TMP}/r3.err" | tail -1)"

assert_contains "a 2-pane window is refused even with the transcript present" \
    "REFUSED" "${out}"
assert_contains "…and the refusal names the verb that opens a new window" \
    "goto E-2106 --resume" "${out}"
assert_eq "the panes the user arranged are untouched" \
    "2" "$(panes "${sock3}" "${pane3}")"

# And the same window with one pane proceeds, so section 7 is a gate on the
# pane count rather than on the transcript still being there.
sock4="${TMP}/lone.sock"
pane4="$(tmux -S "${sock4}" new-session -d -s l -P -F '#{pane_id}' 'sleep 120')" \
    || setup_error "could not start a tmux server on ${sock4}"
out="$(env TMUX="${sock4},1,0" TMUX_PANE="${pane4}" PATH="${TMP}:${WT}/bin:${PATH}" \
    uv run python "${TMP}/drive_resume.py" "${TMP}/wt" "${TMP}/claude" "${TMP}" \
    2>"${TMP}/r4.err" | tail -1)"

assert_eq "a lone-pane window resumes for real" \
    "claude --resume uuid-for-e2106" "${out}"
assert_eq "…and is laid out around the pane it took over" \
    "3" "$(panes "${sock4}" "${pane4}")"

# ── 8. task show marks the loss where the task is read ──────────────────────
section "8. task show marks a session whose transcript is gone"

cat >"${TMP}/show.py" <<'PY'
import sys
from endless import task_cmd
touch = {"session_id": 4242, "task_id": None, "rel_label": "Claimed",
         "rel_slug": "claimed", "state": "ended", "uuid": sys.argv[1]}
print("GONE" if task_cmd._transcript_gone(touch) else "PRESENT")
PY

assert_eq "a session whose transcript survives is not marked" \
    "PRESENT" "$(uv run python "${TMP}/show.py" uuid-for-e2106)"
assert_eq "a session whose transcript is gone is" \
    "GONE" "$(uv run python "${TMP}/show.py" uuid-that-never-existed)"
assert_eq "a touch with no uuid at all is not reported as a loss" \
    "PRESENT" "$(uv run python "${TMP}/show.py" '')"

# ── 9. the claim block that sent people nowhere ─────────────────────────────
# Its first option was `task spawn`, refused for any task that has ever been
# claimed — which a re-claim always has — and its second taught `eswt`, a shell
# helper `endless shell-init` has never defined.
section "9. task claim ends with one next step, not a broken menu"

if uv run endless task claim --help >"${TMP}/claimhelp.log" 2>&1; then
    report_pass "task claim --help still renders"
else
    report_fail "task claim --help" "exit 0" "$(tail -10 "${TMP}/claimhelp.log")"
fi

# A shell claim starts Claude through the SAME `spawn-window` seam `task spawn`
# uses — in a window of its own, so the shell the claim was typed in survives.
# An earlier revision exec'd Claude over that pane and split the window around
# it, which cost the caller their shell and forced a refusal for any window
# holding another pane.
cat >"${TMP}/claim_launch.py" <<'PY_LAUNCH'
import sys, types
from endless import task_cmd, event_bridge
calls = []
task_cmd._claude_binary = lambda: "/bin/claude"
task_cmd._current_endless_session_id = lambda: None
event_bridge._resolve_endless_go = lambda *a, **kw: "/bin/endless-go"
# Rebind the NAME in task_cmd, rather than mutating the shared subprocess
# module: everything else that shells out (the status vocabulary, for one) goes
# through the same module object and would be stubbed along with it.
task_cmd.subprocess = types.SimpleNamespace(
    run=lambda argv, **kw: calls.append(list(argv))
)
task_cmd.shutil = types.SimpleNamespace(which=lambda name: "/bin/claude")
task_cmd.os.execvp = lambda *a: sys.exit("EXECED OVER THE CALLER'S SHELL")
task_cmd.os.chdir = lambda p: sys.exit("MOVED THE CALLER'S SHELL")
task_cmd._launch_claude_for_claim(2106, 7, "/tmp/wt")
argv = calls[0]
handoff = argv[argv.index("--handoff-file") + 1]
with open(handoff) as f:
    body = f.read()
print(" ".join([argv[1], argv[argv.index("--task-id") + 1],
                argv[argv.index("--cwd") + 1],
                "HANDOFF-EMPTY" if body == "" else "HANDOFF-PRESENT",
                "SPAWN-MARKER" if argv[argv.index("--spawned-by") + 1] else "NO-MARKER"]))
PY_LAUNCH

launch="$(env TMUX="fake,1,0" uv run python "${TMP}/claim_launch.py" 2>&1 | tail -1)"
assert_eq "claim launches through spawn-window, with the task, worktree, no handoff and a spawn marker" \
    "spawn-window 2106 /tmp/wt HANDOFF-EMPTY SPAWN-MARKER" "${launch}"

cat >"${TMP}/tmux_gate.py" <<'PY_GATE'
import sys
from endless import task_cmd
try:
    task_cmd._require_tmux_for_claim(2106)
except Exception as e:
    print("REFUSED: " + " ".join(str(e).split()))
    sys.exit(3)
print("ALLOWED")
PY_GATE

assert_eq "in tmux, a claim from a shell is allowed whatever else the window holds" \
    "ALLOWED" "$(env TMUX="fake,1,0" uv run python "${TMP}/tmux_gate.py")"

gate="$(env -u TMUX uv run python "${TMP}/tmux_gate.py")"
assert_contains "outside tmux it is refused — there is nowhere to start a session" \
    "no tmux" "${gate}"
assert_contains "…routed to the flag for working it by hand" "--unattended" "${gate}"

# Grepped as CODE, not as prose: this change's own comments explain what it
# removed and name both strings.
if grep -q "^def _eswt_defined_in_user_shell" src/endless/task_cmd.py; then
    report_fail "the eswt shell probe is gone" "absent" "still defined"
else
    report_pass "the eswt shell probe is gone — it probed for a helper shell-init never defined"
fi

if grep -qE '^\s*click\.echo\(.*(choose one|eswt)' src/endless/task_cmd.py; then
    report_fail "nothing prints the choose-one menu" "no such echo" \
        "$(grep -nE '^\s*click\.echo\(.*(choose one|eswt)' src/endless/task_cmd.py)"
else
    report_pass "nothing prints the choose-one menu, or the eswt line under it"
fi

# ── 10. both flags are reachable from the CLI ───────────────────────────────
section "10. --new-transcript is on both verbs"

assert_contains "session resume offers it" "--new-transcript" \
    "$(uv run endless session resume --help 2>&1)"
assert_contains "session goto offers it too — the window choice and the give-up choice are separate" \
    "--new-transcript" "$(uv run endless session goto --help 2>&1)"

summary
