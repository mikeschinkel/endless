#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2112 and records what was true when E-2112
# landed. Edit it only if you ARE E-2112. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2112 verification — `session resume` no longer refuses to re-enter the task
# the pane is already on.
#
# Before: the E-1968 clobber gate fired on the mere PRESENCE of a task in the
# pane's session, without ever asking what the resume TARGET was. So a pane
# working E-1644 that ran `endless session resume E-1644` — the ordinary move
# after its Claude process died while the window kept the task's identity — was
# told "This pane is working E-1644 … open the target in a NEW window instead:
# endless session goto E-1644 --resume". The same task, named twice, routing you
# to a second window for the task you were already sitting in.
#
# After: the gate compares. It refuses only when the target's task DIFFERS from
# the one the pane holds, and anything the comparison cannot settle — an
# unresolvable ref, a target session that holds no task — keeps the refusal.
#
# Nothing below section 1 is stubbed except the `execvp` that would replace this
# process: the database is a real SQLite file with the real schema, the target
# is resolved by THIS worktree's `endless-go session-query resume-target`, the
# pane's identity is read by the real `_current_pane_task`, and `claude` is
# found on PATH by the real `_require_claude`.
#
#   endless task verify E-2112
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
trap 'rm -rf "${TMP}"' EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
# The durable coverage lives in tests/, per .endless/tasks/CLAUDE.md: the gate's
# own file carries E-2112's self-resume cases, and the three neighbouring resume
# suites plus goto/back own the paths the gate hands off to. A failure here
# makes every drive below meaningless, so the suite stops rather than reporting
# a cascade.
section "1. Unit gate (fail fast)"

if uv run pytest -q \
        tests/test_session_resume_clobber_gate.py \
        tests/test_session_resume_window_options.py \
        tests/test_session_resume_recover.py \
        tests/test_session_resume_taskless.py \
        tests/test_session_goto_back.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest clobber gate + resume/goto regression"
else
    report_fail "pytest clobber gate + resume/goto" "exit 0" \
        "$(tail -25 "${TMP}/py.log")"
    summary
fi

# Built from THIS worktree rather than taken off PATH: the drives resolve their
# targets through the real Go query, and an installed binary would prove
# something about a different tree.
mkdir -p "${TMP}/bin"
if go build -o "${TMP}/bin/endless-go" ./cmd/endless-go >"${TMP}/go.log" 2>&1; then
    report_pass "go build ./cmd/endless-go (the resolver the drives use)"
else
    report_fail "go build ./cmd/endless-go" "exit 0" "$(tail -25 "${TMP}/go.log")"
    summary
fi

# ── fixture: a real database, a real project, real worktrees ────────────────
# Two tasks with a resumable session each, and a third session that never
# claimed a task. E-3001 is what the pane holds; E-3002 is the other work the
# gate exists to protect; ES-503 is the task-less target whose resolution WOULD
# mint a container task, which is what section 5 measures.
CFG="${TMP}/cfg"
PROJ="${TMP}/proj"
mkdir -p "${CFG}" "${PROJ}/.endless/worktrees/e-3001" "${PROJ}/.endless/worktrees/e-3002"

cat >"${TMP}/seed.py" <<'PY'
import os, sys
from pathlib import Path
from endless import config
config.set_db_context(Path(os.environ["E2112_CFG"]))
from endless import db

root = os.environ["E2112_PROJ"]
db.execute("INSERT INTO projects (id, name, path) VALUES (1, 'e2112', ?)", (root,))
for tid in (3001, 3002):
    db.execute(
        "INSERT INTO tasks (id, project_id, title, status) "
        "VALUES (?, 1, ?, 'underway')", (tid, f"task {tid}"))
rows = [
    (501, "uuid-3001", "working", 3001),
    (502, "uuid-3002", "ended", 3002),
    (503, "uuid-none", "ended", None),
]
for pk, uuid, state, task in rows:
    db.execute(
        "INSERT INTO sessions (id, session_id, project_id, platform, state, "
        "started_at, last_activity, task_id) "
        "VALUES (?, ?, 1, 'claude', ?, '2026-09-01T00:00:00', "
        "'2026-09-01T00:00:00', ?)", (pk, uuid, state, task))
PY

# drive.py — `session resume` end to end, with ONLY the exec (and the chdir into
# the target worktree that precedes it) replaced. Everything the gate consults
# is the real thing.
cat >"${TMP}/drive.py" <<'PY'
import os, sys
from pathlib import Path
from endless import config
config.set_db_context(Path(os.environ["E2112_CFG"]))
from endless import session_cmd


class Exec(Exception):
    pass


session_cmd.os.execvp = lambda file, argv: (_ for _ in ()).throw(Exec(argv))
session_cmd.os.chdir = lambda p: None

kwargs = {flag: True for flag in sys.argv[2:]}
try:
    session_cmd.resume_session(sys.argv[1], **kwargs)
except Exec as e:
    print("EXEC: " + " ".join(e.args[0]))
    sys.exit(0)
except Exception as e:
    print("REFUSED: " + str(e).replace("\n", " | "))
    sys.exit(3)
print("NO-EXEC")
sys.exit(0)
PY

# A `claude` the real `_require_claude` can find. It is never executed — the
# drive replaces execvp — but resume refuses short of the exec without it, and
# stubbing `_require_claude` would hide that.
printf '#!/bin/sh\nexec sleep 1\n' >"${TMP}/bin/claude" || setup_error "no temp dir"
chmod +x "${TMP}/bin/claude"

E2112_CFG="${CFG}" E2112_PROJ="${PROJ}" uv run python "${TMP}/seed.py" \
    >"${TMP}/seed.log" 2>&1 \
    || setup_error "could not seed the fixture database: $(tail -5 "${TMP}/seed.log")"

# drive <ref> [flags…] — run one resume as the pane holding E-3001.
#
# TMUX/TMUX_PANE are cleared deliberately: past the gate, resume rewrites its
# pane's `@endless_*` identity (E-2104), and a suite must not reach out and
# relabel the terminal a person is sitting in. ENDLESS_SESSION_ID is layer 1 of
# the real session resolver, so the pane identity is genuine without a stub.
drive() {
    local ref="$1"; shift
    env -u TMUX -u TMUX_PANE -u CLAUDECODE -u CLAUDE_CODE_SESSION_ID \
        PATH="${TMP}/bin:${PATH}" \
        E2112_CFG="${CFG}" ENDLESS_SESSION_ID=501 \
        uv run python "${TMP}/drive.py" "${ref}" "$@" 2>/dev/null | tail -1
}

task_count() {
    env PATH="${TMP}/bin:${PATH}" E2112_CFG="${CFG}" uv run python -c '
import os
from pathlib import Path
from endless import config
config.set_db_context(Path(os.environ["E2112_CFG"]))
from endless import db
print(db.scalar("SELECT count(*) FROM tasks"))' 2>/dev/null | tail -1
}

# ── 2. the defect: self-resume of the pane's own task ───────────────────────
section "2. Self-resume reaches the exec"

out="$(drive E-3001)"
assert_eq "resume of the task this pane holds execs claude --resume" \
    "EXEC: claude --resume uuid-3001" "${out}"
assert_not_contains "…without the replace refusal that used to fire" \
    "This pane is working" "${out}"
assert_not_contains "…and without being sent to a second window for E-3001" \
    "session goto" "${out}"

# ── 3. the control: a different task still refuses ─────────────────────────
section "3. Other work is still protected"

out="$(drive E-3002)"
assert_contains "resume of E-3002 from a pane on E-3001 still refuses" \
    "REFUSED:" "${out}"
assert_contains "…naming the work that would be destroyed" \
    "This pane is working E-3001" "${out}"
assert_contains "…and routing to the new-window surface" \
    "endless session goto E-3002 --resume" "${out}"
assert_contains "…and offering --force" "--force" "${out}"

# The exemption is equality of TASK, not of ref spelling: ES-502 is the same
# task E-3002 by another name, and refuses identically.
assert_contains "the same target by session ref refuses too" \
    "This pane is working E-3001" "$(drive ES-502)"

# ── 4. what the comparison cannot settle keeps the refusal ─────────────────
section "4. The gate fails closed"

assert_contains "a target session holding no task refuses" \
    "This pane is working E-3001" "$(drive ES-503)"
assert_contains "a ref that resolves to nothing refuses" \
    "This pane is working E-3001" "$(drive E-9999)"
assert_contains "…as does a ref that is not a reference at all" \
    "This pane is working E-3001" "$(drive not-a-ref)"

# ── 5. a refusal still resolves nothing it would have to clean up ──────────
# The gate now reads the target before deciding, which is only safe because the
# read is the Go query and not `_resolve_resume` — resolving ES-503 mints a
# container task and a worktree. Counting tasks across the refusal is what
# proves the read stayed read-only rather than asserting it from the source.
section "5. Refusing is still side-effect-free"

before="$(task_count)"
drive ES-503 >/dev/null
after="$(task_count)"
assert_eq "the fixture starts with the two tasks it was seeded with" \
    "2" "${before}"
assert_eq "a refused resume of a task-less session mints nothing" \
    "${before}" "${after}"

# ── 6. the two escapes the gate always had ─────────────────────────────────
section "6. --force and --dry-run are untouched"

assert_eq "--force still replaces the pane with other work" \
    "EXEC: claude --resume uuid-3002" "$(drive E-3002 force)"
assert_contains "--dry-run still needs no --force and stops short of the exec" \
    "NO-EXEC" "$(drive E-3002 dry_run)"

summary
