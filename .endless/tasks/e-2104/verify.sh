#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2104 and records what was true when E-2104
# landed. Edit it only if you ARE E-2104. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2104 verification — a resumed window carries the identity of the session
# that is actually in it.
#
# Before: `session resume` exec'd `claude --resume` in the current pane and set
# no tmux window options at all, and `session goto --resume` opened a new window
# and set none either. So a resumed window's `@endless_*` identity was whatever
# it happened to hold — a DIFFERENT task's, when resume landed in a spawned
# window, or nothing, when it landed in a plain recovery shell.
#
# After: both paths write `@endless_task_id`, `@endless_project_id` and
# `@endless_session_uuid` from the resolved target, and leave
# `@endless_spawned_by` alone (it records who CREATED the window; resume
# creates none).
#
# Nothing here is stubbed below section 1: the drives run against a REAL tmux
# server on a private socket, the options are read back with real
# `display-message`, and the consumer that the clobber gate depends on is the
# real one.
#
#   endless task verify E-2104
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
# The durable coverage: the re-bind itself, plus the resolver and gate tests it
# threads `project_id` through, plus the goto/back suite that owns the other
# caller of `_tmux_run`. A failure here makes everything below meaningless, so
# the suite stops rather than reporting a cascade.
section "1. Unit gate (fail fast)"

if uv run pytest -q \
        tests/test_session_resume_window_options.py \
        tests/test_session_resume_recover.py \
        tests/test_session_resume_taskless.py \
        tests/test_session_resume_clobber_gate.py \
        tests/test_session_goto_back.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest resume window options + resume/goto regression"
else
    report_fail "pytest resume/goto" "exit 0" "$(tail -25 "${TMP}/py.log")"
    summary
fi

# Spawn is the source of truth for WHICH options a task's window carries; this
# suite asserts resume matches it. If spawn's own argv test breaks, the parity
# claim below is comparing against something that moved.
if go test ./internal/spawnlaunchcmd/ >"${TMP}/go.log" 2>&1; then
    report_pass "go test ./internal/spawnlaunchcmd/ (spawn's option argv)"
else
    report_fail "go test ./internal/spawnlaunchcmd/" "exit 0" \
        "$(tail -25 "${TMP}/go.log")"
    summary
fi

# ── shared fixtures for the real-tmux drives ────────────────────────────────
# A fake `claude` that just sleeps, so a window opened to run it stays alive
# long enough for its options to be read back.
printf '#!/bin/sh\nexec sleep 120\n' >"${TMP}/claude" || setup_error "no temp dir"
chmod +x "${TMP}/claude"
mkdir -p "${TMP}/wt"

# The resolved target both drives resume, written once. project_id 7 and task
# 2104 are arbitrary but distinct from anything a pane could already hold.
cat >"${TMP}/target.py" <<'PY'
TARGET = {
    "endless_id": 4242, "session_id": "uuid-for-e2104", "task_id": 2104,
    "project_id": 7, "worktree_path": None, "state": "ended",
    "task_type": "todo", "task_status": "underway",
    "task_title": "resume rebind", "landed_sha": "",
}
PY

# drive_resume.py — `session resume` all the way to the exec, with ONLY the
# exec and the target lookup replaced. tmux is real.
cat >"${TMP}/drive_resume.py" <<'PY'
import sys
sys.path.insert(0, sys.argv[3])
from target import TARGET
from endless import session_cmd

TARGET["worktree_path"] = sys.argv[1]


class Exec(Exception):
    pass


session_cmd._resume_target = lambda ref: TARGET
session_cmd._current_pane_task = lambda: None
if sys.argv[2] == "NOCLAUDE":
    # `_require_claude` resolves through shutil.which; emptying it is how this
    # drive reproduces "claude is not installed" without touching PATH, which
    # `uv run` needs intact to have started this interpreter at all.
    import shutil
    shutil.which = lambda *a, **k: None
else:
    session_cmd._require_claude = lambda: sys.argv[2]


def fake_exec(file, argv):
    raise Exec(argv)


session_cmd.os.execvp = fake_exec
try:
    session_cmd.resume_session("E-2104")
except Exec as e:
    print(" ".join(e.args[0]))
    sys.exit(0)
except Exception as e:                      # a refusal short of the exec
    print(f"REFUSED: {e}")
    sys.exit(3)
print("NO-EXEC")
sys.exit(1)
PY

# drive_goto.py — `session goto --resume`'s new-window path, real tmux, real
# new-window; only the target lookup and the claude binary are replaced.
cat >"${TMP}/drive_goto.py" <<'PY'
import sys
sys.path.insert(0, sys.argv[3])
from target import TARGET
from endless import session_cmd

TARGET["worktree_path"] = sys.argv[1]
session_cmd._resume_target = lambda ref: TARGET
session_cmd._require_claude = lambda: sys.argv[2]
pane, _label = session_cmd._resume_new_window_pane("E-2104")
print(pane)
PY

# opt <socket> <pane> <name> — read one @endless_* window option back.
opt() {
    tmux -S "$1" display-message -p -t "$2" "#{@endless_$3}" 2>/dev/null
}

# ── 2. `session resume`: the current pane gets the resumed session's identity ─
section "2. session resume re-binds the pane it execs in"

sock="${TMP}/resume.sock"
pane="$(tmux -S "${sock}" new-session -d -s p -P -F '#{pane_id}' 'sleep 120')" \
    || setup_error "could not start a tmux server on ${sock}"
# A second window, never resumed into: the pre-fix state, demonstrated rather
# than asserted from memory.
control="$(tmux -S "${sock}" new-window -d -P -F '#{pane_id}' 'sleep 120')" \
    || setup_error "could not open the control window"
# Seed the pane with a DIFFERENT task's identity — a spawned window that resume
# is about to take over. Overwriting this is the defect's sharpest edge: before
# the fix the hook's spawn-bind read 1111 and bound the resumed session to it.
tmux -S "${sock}" set-option -w -t "${pane}" @endless_task_id 1111
tmux -S "${sock}" set-option -w -t "${pane}" @endless_spawned_by 999

argv="$(env TMUX="${sock},1,0" TMUX_PANE="${pane}" \
    uv run python "${TMP}/drive_resume.py" "${TMP}/wt" "${TMP}/claude" "${TMP}" \
    2>"${TMP}/resume.err" | tail -1)"

assert_eq "resume reached the exec it was driven to" \
    "claude --resume uuid-for-e2104" "${argv}"
assert_eq "the pane's task id is the RESUMED task, not the one it held" \
    "2104" "$(opt "${sock}" "${pane}" task_id)"
assert_eq "the pane carries the resumed session's project" \
    "7" "$(opt "${sock}" "${pane}" project_id)"
assert_eq "the pane carries the resumed session's Claude UUID" \
    "uuid-for-e2104" "$(opt "${sock}" "${pane}" session_uuid)"
assert_eq "@endless_spawned_by is left exactly as found — resume spawns nothing" \
    "999" "$(opt "${sock}" "${pane}" spawned_by)"

# The control window is what EVERY resumed window looked like before this task.
assert_eq "an untouched window has no task id at all (the pre-fix state)" \
    "" "$(opt "${sock}" "${control}" task_id)"
assert_eq "…and no session uuid either" \
    "" "$(opt "${sock}" "${control}" session_uuid)"

# ── 3. the consumer the clobber gate actually depends on ────────────────────
# `_current_pane_task` (the E-1968 guard) resolves this pane's session through
# `_tmux_window_session_uuid`. Running the REAL reader against the window resume
# just wrote is what proves the write is useful, not merely present.
section "3. The window option resume wrote is the one the guard reads"

read_uuid="$(env TMUX="${sock},1,0" TMUX_PANE="${pane}" uv run python -c \
    'from endless.task_cmd import _tmux_window_session_uuid as f; print(f() or "")' \
    2>/dev/null | tail -1)"
assert_eq "_tmux_window_session_uuid reads back what resume published" \
    "uuid-for-e2104" "${read_uuid}"

control_uuid="$(env TMUX="${sock},1,0" TMUX_PANE="${control}" uv run python -c \
    'from endless.task_cmd import _tmux_window_session_uuid as f; print(f() or "")' \
    2>/dev/null | tail -1)"
assert_eq "…and reads nothing in a window nobody bound (the pre-fix state)" \
    "" "${control_uuid}"

# ── 4. a resume that cannot launch leaves the pane alone ────────────────────
# The bind is placed after `_require_claude` on purpose: rewriting a pane's
# identity for a session that is not going to start would be a lie the pane
# then keeps.
section "4. A refused resume does not rewrite the pane"

tmux -S "${sock}" set-option -w -t "${control}" @endless_task_id 1111
out="$(env TMUX="${sock},1,0" TMUX_PANE="${control}" \
    uv run python "${TMP}/drive_resume.py" \
        "${TMP}/wt" NOCLAUDE "${TMP}" 2>&1 | tail -1)"
assert_contains "resume refuses when claude is not on PATH" "REFUSED" "${out}"
assert_eq "…and the pane it refused in still holds what it held" \
    "1111" "$(opt "${sock}" "${control}" task_id)"
assert_eq "…and was given no session uuid" \
    "" "$(opt "${sock}" "${control}" session_uuid)"

tmux -S "${sock}" kill-server 2>/dev/null

# ── 5. `session goto --resume`: the window it CREATES gets one too ──────────
# The sibling surface, folded in with E-2104 because it is the same omission:
# a brand-new window starts with no identity whatsoever.
section "5. session goto --resume binds the window it opens"

sock="${TMP}/goto.sock"
tmux -S "${sock}" new-session -d -s p 'sleep 120' \
    || setup_error "could not start a tmux server on ${sock}"

newpane="$(env TMUX="${sock},1,0" \
    uv run python "${TMP}/drive_goto.py" "${TMP}/wt" "${TMP}/claude" "${TMP}" \
    2>"${TMP}/goto.err" | tail -1)"

if [[ "${newpane}" != %* ]]; then
    setup_error "goto --resume did not open a window: $(tail -5 "${TMP}/goto.err")"
fi

assert_eq "the new window's task id is the resumed task" \
    "2104" "$(opt "${sock}" "${newpane}" task_id)"
assert_eq "the new window carries the resumed session's project" \
    "7" "$(opt "${sock}" "${newpane}" project_id)"
assert_eq "the new window carries the resumed session's Claude UUID" \
    "uuid-for-e2104" "$(opt "${sock}" "${newpane}" session_uuid)"
assert_eq "goto --resume claims no spawn origin either" \
    "" "$(opt "${sock}" "${newpane}" spawned_by)"
# E-2102's naming still holds; the identity write must not have disturbed it.
assert_eq "the window is still named for the task (E-2102)" \
    "E-2104" "$(tmux -S "${sock}" display-message -p -t "${newpane}" '#{window_name}')"

tmux -S "${sock}" kill-server 2>/dev/null

summary
