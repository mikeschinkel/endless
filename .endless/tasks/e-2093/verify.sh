#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2093 and records what was true when E-2093
# landed. Edit it only if you ARE E-2093. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2093 closed two gaps that stranded a session.
#
#   1. The session state machine had no wake edge. `Stop` marked a session
#      idle at the end of every turn and nothing marked it working again, so
#      one clean turn locked a session out of Write/Edit for the rest of its
#      life. The gate then announced "No active work session", which was false
#      for a session that HAD claimed a task, and named `task claim`, which
#      refuses on status before reaching the question the refusal was about.
#      `--force` cleared that only by demoting the task.
#   2. The `handoff_verify` partial said how to RUN a suite and never where
#      the file goes, so sessions kept writing one at the retired
#      `tests/tasks/` path.
#
# What is verified here:
#   A. Fail-fast: the task's own tests, Go and Python, all pass.
#   B. The wake edge lives in TouchSession — the one place every turn reaches
#      — and is conditioned on holding a task, not on the entry point.
#   C. The gate admits a declared session that is working OR idle, and the two
#      refusals stay distinct.
#   D. `--force` is gone from both refusals; `--unattended` is what the
#      no-session half is called now, and the settled half names a reopen
#      route that the lifecycle can actually walk.
#   E. Every handoff that carries a verify instruction names where the suite
#      file goes, and none of them names the retired path.
#
# See .endless/plans/E-2093.md.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# First and fail-fast, so the one command is a complete proof rather than a
# spot check bolted onto a green build somebody else made.

if out=$(go test ./internal/monitor/ ./internal/hookcmd/ ./internal/templatecmd/ 2>&1); then
    report_pass "go test: monitor + hookcmd + templatecmd"
else
    report_fail "go test: monitor + hookcmd + templatecmd" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

if out=$(uv run pytest -q \
        tests/test_task_claim_worktree.py \
        tests/test_task_reopen.py \
        tests/test_no_self_dev_ids.py 2>&1); then
    report_pass "pytest: claim, spawn-reopen, self-dev-id guard"
else
    report_fail "pytest: claim, spawn-reopen, self-dev-id guard" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# ---------------------------------------------------------------------------
section "B. The wake edge, and where it lives"
# ---------------------------------------------------------------------------
# The location is load-bearing twice over:
#   - It must be at the ONE per-event point every turn reaches, not in the
#     UserPromptSubmit handler. A turn begun from a `!` bash-input fires no
#     UserPromptSubmit, so waking there alone reproduces the bug in a narrower,
#     harder-to-see form.
#   - It must NOT be inside TouchSession, which a sibling SHELL pane also
#     reaches on a session's behalf. That version woke idle sessions that were
#     not acting, on every shell command and every monitor repaint.

SESSION_GO="${WT}/internal/monitor/session.go"
CLAUDE_GO="${WT}/internal/hookcmd/claude.go"

wake_body=$(awk '/^func WakeSession\(/,/^}/' "${SESSION_GO}")
assert_contains "the wake requires the session to hold a task" \
    "state = 'idle' AND task_id IS NOT NULL" "${wake_body}"
assert_contains "the wake writes only working" "SET state = 'working'" "${wake_body}"

touch_body=$(awk '/^func TouchSession\(/,/^}/' "${SESSION_GO}")
assert_not_contains "TouchSession does not wake — a sibling shell reaches it" \
    "'working'" "${touch_body}"
assert_contains "the ended revival stays in TouchSession" \
    "WHEN sessions.state = 'ended' THEN 'needs_input'" "${touch_body}"

# The hook calls it once per event, before any event-specific branching, so
# every event of every turn goes through it.
run_claude=$(awk '/^func runClaude\(/,/^}/' "${CLAUDE_GO}")
assert_contains "the hook wakes on every event" \
    "monitor.WakeSession(payload.SessionID)" "${run_claude}"
if [[ "${run_claude}" != *"monitor.TouchSession("*"monitor.WakeSession("* ]]; then
    report_fail "the wake sits beside the per-event touch" \
        "WakeSession called after TouchSession in runClaude" "not in that order"
else
    report_pass "the wake sits beside the per-event touch"
fi

# It must be in runClaude's shared prologue, not in one event's handler.
prompt_body=$(awk '/^func handleUserPromptSubmit\(/,/^}/' "${CLAUDE_GO}")
assert_not_contains "the wake is not bolted onto UserPromptSubmit" \
    "WakeSession" "${prompt_body}"

for t in \
    TestWakeSession_WakesIdleSessionHoldingATask \
    TestWakeSession_DoesNotWakeSessionHoldingNoTask \
    TestWakeSession_LeavesEveryOtherState \
    TestWakeSession_IsIdempotent \
    TestWakeSession_UnknownSessionIsNotAnError \
    TestWakeSession_StopStillEndsIdle \
    TestTouchSession_DoesNotWakeIdle \
    TestTouchSession_EndedRevivalStillLandsNeedsInput ; do
    if grep -q "^func ${t}(" "${WT}/internal/monitor/session_lifecycle_test.go"; then
        report_pass "${t} exists"
    else
        report_fail "${t} exists" "func ${t}(...)" "not found"
    fi
done

# ---------------------------------------------------------------------------
section "C. The gate asks about the declaration, not the state alone"
# ---------------------------------------------------------------------------

may_write=$(awk '/^func sessionMayWrite\(/,/^}/' "${CLAUDE_GO}")
assert_contains "the gate requires a declared task" "s.TaskID == nil" "${may_write}"
assert_contains "working and idle are both admitted" \
    "case stateWorking, stateIdle:" "${may_write}"

refusal=$(awk '/^func declarationRefusal\(/,/^}/' "${CLAUDE_GO}")
assert_not_contains "the false message is gone" \
    "No active work session" "${refusal}"
assert_contains "the undeclared refusal names a command that works" \
    "endless task claim <id>" "${refusal}"
assert_contains "the declared-but-blocked refusal says the task IS declared" \
    "The task IS declared" "${refusal}"
assert_not_contains "no refusal offers --force" "--force" "${refusal}"

# The whole message set, once, so a string moved between branches is still caught.
gate_msgs=$(printf '%s\n%s\n' "${may_write}" "${refusal}")
assert_not_contains "the gate never names --force anywhere" "--force" "${gate_msgs}"

# The expiry branch that could not fire, and named a command that could not
# help, is gone along with its helper. Matched on the declaration and on any
# CALL, not on the bare name — the tombstone comment that explains the removal
# names it too, and deleting that comment to satisfy a grep would be the wrong
# way round.
if grep -rq "func IsSessionExpired" "${WT}/internal/"; then
    report_fail "IsSessionExpired is deleted" "no declaration" \
        "$(grep -rl 'func IsSessionExpired' "${WT}/internal/" | head -5)"
else
    report_pass "IsSessionExpired is deleted"
fi
if grep -rqE "[^c]IsSessionExpired\(" "${WT}/internal/"; then
    report_fail "nothing calls IsSessionExpired" "no call sites" \
        "$(grep -rlE '[^c]IsSessionExpired\(' "${WT}/internal/" | head -5)"
else
    report_pass "nothing calls IsSessionExpired"
fi

# ---------------------------------------------------------------------------
section "D. --force is split into named flags"
# ---------------------------------------------------------------------------

claim_help=$(uv run endless task claim --help 2>&1) \
    || setup_error "endless task claim --help failed: ${claim_help}"
assert_contains "claim documents --unattended" "--unattended" "${claim_help}"
assert_not_contains "claim no longer advertises --force" "--force" "${claim_help}"

spawn_help=$(uv run endless task spawn --help 2>&1) \
    || setup_error "endless task spawn --help failed: ${spawn_help}"
assert_not_contains "spawn no longer advertises --force" "--force" "${spawn_help}"

# The reopen route has to be one the lifecycle actually has an edge for, which
# is two different statuses. `revisit` from shipped work, `untriaged` from an
# abandonment decision — the transition table has no declined -> revisit edge.
BIN="${WT}/bin/endless-go"
[[ -x "${BIN}" ]] || setup_error "missing ${BIN}; run 'just build' in the worktree"

for s in unverified unreviewed confirmed assumed completed; do
    if "${BIN}" task-status has shipped "${s}" >/dev/null 2>&1; then
        report_pass "${s} is shipped work, so it reopens to revisit"
    else
        report_fail "${s} in group 'shipped'" "member" "not a member"
    fi
done
for s in declined obsolete; do
    if "${BIN}" task-status has shipped "${s}" >/dev/null 2>&1; then
        report_fail "${s} not in group 'shipped'" "not a member" "member"
    else
        report_pass "${s} never shipped, so it reopens to untriaged"
    fi
done

# `bind` stops describing itself as a display field. It sets the ownership
# record, and the understatement is why it read as too small to be an answer.
# Checked on the HELP a user reads, not only on the internal docstring.
bind_help=$(uv run endless task bind --help 2>&1) \
    || setup_error "endless task bind --help failed: ${bind_help}"
assert_not_contains "bind no longer calls itself display-only" \
    "status-bar display only" "${bind_help}"
assert_contains "bind help says it records ownership" \
    "ownership record" "${bind_help}"

bind_doc=$(awk '/^def bind_item\(/,/^    """$/' "${WT}/src/endless/task_cmd.py")
assert_not_contains "the bind_item docstring drops the understatement too" \
    "status-bar display only" "${bind_doc}"

# ---------------------------------------------------------------------------
section "E. The handoff says where a verify suite goes"
# ---------------------------------------------------------------------------

CLOSE_TMPL="${WT}/internal/templatecmd/templates/handoff/_close.tmpl"
verify_partial=$(awk '/{{define "handoff_verify"/,/{{- end}}/' "${CLOSE_TMPL}")
assert_contains "the partial names the suite path" \
    'The suite goes at `.endless/tasks/e-{{.spawned_id}}/verify.sh`' \
    "${verify_partial}"
assert_contains "the partial names the shared harness" \
    '.endless/tasks/_harness.sh' "${verify_partial}"
assert_contains "the partial routes to the rules" \
    '.endless/tasks/CLAUDE.md' "${verify_partial}"

# The rules it points at must still say what the sentence claims. A handoff
# teaching a stale rule is worse than one teaching none.
tasks_claude_md="${WT}/.endless/tasks/CLAUDE.md"
assert_contains "CLAUDE.md still puts suites at .endless/tasks/e-<id>/" \
    '.endless/tasks/e-<id>/' "$(cat "${tasks_claude_md}")"
assert_contains "CLAUDE.md still has the suite source ../_harness.sh" \
    '/../_harness.sh"' "$(cat "${tasks_claude_md}")"

# The retired path must not reappear in the text a session READS. Template
# comments ({{/* ... */}}) are stripped at render and are where this task's own
# rationale lives, so they are stripped here too rather than grepped against.
handoff_text=$(cat "${WT}/internal/templatecmd/templates/handoff/"*.tmpl \
    | perl -0777 -pe 's/\{\{\/\*.*?\*\/\s*-?\}\}//gs')
assert_not_contains "no handoff names the retired tests/tasks/ path" \
    "tests/tasks/" "${handoff_text}"

if grep -q "^func TestRender_Handoff_VerifyPartialNamesTheSuiteLocation(" \
        "${WT}/internal/templatecmd/claim_handoff_test.go"; then
    report_pass "the render test guards the sentence"
else
    report_fail "the render test guards the sentence" \
        "func TestRender_Handoff_VerifyPartialNamesTheSuiteLocation(...)" "not found"
fi

summary
