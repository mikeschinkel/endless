#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2418 and records what was true when E-2418
# landed. Edit it only if you ARE E-2418. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2418 — a task is continued only from its earlier session: task claim,
# task bind and the SessionStart cwd auto-bind refuse a task another session
# holds or held, unless the user asks for a fresh transcript.
#
# Checks, fail-fast in order:
#   1. claim and bind refuse a present or ended holder, name it and
#      `session goto E-N --resume`, change nothing, pass your own task, and
#      offer --new-transcript to a person only; --new-transcript marks its
#      launch (tests/test_continue_from_earlier_session.py).
#   2. The SessionStart auto-bind: a present holder in another pane is refused
#      with a fault, an ended holder on a fresh start is refused unless marked,
#      resume/clear/compact, a same-pane holder and a spawned session bind
#      (internal/hookcmd TestCwdBind_*).
#   3. The board no longer raises the duplicate mark for two holders; the new
#      fault code is catalogued and documented.
#   4. The neighbours whose messages changed: spawn's prior-claim guard, the
#      live/unreachable owner refusals, and `session end`.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || setup_error "not inside a git worktree"
cd "${REPO_ROOT}" || setup_error "cannot cd to ${REPO_ROOT}"
for tool in go uv; do
    command -v "${tool}" >/dev/null 2>&1 || setup_error "${tool} not on PATH"
done

check() {
    local name=$1; shift
    local out
    if out=$("$@" 2>&1); then
        report_pass "${name}"
    else
        report_fail "${name}" "pass" "${out}"
        summary
    fi
}

section "1. task claim and task bind"
check "Python: claim/bind refusals and the --new-transcript mark" \
    uv run pytest -q tests/test_continue_from_earlier_session.py

section "2. The SessionStart cwd auto-bind"
check "Go: internal/hookcmd TestCwdBind_*" \
    go test -count=1 -run 'TestCwdBind_' ./internal/hookcmd/

section "3. The board and the fault catalog"
check "Go: internal/monitor stewardship" \
    go test -count=1 -run 'TestStewardship_' ./internal/monitor/
check "Go: internal/faults catalog (WARN-0036 numbered and documented)" \
    go test -count=1 ./internal/faults/

section "4. Neighbouring refusals"
check "Python: spawn guard, owner refusals, session end, bind write-once" \
    uv run pytest -q tests/test_prior_claim_guard.py tests/test_check_task_ownership.py \
        tests/test_session_end.py tests/test_task_bind_write_once.py \
        tests/test_task_bind_planning.py tests/test_transcript_gone.py

summary
