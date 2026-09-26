#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2168 and records what was true when E-2168
# landed. Edit it only if you ARE E-2168. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2168 verification — `session resume` can re-enter a tmux window that
# tmux-resurrect restored after a crash.
#
# Before: the restore brought back a window's name, index, layout, panes and
# working directories, but NOT its `@endless_*` options — its save format
# carries only `pane`, `window`, `state` and `grouped_session` lines, and a real
# save file contains zero `@endless` strings. So a restored window came back
# looking right and answering wrong: crowded with panes holding dead shells,
# while `@endless_task_id` still named whatever task it held before the crash.
# `session resume` refused on both grounds and routed to `session goto --resume`,
# which opens a NEW window — abandoning the restored one along with the layout
# and scrollback the restore had just recovered.
#
# After: two flags, each naming exactly one thing it permits, so a run that
# needed only one does not silently waive the other:
#
#     endless session resume E-NNNN --rebind --no-sibling-panes
#
# `--rebind` rewrites the WINDOW's `@endless_task_id`; `sessions.task_id` is
# write-once and is not touched, which is why the flag cannot breach that
# invariant rather than merely happening not to. Section 7 counts that from the
# database rather than asserting it from the source. `--rebind` has one refusal
# of its own: another LIVE window still claiming the target task would put two
# windows on one task in parallel.
#
# Sections 3 onward run against a REAL tmux server on a private socket and a
# REAL SQLite database with the real schema, so "the window option was
# rewritten", "the layout was built" and "no session row moved" are all counted
# from the thing itself. Only the `execvp` that would replace this process is
# stubbed, plus the chdir that precedes it.
#
#   endless task verify E-2168
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

command -v tmux >/dev/null 2>&1 || setup_error "tmux is not installed"

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
# The layout builder is NOT stubbed below — `--no-sibling-panes` is only
# meaningful if the layout it permits really gets built — so `spawn-layout`
# splits the fixture window for real and leaves live processes in the new panes.
# `kill-server` returns as soon as the request is sent, and the runner removes
# its per-run temp HOME the instant this suite exits, so a pane still running
# there makes that removal fail. Waiting for the socket to go is what makes the
# handoff quiet.
cleanup() {
    local sock i
    for sock in "${TMP}"/*.sock; do
        [[ -S "${sock}" ]] || continue
        tmux -S "${sock}" kill-server 2>/dev/null
        for i in $(seq 1 25); do
            [[ -S "${sock}" ]] || break
            sleep 0.2
        done
    done
    rm -rf "${TMP}"
}
trap cleanup EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
# The durable coverage lives in tests/, per .endless/tasks/CLAUDE.md: this
# task's own cases are in test_session_resume_crash_recovery.py, and the four
# neighbouring resume suites own the gates this one sits beside. A failure here
# makes every drive below meaningless, so the suite stops rather than reporting
# a cascade.
section "1. Unit gate (fail fast)"

if uv run pytest -q \
        tests/test_session_resume_crash_recovery.py \
        tests/test_session_resume_clobber_gate.py \
        tests/test_session_resume_window_options.py \
        tests/test_session_resume_recover.py \
        tests/test_session_resume_taskless.py \
        tests/test_session_goto_back.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest crash-recovery flags + resume/goto regression"
else
    report_fail "pytest crash-recovery flags + resume/goto" "exit 0" \
        "$(tail -25 "${TMP}/py.log")"
    summary
fi

# Built from THIS worktree rather than taken off PATH: the live-window check
# resolves its candidates through the real Go `session-query list-live`, and an
# installed binary would prove something about a different tree.
mkdir -p "${TMP}/bin"
if go build -o "${TMP}/bin/endless-go" ./cmd/endless-go >"${TMP}/go.log" 2>&1; then
    report_pass "go build ./cmd/endless-go (the liveness query the check uses)"
else
    report_fail "go build ./cmd/endless-go" "exit 0" "$(tail -25 "${TMP}/go.log")"
    summary
fi

# ── fixture: a real database, a real project, a real worktree ───────────────
# E-4001 is the task being resumed. E-4002 is what the restored window still
# claims — the stale claim the crash left behind. ES-601 is E-4001's resumable
# session; ES-602 is a session on E-4001 that is still WORKING, parked in a
# second window, which is what section 6 measures; ES-603 is the same thing
# ENDED, the dead claim that must not block a recovery.
CFG="${TMP}/cfg"
PROJ="${TMP}/proj"
mkdir -p "${CFG}" "${PROJ}/.endless/worktrees/e-4001"

cat >"${TMP}/seed.py" <<'PY'
import os
from pathlib import Path
from endless import config
config.set_db_context(Path(os.environ["E2168_CFG"]))
from endless import db

root = os.environ["E2168_PROJ"]
db.execute("INSERT INTO projects (id, name, path) VALUES (1, 'e2168', ?)", (root,))
for tid in (4001, 4002):
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status) "
        "VALUES (?, 1, ?, 'underway')", (tid, f"task {tid}"))
# ES-602's pane is filled in by the suite once tmux has minted it; the row is
# seeded pane-less and updated, because a pane id cannot be predicted. It is
# seeded ENDED and brought to life in section 6 — a live competing claim is one
# of the things being measured, so it must not be ambient in the sections that
# measure something else.
for pk, uuid, state, task in (
    (601, "uuid-4001", "ended", 4001),
    (602, "uuid-live", "ended", 4001),
    (603, "uuid-dead", "ended", 4001),
):
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, platform, state, "
        "started_at, last_activity, task_id) "
        "VALUES (?, ?, 1, 'claude', ?, '2026-09-01T00:00:00', "
        "'2026-09-01T00:00:00', ?)", (pk, uuid, state, task))
PY

# set_pane.py <session pk> <pane id> — point a seeded session at a real pane.
#
# A pane binding is (server_uuid, address) in `processes`, which `sessions`
# references by `process_id` — not a bare pane string on the session row
# (E-1898: a tmux server restart reissues "%414" to an unrelated pane, so a row
# keyed on the string alone silently resolves to the wrong session). The suite
# leaves server_uuid NULL, which is what the identity index's ifnull() collapse
# exists for, and what a binding on a server nothing can identify honestly is.
cat >"${TMP}/set_pane.py" <<'PY'
import os, sys
from pathlib import Path
from endless import config
config.set_db_context(Path(os.environ["E2168_CFG"]))
from endless import db

pane = sys.argv[2]
db.execute(
    "INSERT OR IGNORE INTO processes (kind_id, server_uuid, address) "
    "VALUES (1, NULL, ?)", (pane,))
pid = db.query(
    "SELECT id FROM processes WHERE kind_id = 1 AND server_uuid IS NULL "
    "AND address = ?", (pane,))[0]["id"]
db.execute("UPDATE sessions SET process_id = ? WHERE id = ?",
           (pid, int(sys.argv[1])))
PY

# state.py <session pk> — that session's state, for taking ES-602 down.
cat >"${TMP}/set_state.py" <<'PY'
import os, sys
from pathlib import Path
from endless import config
config.set_db_context(Path(os.environ["E2168_CFG"]))
from endless import db

db.execute("UPDATE sessions SET state = ? WHERE id = ?",
           (sys.argv[2], int(sys.argv[1])))
PY

# bindings.py — every session's task binding, as `pk=task` pairs on one line.
# Section 7 diffs this across the whole run: `sessions.task_id` is write-once,
# so the only honest proof that `--rebind` cannot breach it is that no row moved.
cat >"${TMP}/bindings.py" <<'PY'
import os
from pathlib import Path
from endless import config
config.set_db_context(Path(os.environ["E2168_CFG"]))
from endless import db

rows = db.query("SELECT id, task_id FROM sessions ORDER BY id")
print(" ".join(f"{r['id']}={r['task_id']}" for r in rows))
PY

# drive.py — `session resume` end to end, with ONLY the exec and the chdir
# replaced. The window's stale claim is read by the real `_window_claimed_task`
# off the real tmux server; the live-window check runs the real Go query; the
# target is the fixture row, resolved by the real `resume-target`.
cat >"${TMP}/drive.py" <<'PY'
import os, sys
from pathlib import Path
from endless import config
config.set_db_context(Path(os.environ["E2168_CFG"]))
from endless import session_cmd


class Exec(Exception):
    pass


# Into the fixture PROJECT before anything resolves. `_project_root_for_cwd`
# walks up from cwd looking for a registered project, and the live-window check
# asks `session-query list-live` for that root — run from the endless worktree
# instead, it would ask about a project the fixture never registered and read
# back an empty set, which is a check that cannot fail. Done before the chdir
# stub goes in, because that stub is for the LAST chdir (into the resumed
# worktree, on the way to an exec this suite never performs).
os.chdir(os.environ["E2168_PROJ"])

session_cmd.os.execvp = lambda file, argv: (_ for _ in ()).throw(Exec(argv))
session_cmd.os.chdir = lambda p: None

kwargs = {flag: True for flag in sys.argv[2:]}
try:
    session_cmd.resume_session(sys.argv[1], **kwargs)
except Exec as e:
    print("EXEC: " + " ".join(e.args[0]))
    sys.exit(0)
except Exception as e:
    print("REFUSED: " + " ".join(str(e).split()))
    sys.exit(3)
print("NO-EXEC")
sys.exit(0)
PY

# A `claude` the real `_require_claude` can find. Never executed — the drive
# replaces execvp — but resume refuses short of the exec without it, and
# stubbing `_require_claude` would hide that.
printf '#!/bin/sh\nexec sleep 1\n' >"${TMP}/bin/claude"
chmod +x "${TMP}/bin/claude"

# A transcript on disk: E-2106 refuses a resume whose transcript is gone, and
# this suite's subject is the two refusals that come after that one.
CLAUDE_HOME="${TMP}/claude-home"
mkdir -p "${CLAUDE_HOME}/projects/-e2168"
printf '{}\n' >"${CLAUDE_HOME}/projects/-e2168/uuid-4001.jsonl"

E2168_CFG="${CFG}" E2168_PROJ="${PROJ}" uv run python "${TMP}/seed.py" \
    >"${TMP}/seed.log" 2>&1 \
    || setup_error "could not seed the fixture database: $(tail -5 "${TMP}/seed.log")"

# ── the restored window ────────────────────────────────────────────────────
# What tmux-resurrect leaves: the layout back (three panes), the name back, and
# `@endless_task_id` naming the task the window held before the crash. The
# option is SET here rather than left absent because a stale claim is the case
# `--rebind` is named for; the absent-claim case is section 8.
SOCK="${TMP}/crash.sock"
PANE="$(tmux -S "${SOCK}" new-session -d -s c -n 'E-4002-before-the-crash' \
    -P -F '#{pane_id}' 'sleep 120')" \
    || setup_error "could not start a tmux server on ${SOCK}"
tmux -S "${SOCK}" split-window -t "${PANE}" -d 'sleep 120' \
    || setup_error "could not restore the second pane"
tmux -S "${SOCK}" split-window -t "${PANE}" -d 'sleep 120' \
    || setup_error "could not restore the third pane"
tmux -S "${SOCK}" set-option -w -t "${PANE}" @endless_task_id 4002 \
    || setup_error "could not set the stale window claim"

# A SECOND window on the same server, holding ES-602 — the live session on the
# resume target. Its pane is real, so the liveness query and the window resolver
# both see what a real competing claim looks like.
OTHER="$(tmux -S "${SOCK}" new-window -d -t c -n 'E-4001-elsewhere' \
    -P -F '#{pane_id}' 'sleep 120')" \
    || setup_error "could not open the competing window"
E2168_CFG="${CFG}" uv run python "${TMP}/set_pane.py" 602 "${OTHER}" \
    || setup_error "could not point ES-602 at its pane"

# Helpers that count from tmux and from the database, never from a return value.
opt()   { tmux -S "${SOCK}" display-message -p -t "$1" "#{@endless_$2}"; }
panes() { tmux -S "${SOCK}" list-panes -t "$1" -F '#{pane_id}' | wc -l | tr -d ' '; }
bindings() {
    env PATH="${TMP}/bin:${PATH}" E2168_CFG="${CFG}" \
        uv run python "${TMP}/bindings.py" 2>/dev/null | tail -1
}
set_state() {
    env PATH="${TMP}/bin:${PATH}" E2168_CFG="${CFG}" \
        uv run python "${TMP}/set_state.py" "$1" "$2" >/dev/null 2>&1
}

# drive <ref> [flags…] — one resume, run from the restored window's first pane.
#
# CLAUDECODE / CLAUDE_CODE_SESSION_ID are cleared so the real session resolver
# reads this as a plain shell pane, which is what a recovery shell after a crash
# actually is. $TMUX points at the private socket, so every bare `tmux` the code
# runs talks to the fixture server and not to the terminal a person is sitting
# in.
drive() {
    local ref="$1"; shift
    env -u CLAUDECODE -u CLAUDE_CODE_SESSION_ID \
        TMUX="${SOCK},1,0" TMUX_PANE="${PANE}" \
        PATH="${TMP}/bin:${PATH}" \
        CLAUDE_CONFIG_DIR="${CLAUDE_HOME}" \
        E2168_CFG="${CFG}" E2168_PROJ="${PROJ}" \
        uv run python "${TMP}/drive.py" "${ref}" "$@" 2>/dev/null | tail -1
}

BINDINGS_BEFORE="$(bindings)"

# ── 2. the fixture really is what a restore leaves behind ──────────────────
# Asserted, not assumed: every refusal below is only meaningful if the window
# genuinely carries three panes and a claim on the wrong task.
section "2. The restored window"

assert_eq "the restore brought back three panes" "3" "$(panes "${PANE}")"
assert_eq "…and a claim on the task held before the crash" \
    "4002" "$(opt "${PANE}" task_id)"
assert_eq "…while the session uuid it would need is absent, as resurrect \
leaves it" "" "$(opt "${PANE}" session_uuid)"

# ── 3. the defect: a bare resume is refused, and names its route ────────────
section "3. A bare resume refuses"

out="$(drive E-4001)"
assert_contains "resume into the restored window refuses" "REFUSED:" "${out}"
assert_contains "…naming what the window claims" "E-4002" "${out}"
assert_contains "…and the flag that permits taking it over" \
    "endless session resume E-4001 --rebind" "${out}"
assert_eq "the stale claim is left exactly as found" \
    "4002" "$(opt "${PANE}" task_id)"
assert_eq "…and no layout was built over a session that never started" \
    "3" "$(panes "${PANE}")"

# ── 4. each flag waives ONE thing ──────────────────────────────────────────
# Verbose on purpose. A single combined `--recover` was considered and rejected:
# a run that needed only one override must not silently waive the other.
section "4. Neither flag implies the other"

out="$(drive E-4001 no_sibling_panes)"
assert_contains "--no-sibling-panes alone still refuses the stale claim" \
    "REFUSED:" "${out}"
assert_contains "…and still names --rebind" "--rebind" "${out}"

out="$(drive E-4001 rebind)"
assert_contains "--rebind alone still refuses the crowded window" \
    "REFUSED:" "${out}"
assert_contains "…naming the panes it will not resize unasked" "3 panes" "${out}"
assert_contains "…and naming --no-sibling-panes" "--no-sibling-panes" "${out}"
assert_contains "…while still offering the sibling verb for panes that ARE \
yours" "endless session goto E-4001 --resume" "${out}"
assert_eq "neither half-run rewrote the claim" "4002" "$(opt "${PANE}" task_id)"

# ── 5. both flags: the crash case, end to end ──────────────────────────────
# ES-602 and ES-603 both hold E-4001 and are both ENDED here — which is what a
# crash leaves behind — so this section doubles as the proof that a dead claim
# on the target does not block the recovery `--rebind` exists for.
section "5. Both flags recover the window"

out="$(drive E-4001 rebind no_sibling_panes)"
assert_eq "resume reaches the exec" "EXEC: claude --resume uuid-4001" "${out}"
assert_eq "the window's claim now reads the resumed task" \
    "4001" "$(opt "${PANE}" task_id)"
assert_eq "…and its session uuid is published, not left to the first hook \
event" "uuid-4001" "$(opt "${PANE}" session_uuid)"
assert_contains "…and the standard layout was built over the dead panes, so a \
recovered window comes back looking like a spawned one" \
    "$(panes "${PANE}")" "3 4 5 6 7 8 9"

# Put the stale claim back for the sections that still need it.
tmux -S "${SOCK}" set-option -w -t "${PANE}" @endless_task_id 4002

# ── 6. --rebind's own refusal: a LIVE window already has the target ────────
# `tmux window == Endless task == one or more Claude sessions` holds in SERIES,
# never in parallel. ES-602 is working E-4001 in a real second window, so
# rebinding this one onto it is the misuse the check exists for.
section "6. A live window still claiming the target refuses"

set_state 602 working
out="$(drive E-4001 rebind no_sibling_panes)"
assert_contains "--rebind refuses while another window is live on the target" \
    "REFUSED:" "${out}"
assert_contains "…naming that window" "E-4001-elsewhere" "${out}"
assert_contains "…and the session in it" "602" "${out}"
assert_contains "…and routing to the window that already has the task" \
    "endless session goto E-4001" "${out}"
assert_eq "the refused rebind left this window's claim alone" \
    "4002" "$(opt "${PANE}" task_id)"

# The same shape with that session DEAD. Liveness is derived, never read off a
# `state` column — but an ended row is what a dead session leaves, and a stale
# claim from one must not block the recovery the flag exists for.
set_state 602 ended
out="$(drive E-4001 rebind no_sibling_panes)"
assert_eq "with that session ended, the same rebind proceeds" \
    "EXEC: claude --resume uuid-4001" "${out}"
assert_eq "…and the claim moves" "4001" "$(opt "${PANE}" task_id)"
tmux -S "${SOCK}" set-option -w -t "${PANE}" @endless_task_id 4002

# ── 7. the invariant --rebind must not breach ──────────────────────────────
# `sessions.task_id` is write-once, enforced by a trigger. The flag rewrites the
# WINDOW's claim and nothing else, which is what makes it safe rather than
# merely lucky. Counted from the database across every drive above.
section "7. sessions.task_id never moved"

assert_eq "every session still holds the task it was seeded with" \
    "${BINDINGS_BEFORE}" "$(bindings)"
assert_eq "…which is the seeded set, not an empty read" \
    "601=4001 602=4001 603=4001" "${BINDINGS_BEFORE}"

# ── 8. what the flags do NOT change ────────────────────────────────────────
section "8. Untouched surfaces"

# The plain recovery shell: a window carrying no claim at all. tmux renders an
# unset option as empty, and this is the case resume is most often run from — it
# must stay frictionless.
tmux -S "${SOCK}" set-option -wu -t "${PANE}" @endless_task_id
assert_eq "a window with no claim needs no --rebind" \
    "EXEC: claude --resume uuid-4001" "$(drive E-4001 no_sibling_panes)"

# Re-entering the task the window already holds is not a rebind (E-2112).
tmux -S "${SOCK}" set-option -w -t "${PANE}" @endless_task_id 4001
assert_eq "a window already claiming the target needs no --rebind" \
    "EXEC: claude --resume uuid-4001" "$(drive E-4001 no_sibling_panes)"

# --dry-run reaches no exec, so it rewrites no option and resizes no pane.
# Nothing to permit means nothing to refuse.
tmux -S "${SOCK}" set-option -w -t "${PANE}" @endless_task_id 4002
assert_contains "--dry-run is gated by neither flag" "NO-EXEC" \
    "$(drive E-4001 dry_run)"
assert_eq "…and left the claim it was never going to rewrite" \
    "4002" "$(opt "${PANE}" task_id)"

# `--no-sibling-panes` is deliberately NOT on `session goto --resume`: that verb
# opens a brand-new window, which by construction holds one pane, so there is no
# sibling-pane refusal there to waive. A flag that is accepted and does nothing
# is worse than one that is not offered.
goto_help="$(env PATH="${TMP}/bin:${PATH}" uv run endless session goto --help 2>&1)"
assert_not_contains "session goto does not offer --no-sibling-panes" \
    "--no-sibling-panes" "${goto_help}"
assert_contains "…while session resume does" "--no-sibling-panes" \
    "$(env PATH="${TMP}/bin:${PATH}" uv run endless session resume --help 2>&1)"
assert_contains "…and so does --rebind" "--rebind" \
    "$(env PATH="${TMP}/bin:${PATH}" uv run endless session resume --help 2>&1)"

summary
