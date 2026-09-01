#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2006 and records what was true when E-2006
# landed. Edit it only if you ARE E-2006. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2006 verification script — one answer to "was this an agent?", and it is the
# one on the event envelope.
#
# What changed. `tasks_notify_sessions` suppresses a session's notice about its
# own edit by reading `tasks.changed_by_session`, which the Go executor stamps in
# `stampTaskActor`. That stamp decided "an agent made this change" by re-reading
# `os.Getenv("CLAUDECODE")` in whichever process happened to be executing the
# event (E-1917). The envelope had meanwhile begun recording the same fact as
# `Actor.Harness`, observed by `events.EmittingActor` from `internal/agentenv`
# (E-2005). Two answers to one question, which could disagree in BOTH directions:
#
#   CLAUDECODE=1 with no CLAUDE_CODE_ENTRYPOINT  -> old: agent   new: person
#   CLAUDE_CODE_ENTRYPOINT=cli with no CLAUDECODE -> old: person  new: agent
#
# and the answer that decided suppression was ambient process state that was
# never written down, so a notice that got dropped left nothing behind to explain
# why. `stampTaskActor` now reads `evt.Actor.Harness` and `agentRanThisCommand()`
# is gone — the last CLAUDECODE read in Go.
#
# What did NOT change, and must not: the attribution itself. Crediting a command
# typed in a sibling tmux pane to the session whose window it was typed in
# (E-1294) is the resolver working — it is what puts a real value in
# `task_landings.session_id` instead of NULL. `session_id` answers *which session
# is this about*; suppression needs *who typed this*. This task stops one
# consumer from using the first as a proxy for the second. The E-1917 assertions
# are re-run below verbatim to hold that line.
#
# Run from anywhere inside the worktree:
#   endless task verify E-2006
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Isolation: a throwaway git repo as project root under a temp dir, plus a temp
# XDG_CONFIG_HOME (its own DB) and XDG_CACHE_HOME. No real DB, ledger, cache or
# log is touched. The freshly-built worktree binary is driven end to end —
# `endless-go event emit` for the mutation, `endless-go hook claude` on stdin for
# the claim path — so the whole chain (envelope -> executor -> column -> trigger
# -> notice) is exercised, not stubbed.
#
# Fail-fast: the Go unit layer runs FIRST, as E-2001/E-2005 do. It pins the
# executor and the predicate at a granularity the shell cannot reach, so if it
# fails there is no point running the end-to-end checks.
#
# The last section re-runs .endless/tasks/e-1917/verify.sh and
# .endless/tasks/e-2005/verify.sh in full. The first owns the behavior this task
# re-expresses; the second owns the field it now reads. Neither needed editing,
# which is itself the evidence that only the SOURCE of the answer moved.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

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

# ─── locate the worktree + binary ───────────────────────────────────────────

WT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
EGO="$WT/bin/endless-go"

if [[ ! -x "$EGO" ]]; then
    printf '%sSETUP FAILED%s: %s not built. Run `just build` first.\n' \
        "${RED}" "${RESET}" "$EGO" >&2
    exit 2
fi

# ─── 0. fail-fast unit layer ────────────────────────────────────────────────

section "Executor, predicate and bypass-path unit tests (fail-fast)"

# StampTaskActor is E-1917's suite plus this task's own divergence cases. The
# four inherited tests changed only in HOW they declare agent-ness (t.Setenv
# became evt.Actor.Harness); every assertion and failure message is what E-1917
# shipped.
if go_out="$(cd "$WT" && go test ./internal/events/ -run StampTaskActor 2>&1)"; then
    report_pass "go test ./internal/events -run StampTaskActor"
else
    report_fail "go test ./internal/events -run StampTaskActor" "all tests pass" "$go_out"
    summary
    exit 1
fi

# Present() is the one predicate both sides now share. Its Desktop rows are the
# load-bearing ones: an unsupported harness is still an agent.
if go_out="$(cd "$WT" && go test ./internal/agentenv/ -run 'Present' 2>&1)"; then
    report_pass "go test ./internal/agentenv -run Present"
else
    report_fail "go test ./internal/agentenv -run Present" "all tests pass" "$go_out"
    summary
    exit 1
fi

# The two writes that bypass the executor and so have no envelope to read.
if go_out="$(cd "$WT" && go test ./internal/monitor/ \
        -run 'StartWorkSession|CompleteTask' 2>&1)"; then
    report_pass "go test ./internal/monitor -run 'StartWorkSession|CompleteTask'"
else
    report_fail "go test ./internal/monitor -run 'StartWorkSession|CompleteTask'" \
        "all tests pass" "$go_out"
    summary
    exit 1
fi

# The Python mirror. It gained present() in the same change; if the two tables
# diverge, the Go side wins, but they should only diverge by mistake.
if py_out="$(cd "$WT" && uv run --project "$WT" python -m pytest \
        tests/test_agent_env.py -q 2>&1)"; then
    report_pass "pytest tests/test_agent_env.py (Python mirror)"
else
    report_fail "pytest tests/test_agent_env.py (Python mirror)" "all tests pass" "$py_out"
    summary
    exit 1
fi

# ─── the source-of-truth grep ───────────────────────────────────────────────

section "There is exactly one answer left in Go"

# Not a style check. A SECOND reader of CLAUDECODE for this question is the
# defect, and it is invisible: both gates are subtractive, so a disagreement
# over-notifies rather than failing, and nothing surfaces.
# Comment lines are excluded deliberately: stampTaskActor's doc block still
# NAMES the retired call, which is the record of why it is gone.
stray="$(cd "$WT" && grep -rn 'Getenv("CLAUDECODE")' --include='*.go' . \
    | grep -v '^./vendor' | grep -v '_test.go' | grep -vE ':[0-9]+:[[:space:]]*//' \
    | wc -l | tr -d ' ')"
assert_eq "no Go production code reads CLAUDECODE to ask who acted" "0" "$stray"

gone="$(cd "$WT" && grep -rn 'agentRanThisCommand' --include='*.go' . \
    | grep -v '^./vendor' | wc -l | tr -d ' ')"
assert_eq "agentRanThisCommand is deleted, not merely unused" "0" "$gone"

# ─── fixture ────────────────────────────────────────────────────────────────

TMP=""; REPO=""; DBDIR=""

# Four tasks. The first pair is the disagreement, one direction each; the second
# pair drives the hook's own claim path, which bypasses the executor entirely.
TASK_HUMAN=7601        # envelope says person, process says agent
TASK_AGENT=7602        # envelope says agent, process says person
TASK_CLAIM=7603        # claimed through `hook claude` under a supported harness
TASK_OTHER=7604        # claimed with no harness at all — the inverse case

SESS_H="sess-e2006-h";  SID_H=9601   # holds TASK_HUMAN, the edit is credited to it
SESS_A="sess-e2006-a";  SID_A=9602   # holds TASK_AGENT, ditto
SESS_C="sess-e2006-c";  SID_C=9603   # holds TASK_CLAIM, does the claiming
SESS_O="sess-e2006-o";  SID_O=9604   # holds TASK_OTHER, claims WITHOUT a harness

E() { ( cd "$REPO" && uv run --project "$WT" endless "$@" ); }
Q() { E sql "$1" --tsv 2>/dev/null; }
W() { E sql "$1" --write >/dev/null 2>&1; }

# EMIT <task-id> <session-id> <agent|human> <old-status> <new-status>
#
# Drives the real emit path. The harness is NOT a flag — `endless-go event emit`
# reads it from its own environment via EmittingActor, which is the entire point:
# a value the caller supplies is a value a stale caller can get wrong.
#
# CLAUDECODE is set to the OPPOSITE of what the envelope will carry, on purpose.
# It is the variable the deleted gate read, and it must now change nothing. The
# `human` case must strip CLAUDE_CODE_ENTRYPOINT explicitly because this script
# is frequently run BY an agent whose environment carries it; the `agent` case
# must set it because it is also run by a person at a terminal. Neither can rely
# on the ambient environment and still mean anything.
EMIT() {
    local task="$1" session="$2" who="$3" old="$4" new="$5"
    local -a envcmd
    if [[ "$who" == "agent" ]]; then
        envcmd=(env -u CLAUDECODE CLAUDE_CODE_ENTRYPOINT=cli)
    else
        envcmd=(env -u CLAUDE_CODE_ENTRYPOINT -u __CFBundleIdentifier CLAUDECODE=1)
    fi
    "${envcmd[@]}" "$EGO" --config-dir "$DBDIR" event emit \
        --kind task.status_changed \
        --project probe \
        --entity-type task \
        --entity-id "$task" \
        --actor-kind cli \
        --actor-id "verify@test" \
        --session-id "$session" \
        --node-id 1337 \
        --project-root "$REPO" \
        --payload "{\"old_status\":\"${old}\",\"new_status\":\"${new}\"}" \
        >/dev/null 2>&1
}

# NOTICES <session-id>: notices queued for a session, delivered or not. Counting
# the table isolates the trigger's fan-out from the delivery path, which E-1917
# and E-2005 already cover end to end.
NOTICES() { Q "SELECT count(*) FROM session_notices WHERE session_id=$1"; }

# STAMP <task-id>: 'NULL' or the sessions.id recorded as the acting session.
STAMP() {
    local v; v="$(Q "SELECT COALESCE(changed_by_session, 'NULL') FROM tasks WHERE id=$1")"
    printf '%s' "$v"
}

# CLAIM_HOOK <session-id> <task-id> [harness]
#
# A PostToolUse payload naming an `endless task claim` Bash call — the ONLY route
# to monitor.StartWorkSession, which bypasses the executor and so calls
# agentenv.Present() directly. TMUX_PANE is stripped so the probe does not bind
# itself to whatever live session is running this script.
CLAIM_HOOK() {
    # ${3-cli}, not ${3:-cli}: an EXPLICIT empty third argument means "no
    # harness", and :- would substitute the default for it and silently test the
    # supported case twice.
    local sess="$1" task="$2" harness="${3-cli}"
    local -a envcmd
    if [[ -n "$harness" ]]; then
        envcmd=(env -u TMUX_PANE "CLAUDE_CODE_ENTRYPOINT=$harness")
    else
        envcmd=(env -u TMUX_PANE -u CLAUDE_CODE_ENTRYPOINT -u __CFBundleIdentifier)
    fi
    printf '{"session_id":"%s","cwd":"%s","hook_event_name":"PostToolUse","tool_name":"Bash","tool_input":{"command":"endless task claim %s"}}' \
        "$sess" "$REPO" "$task" \
    | "${envcmd[@]}" "$EGO" --config-dir "$DBDIR" hook claude >/dev/null 2>&1
}

setup_fixture() {
    # -P is load-bearing on macOS, where mktemp hands back /var/... while the Go
    # hook resolves cwd to /private/var/.... Registering one and probing the
    # other makes the hook auto-register a SECOND project for the same directory,
    # and every assertion then reads an empty one.
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

    # The claim matcher, which is USER config: matchers.Load reads the machine
    # layer from ConfigDir()/config.json, and ConfigDir() follows the temp
    # XDG_CONFIG_HOME this fixture exports. Without it `hook claude` parses the
    # PostToolUse payload, finds no `start`/`task` matcher, and returns silently
    # — so the claim-path section below would pass by never running.
    # Transcribed from ~/.config/endless/config.json; a drift here shows up as
    # the claim assertions failing, not as a false green.
    cat > "$DBDIR/config.json" <<'JSON'
{
  "matchers": [
    {"type": "start",   "scope": "task", "method": "regex",
     "match": "endless\\s+task\\s+claim\\s+(?:[Ee]-)?(\\d+)"},
    {"type": "confirm", "scope": "task", "method": "regex",
     "match": "endless\\s+task\\s+confirm\\s+(?:[Ee]-)?(\\d+)"}
  ]
}
JSON

    E project register "$REPO" --name probe --label Probe --desc d --lang Go --status active \
        >/dev/null 2>&1

    local pid
    pid="$(Q "SELECT id FROM projects WHERE name='probe'")"
    [[ -n "$pid" ]] || return 1

    W "INSERT INTO tasks (id, project_id, title, status, phase)
       VALUES ($TASK_HUMAN, $pid, 'Human edit probe', 'ready', 'now'),
              ($TASK_AGENT, $pid, 'Agent edit probe', 'ready', 'now'),
              ($TASK_CLAIM, $pid, 'Claim path probe', 'ready', 'now'),
              ($TASK_OTHER, $pid, 'Second holder probe', 'ready', 'now')"
    W "INSERT INTO sessions (id, session_id, project_id, platform, state, active_task_id, kind_id, started_at, last_activity)
       VALUES ($SID_H, '$SESS_H', $pid, 'claude', 'working', $TASK_HUMAN, 1, '2026-08-20T00:00:00', '2026-08-20T00:00:00'),
              ($SID_A, '$SESS_A', $pid, 'claude', 'working', $TASK_AGENT, 1, '2026-08-20T00:00:00', '2026-08-20T00:00:00'),
              ($SID_C, '$SESS_C', $pid, 'claude', 'working', $TASK_CLAIM, 1, '2026-08-20T00:00:00', '2026-08-20T00:00:00'),
              ($SID_O, '$SESS_O', $pid, 'claude', 'working', $TASK_OTHER, 1, '2026-08-20T00:00:00', '2026-08-20T00:00:00')"
    W "INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
       VALUES ($SID_H, $TASK_HUMAN, '2026-08-20T00:00:00', '2026-08-20T00:00:00'),
              ($SID_A, $TASK_AGENT, '2026-08-20T00:00:00', '2026-08-20T00:00:00'),
              ($SID_C, $TASK_CLAIM, '2026-08-20T00:00:00', '2026-08-20T00:00:00'),
              ($SID_O, $TASK_OTHER, '2026-08-20T00:00:00', '2026-08-20T00:00:00')"

    [[ "$(Q "SELECT count(*) FROM session_tasks")" == "4" ]] || return 1
    # Nothing has fired yet, so any notice counted later is one this task made.
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

# ─── the one real behavior change, both directions ──────────────────────────

section "The envelope decides, not the executing process"

# Direction 1: a person's edit, executed in a process carrying CLAUDECODE=1.
# The old gate read that variable and called this an agent — stamping the
# session id the resolver credited the human's shell with, and silencing the one
# session that needed to hear. The envelope records no harness, so a person did
# this, so everybody is told.
EMIT "$TASK_HUMAN" "$SID_H" human ready revisit

assert_eq "a person's edit stamps NULL even with CLAUDECODE=1 in the environment" \
    "NULL" "$(STAMP "$TASK_HUMAN")"
assert_eq "...so the session the edit was CREDITED to is still notified" \
    "1" "$(NOTICES "$SID_H")"

# Direction 2: an agent's edit, executed in a process with no CLAUDECODE — a
# replay, a daemon, or simply a harness that sets the entrypoint and not the
# legacy variable. The old gate called this a person and queued the agent a
# notice about its own change.
EMIT "$TASK_AGENT" "$SID_A" agent ready revisit

assert_eq "an agent's edit stamps its session with no CLAUDECODE in the environment" \
    "$SID_A" "$(STAMP "$TASK_AGENT")"
assert_eq "...so the agent is not told about the change it just made" \
    "0" "$(NOTICES "$SID_A")"

# The value that decided suppression is now on the ledger line rather than
# discarded with the process. This is the auditability half of the task: a notice
# that was dropped can be explained afterward.
ledger="$(cat "$REPO"/.endless/db-ledger/*.jsonl 2>/dev/null \
    | grep "\"id\":\"$TASK_AGENT\"" | tail -1)"
if [[ "$ledger" == *'"harness":"claude_cli"'* ]]; then
    report_pass "the deciding value is durable — it is on the event that made the change"
else
    report_fail "the deciding value is durable — it is on the event that made the change" \
        'the ledger line carries "harness":"claude_cli"' "$ledger"
fi
if [[ "$(cat "$REPO"/.endless/db-ledger/*.jsonl 2>/dev/null \
        | grep "\"id\":\"$TASK_HUMAN\"" | tail -1)" != *'"harness"'* ]]; then
    report_pass "...and a person's event carries no harness key at all"
else
    report_fail "...and a person's event carries no harness key at all" \
        "no harness key" "$(cat "$REPO"/.endless/db-ledger/*.jsonl | grep "\"id\":\"$TASK_HUMAN\"" | tail -1)"
fi

# ─── the paths that have no envelope to read ────────────────────────────────

section "The hook's claim path is unchanged — the gate is a no-op here"

# A PASS here means the §3 gate changed nothing, which is the intended outcome.
# `hook claude` runs only under a supported harness (E-1962), so an agent is the
# only thing that can reach StartWorkSession, and the stamp is right by
# construction. The gate says so at the site instead of leaving it to be
# re-derived.
CLAIM_HOOK "$SESS_C" "$TASK_CLAIM" cli

assert_eq "claiming through the hook still moves the task to underway" \
    "underway" "$(Q "SELECT status FROM tasks WHERE id=$TASK_CLAIM")"
assert_eq "...and still stamps the claiming session" \
    "$SID_C" "$(STAMP "$TASK_CLAIM")"
assert_eq "...so the claiming session is not told about its own claim" \
    "0" "$(NOTICES "$SID_C")"

# Fan-out to the OTHER holders is deliberately not asserted here. `hook claude`
# reaps sessions whose process is gone, and every session in this fixture is a
# row without one, so a second holder is `ended` by the time the trigger runs and
# is excluded by design. That scoping is pinned where it can be observed
# cleanly: TestStampTaskActor_AgentEditStillNotifiesOtherHolders, and arm 1 of
# e-1917-verify.sh below.

# The inverse demonstration the plan asks for: the gate only fires when the
# caller runs without a harness, which today cannot happen — the hook itself
# returns before reading stdin, so nothing is written at all.
before_status="$(Q "SELECT status FROM tasks WHERE id=$TASK_OTHER")"
CLAIM_HOOK "$SESS_O" "$TASK_OTHER" ""
assert_eq "without a harness the hook writes nothing — it never reads stdin" \
    "$before_status" "$(Q "SELECT status FROM tasks WHERE id=$TASK_OTHER")"
assert_eq "...so nothing is stamped either" "NULL" "$(STAMP "$TASK_OTHER")"

# ─── the rules this task must not have moved ────────────────────────────────

section "E-1917's self-suppression rule still holds end to end (precondition)"

# Only HOW agent-ness is expressed changed. If this suite needed editing to stay
# green, the behavior moved and this task overreached.
if e1917_out="$("$WT/tests/tasks/e-1917-verify.sh" 2>&1)"; then
    report_pass "tests/tasks/e-1917-verify.sh passes UNTOUCHED"
else
    report_fail "tests/tasks/e-1917-verify.sh passes UNTOUCHED" \
        "exit 0" "$(printf '%s' "$e1917_out" | tail -20)"
fi

section "E-2005's landing notice still holds (the field this now reads)"

if e2005_out="$("$WT/tests/tasks/e-2005-verify.sh" 2>&1)"; then
    report_pass "tests/tasks/e-2005-verify.sh passes in full"
else
    report_fail "tests/tasks/e-2005-verify.sh passes in full" \
        "exit 0" "$(printf '%s' "$e2005_out" | tail -20)"
fi

summary
