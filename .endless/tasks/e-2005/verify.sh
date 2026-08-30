#!/usr/bin/env bash
#
# E-2005 verification script — a session is told when a task it holds is landed.
#
# The gap: landing a task changes no field `tasks_notify_sessions` watches, so
# that trigger never fired and a session was never told its own work reached
# main. E-2001 fixed the delivery CHANNEL (injections were framed in a shape the
# harness discards); this is the missing MESSAGE.
#
# Why it needed a schema change rather than one more trigger. `events.Actor`
# recorded {kind, id, session_id}, and none of the three answers "was this a
# PERSON or an AGENT". ActorKind names the CHANNEL — a human typing
# `endless worktree land` and an agent shelling out to the same command both
# produce kind="cli". session_id is worse than useless for the question: it
# answers "which session is this ABOUT", and its resolver deliberately credits a
# bare shell in a sibling tmux pane to the Claude session next to it (E-1294),
# so a human's land routinely arrives carrying the agent's own session id.
# Suppressing on session_id alone would silence exactly the session whose work
# just landed. Actor.Harness (from internal/agentenv, E-1962) is the missing
# axis, observed in the emitting process rather than declared by its caller.
#
# The rule this asserts, stated once: an AGENT is not told about a land it
# performed; a PERSON's land is announced to every holder, INCLUDING the session
# the land was attributed to.
#
# Run from anywhere inside the worktree:
#   esu && ./tests/tasks/e-2005-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Isolation: a throwaway git repo as project root under a temp dir, plus a temp
# XDG_CONFIG_HOME (its own DB) and XDG_CACHE_HOME. No real DB, ledger, cache or
# log is touched. The freshly-built worktree binary is driven end to end —
# `endless-go event emit` for the land, `endless-go hook claude` on stdin for
# the delivery — so the whole chain (envelope → executor → column → trigger →
# notice → renderer → hook framing) is exercised, not stubbed.
#
# Fail-fast: the Go unit layer runs FIRST. It pins the executor, the trigger's
# suppression rule and the renderer at a granularity the shell cannot reach, so
# if it fails there is no point running the end-to-end checks.
#
# The last section re-runs tests/tasks/e-2001-verify.sh in full. Without E-2001's
# framing fix every notice this task queues would be composed correctly, written
# to stdout, and silently discarded — so that suite is a precondition of this
# one, not a neighbour.

set -u

# ─── output ─────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section()     { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}
summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}" "${RESET}"
        return 0
    fi
    printf '  %d passed, %s%d failed%s\n' "${PASS_COUNT}" "${RED}" "${FAIL_COUNT}" "${RESET}"
    for t in "${FAILED_TESTS[@]}"; do printf '    %s✗%s %s\n' "${RED}" "${RESET}" "$t"; done
    printf '\n'
    return 1
}

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"; else report_fail "$1" "$2" "$3"; fi
}
# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output contains: $2" "$3"; fi
}
# assert_not_contains DESC NEEDLE HAYSTACK
assert_not_contains() {
    if [[ "$3" != *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output does NOT contain: $2" "$3"; fi
}

# ─── locate the worktree + binary ───────────────────────────────────────────

WT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
EGO="$WT/bin/endless-go"

if [[ ! -x "$EGO" ]]; then
    printf '%sSETUP FAILED%s: %s not built. Run `just build` first.\n' \
        "${RED}" "${RESET}" "$EGO" >&2
    exit 2
fi

# ─── 0. fail-fast unit layer ────────────────────────────────────────────────

section "Executor, trigger and renderer unit tests (fail-fast)"

if go_out="$(cd "$WT" && go test ./internal/events/ \
        -run 'TestExecTaskLanded|TestLandingNotice|TestEmittingActor' 2>&1)"; then
    report_pass "go test ./internal/events -run '…TaskLanded|LandingNotice|EmittingActor'"
else
    report_fail "go test ./internal/events -run '…TaskLanded|LandingNotice|EmittingActor'" \
        "all tests pass" "$go_out"
    summary
    exit 1
fi

if go_out="$(cd "$WT" && go test ./internal/monitor/ -run 'RenderNotice' 2>&1)"; then
    report_pass "go test ./internal/monitor -run 'RenderNotice'"
else
    report_fail "go test ./internal/monitor -run 'RenderNotice'" "all tests pass" "$go_out"
    summary
    exit 1
fi

# ─── fixture ────────────────────────────────────────────────────────────────

TMP=""; REPO=""; DBDIR=""

# Two tasks, because a land is one-shot per task and the two halves of the rule
# must not contaminate each other: TASK_AGENT is landed BY an agent, TASK_HUMAN
# by a person. Each is held by two sessions so "suppress the lander" and "tell
# the others" are separately observable.
TASK_AGENT=7301
TASK_HUMAN=7302
SESS_LANDER="sess-e2005-lander";  SID_LANDER=9301   # holds TASK_AGENT, lands it
SESS_BYSTAND="sess-e2005-bystand"; SID_BYSTAND=9302 # holds TASK_AGENT
SESS_HUMAN="sess-e2005-human";    SID_HUMAN=9303    # holds TASK_HUMAN, the land
                                                    # is ATTRIBUTED to it
SESS_HUMAN2="sess-e2005-human2";  SID_HUMAN2=9304   # holds TASK_HUMAN
SESS_ENDED="sess-e2005-ended";    SID_ENDED=9305    # holds TASK_HUMAN, ended

E() { ( cd "$REPO" && uv run --project "$WT" endless "$@" ); }
Q() { E sql "$1" --tsv 2>/dev/null; }
W() { E sql "$1" --write >/dev/null 2>&1; }

# LAND <task-id> <session-id|""> <agent|human> <base-branch> <sha>
#
# Drives the real emit path. The harness is NOT a flag: `endless-go event emit`
# reads it from its own environment (that is the whole point — a value the
# caller supplies is a value a stale caller can get wrong), so the two cases
# differ only in what is exported to the process.
#
# The `human` case must strip the variables explicitly, because this script is
# frequently run BY an agent whose environment carries them; the `agent` case
# must set them, because it is also run by a person at a terminal. Neither can
# rely on the ambient environment and still mean anything.
LAND() {
    local task="$1" session="$2" who="$3" base="$4" sha="$5"
    local -a envcmd
    if [[ "$who" == "agent" ]]; then
        envcmd=(env CLAUDE_CODE_ENTRYPOINT=cli)
    else
        envcmd=(env -u CLAUDE_CODE_ENTRYPOINT -u __CFBundleIdentifier)
    fi
    "${envcmd[@]}" "$EGO" --config-dir "$DBDIR" event emit \
        --kind task.landed \
        --project probe \
        --entity-type task \
        --entity-id "$task" \
        --actor-kind cli \
        --actor-id "verify@test" \
        --session-id "$session" \
        --node-id 1337 \
        --project-root "$REPO" \
        --payload "{\"branch\":\"task/${task}-probe\",\"base_branch\":\"${base}\",\"merge_commit_sha\":\"${sha}\"}" \
        >/dev/null 2>&1
}

# NOTICES <session-id>: how many notices are queued for a session, delivered or
# not. Counting the table rather than the hook output isolates the trigger's
# fan-out from the delivery path tested further down.
NOTICES() { Q "SELECT count(*) FROM session_notices WHERE session_id=$1"; }

# RAW <session_id> <event>: the literal bytes the hook writes on stdout.
#
# CLAUDE_CODE_ENTRYPOINT=cli is REQUIRED, not decoration (E-1962): the hook is
# gated on the harness and returns immediately — exit 0, no stdout — when the
# environment is not a supported agent host. TMUX_PANE is stripped so the probe
# does not bind itself to whatever live session is running this script.
RAW() {
    printf '{"session_id":"%s","cwd":"%s","hook_event_name":"%s","source":"startup","prompt":"probe"}' \
        "$1" "$REPO" "$2" \
    | env -u TMUX_PANE CLAUDE_CODE_ENTRYPOINT=cli \
        "$EGO" --config-dir "$DBDIR" hook claude 2>/dev/null
}

# CONTEXT <json>: the injected text out of a raw hook response.
CONTEXT() {
    python3 -c '
import json, sys
raw = sys.argv[1].strip()
if not raw:
    print("<no output>"); sys.exit(0)
try:
    doc = json.loads(raw)
except Exception as e:
    print("<unparseable: %s>" % e); sys.exit(0)
print((doc.get("hookSpecificOutput") or {}).get("additionalContext", ""))
' "$1"
}

setup_fixture() {
    # -P is load-bearing on macOS, where mktemp hands back /var/... while the
    # Go hook resolves cwd to /private/var/.... Registering one and probing the
    # other makes the hook auto-register a SECOND project for the same
    # directory, and every assertion then reads an empty one.
    TMP="$(cd "$(mktemp -d)" && pwd -P)"
    REPO="$TMP/repo"
    DBDIR="$TMP/xdg/endless"
    export XDG_CONFIG_HOME="$TMP/xdg"
    export XDG_CACHE_HOME="$TMP/cache"
    mkdir -p "$REPO" "$DBDIR"

    git -C "$REPO" init -q
    git -C "$REPO" config user.email verify@test
    git -C "$REPO" config user.name verify
    git -C "$REPO" commit -q --allow-empty -m "initial commit"

    E project register "$REPO" --name probe --label Probe --desc d --lang Go --status active \
        >/dev/null 2>&1

    local pid
    pid="$(Q "SELECT id FROM projects WHERE name='probe'")"
    [[ -n "$pid" ]] || return 1

    W "INSERT INTO tasks (id, project_id, title, description, status, phase)
       VALUES ($TASK_AGENT, $pid, 'Agent-landed probe', 'agent-probe-description', 'unverified', 'now'),
              ($TASK_HUMAN, $pid, 'Human-landed probe', 'human-probe-description', 'unverified', 'now')"
    W "INSERT INTO sessions (id, session_id, project_id, platform, state, active_task_id, kind_id, started_at, last_activity)
       VALUES ($SID_LANDER,  '$SESS_LANDER',  $pid, 'claude', 'working', $TASK_AGENT, 1, '2026-08-20T00:00:00', '2026-08-20T00:00:00'),
              ($SID_BYSTAND, '$SESS_BYSTAND', $pid, 'claude', 'working', $TASK_AGENT, 1, '2026-08-20T00:00:00', '2026-08-20T00:00:00'),
              ($SID_HUMAN,   '$SESS_HUMAN',   $pid, 'claude', 'working', $TASK_HUMAN, 1, '2026-08-20T00:00:00', '2026-08-20T00:00:00'),
              ($SID_HUMAN2,  '$SESS_HUMAN2',  $pid, 'claude', 'idle',    $TASK_HUMAN, 1, '2026-08-20T00:00:00', '2026-08-20T00:00:00'),
              ($SID_ENDED,   '$SESS_ENDED',   $pid, 'claude', 'ended',   $TASK_HUMAN, 1, '2026-08-20T00:00:00', '2026-08-20T00:00:00')"
    W "INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
       VALUES ($SID_LANDER,  $TASK_AGENT, '2026-08-20T00:00:00', '2026-08-20T00:00:00'),
              ($SID_BYSTAND, $TASK_AGENT, '2026-08-20T00:00:00', '2026-08-20T00:00:00'),
              ($SID_HUMAN,   $TASK_HUMAN, '2026-08-20T00:00:00', '2026-08-20T00:00:00'),
              ($SID_HUMAN2,  $TASK_HUMAN, '2026-08-20T00:00:00', '2026-08-20T00:00:00'),
              ($SID_ENDED,   $TASK_HUMAN, '2026-08-20T00:00:00', '2026-08-20T00:00:00')"

    [[ "$(Q "SELECT count(*) FROM session_tasks")" == "5" ]] || return 1
    # Nothing has landed yet, so any notice counted later is one this task made.
    [[ "$(Q "SELECT count(*) FROM session_notices")" == "0" ]] || return 1
    return 0
}

teardown_fixture() { [[ -n "$TMP" && -d "$TMP" ]] && rm -rf "$TMP"; }

if ! setup_fixture; then
    printf '%sSETUP FAILED%s: could not build the isolated fixture.\n' "${RED}" "${RESET}" >&2
    teardown_fixture
    exit 2
fi
trap teardown_fixture EXIT

# Burn SESS_HUMAN's first turn BEFORE anything lands. The one-shot task-list
# context is consumed by whichever event fires first for a session, so spending
# it here keeps the later assertions about the notice — and, more to the point,
# a turn taken AFTER the land would drain the very notice under test.
RAW "$SESS_HUMAN" UserPromptSubmit >/dev/null

# ─── the envelope carries the harness ───────────────────────────────────────

section "actor.harness is observed at emit time, not declared"

LAND "$TASK_AGENT" "$SID_LANDER" agent main 1dd0006abcdef

ledger="$(cat "$REPO"/.endless/db-ledger/*.jsonl 2>/dev/null | grep task.landed | tail -1)"
assert_contains "the ledger line records the harness that emitted it" \
    '"harness":"claude_cli"' "$ledger"
assert_eq "...and it reaches task_landings.landed_by_harness" \
    "claude_cli" "$(Q "SELECT landed_by_harness FROM task_landings WHERE task_id=$TASK_AGENT")"
assert_eq "...alongside the branch the work landed ON" \
    "main" "$(Q "SELECT base_branch FROM task_landings WHERE task_id=$TASK_AGENT")"

# ─── an agent's own land ────────────────────────────────────────────────────

section "An agent is not told about a land it performed"

assert_eq "the landing session gets no notice" "0" "$(NOTICES "$SID_LANDER")"
assert_eq "the other holder is still told" "1" "$(NOTICES "$SID_BYSTAND")"

# ─── a person's land ────────────────────────────────────────────────────────

section "A person's land is announced to every holder"

# No harness exported: a human's shell. Actor.SessionID still carries 9303 —
# exactly what the resolver produces for `esu` or a sibling pane — so this is
# the case that a session-id-only suppression rule would have silenced.
LAND "$TASK_HUMAN" "$SID_HUMAN" human main 6671bca987654

# NULL, not "": an empty string is a recorded value, and the suppression rule
# reads NULL as "a person did this, tell everyone".
assert_eq "a human's land records NULL harness, not the empty string" \
    "1" "$(Q "SELECT landed_by_harness IS NULL FROM task_landings WHERE task_id=$TASK_HUMAN")"
assert_eq "the session the land was ATTRIBUTED to is told anyway" \
    "1" "$(NOTICES "$SID_HUMAN")"
assert_eq "an idle holder is told too — it can still come back" \
    "1" "$(NOTICES "$SID_HUMAN2")"
assert_eq "an ended holder is not queued an undeliverable notice" \
    "0" "$(NOTICES "$SID_ENDED")"
assert_eq "a human's land stamps no acting session on the notice" \
    "1" "$(Q "SELECT count(*) FROM session_notices
              WHERE session_id=$SID_HUMAN AND changed_by_session IS NULL")"

# ─── delivery, through the hook, in the shape the harness accepts ───────────

section "The notice reaches the agent's context"

out="$(RAW "$SESS_HUMAN" UserPromptSubmit)"
ctx="$(CONTEXT "$out")"

assert_contains "the FYI line rides inside hookSpecificOutput" \
    "FYI — E-$TASK_HUMAN" "$ctx"
assert_contains "...naming the branch the work landed on and the commit" \
    "landed on main (6671bca)" "$ctx"

section "Delivery stays one-shot"

out="$(RAW "$SESS_HUMAN" UserPromptSubmit)"
ctx="$(CONTEXT "$out")"
assert_not_contains "the next turn does not repeat it" "landed on main" "$ctx"
assert_contains "...while the per-turn active-task line still ships" \
    "Active task: E-$TASK_HUMAN" "$ctx"

# ─── the channel this rides on ──────────────────────────────────────────────

section "E-2001's framing fix still holds (precondition)"

# Every notice above is composed correctly and written to stdout. Before E-2001
# the harness discarded exactly that output, silently, exit 0 — so this suite
# would pass in full while nothing reached any agent.
if e2001_out="$("$WT/tests/tasks/e-2001-verify.sh" 2>&1)"; then
    report_pass "tests/tasks/e-2001-verify.sh passes in full"
else
    report_fail "tests/tasks/e-2001-verify.sh passes in full" \
        "exit 0" "$(printf '%s' "$e2001_out" | tail -20)"
fi

summary
