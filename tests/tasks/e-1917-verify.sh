#!/usr/bin/env bash
#
# E-1917 verification script — notify a session, on its next turn, when a task
# it holds changes.
#
# The problem: the user changes a task from the CLI mid-session, the agent keeps
# answering from the status it learned N turns ago, and the two proceed on
# different facts until the user corrects it by hand.
#
# Three arms:
#   1. A SQLite trigger (tasks_notify_sessions) fans a watched-field change out
#      to every session holding that task in session_tasks, one immutable row per
#      (session, update event), suppressing the session that made the change.
#   2. The UserPromptSubmit hook drains those rows ONCE, renders one FYI line
#      each, and marks them delivered. The active-task line additionally carries
#      current status/tier/phase — the one place state IS re-asserted every turn,
#      because a context compaction discards notices already delivered.
#   3. Delivery (not writing) is logged to .endless/logs/session-notices.jsonl,
#      so `tail -f` shows what the agent was actually shown.
#
# Run from anywhere inside the worktree:
#   esu && ./tests/tasks/e-1917-verify.sh
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Isolation: a throwaway git repo as project root under a temp dir, plus a temp
# XDG_CONFIG_HOME (its own DB) and XDG_CACHE_HOME. No real DB, ledger, cache or
# log is touched. The freshly-built worktree binary is exercised end to end: the
# trigger through the isolated DB, and delivery by feeding real UserPromptSubmit
# payloads to `endless-go hook claude` on stdin.
#
# Fail-fast: the Go unit tests for the trigger and renderer run FIRST. They pin
# the four freeform transitions, the content-leak guarantee and the one-shot
# contract at a granularity the shell cannot reach, so if they fail there is no
# point running the end-to-end checks.

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
    else report_fail "$1" "output must NOT contain: $2" "$3"; fi
}

# ─── shared fixture ──────────────────────────────────────────────────────────

WT=""; TMP=""; REPO=""; EGO=""; DBDIR=""

# E: the worktree's Python CLI against the isolated DB, from the repo dir.
E() { ( cd "$REPO" && uv run --project "$WT" endless "$@" ); }
# Q SQL: one scalar/row out of the isolated DB.
Q() { E sql "$1" --tsv 2>/dev/null; }
# W SQL: a write against the isolated DB.
W() { E sql "$1" --write >/dev/null 2>&1; }

# HOOK SESSION_ID: feed one UserPromptSubmit payload to the candidate hook and
# print the additionalContext it injects (empty when it injects nothing).
# Driving the real binary on stdin is the point — the delivery half only exists
# inside the hook, and a unit test cannot observe it.
# CLAUDE_CODE_ENTRYPOINT=cli is REQUIRED, not decoration (E-1962): the hook is
# gated on the harness and returns immediately — exit 0, no stdout — when the
# environment is not a supported agent host. A bare shell is not one. Without
# this the hook silently injects nothing and every check below fails for the
# user while passing for an agent whose own environment happens to carry the
# variable. Scoped to this subprocess so nothing else in the suite inherits it.
HOOK() {
    printf '{"session_id":"%s","cwd":"%s","hook_event_name":"UserPromptSubmit","prompt":"hello"}' \
        "$1" "$REPO" \
    | CLAUDE_CODE_ENTRYPOINT=cli "$EGO" --config-dir "$DBDIR" hook claude 2>/dev/null \
    | python3 -c 'import json,sys
raw = sys.stdin.read().strip()
if not raw:
    sys.exit(0)
try:
    print(json.loads(raw).get("additionalContext", ""))
except Exception:
    print(raw)'
}

# NOTICE_COUNT SESSION_ID: undelivered notices queued for a session.
NOTICE_COUNT() {
    Q "SELECT count(*) FROM session_notices WHERE session_id=$1 AND notified=0"
}

TASK=7101       # the task both sessions hold
S1=9101         # the session under test
S2=9102         # a second holder, to prove delivery is per-session
SESS1="sess-e1917-a"
SESS2="sess-e1917-b"
LOG=""

setup_fixture() {
    TMP="$(mktemp -d)"
    REPO="$TMP/repo"
    DBDIR="$TMP/xdg/endless"
    LOG="$REPO/.endless/logs/session-notices.jsonl"
    export XDG_CONFIG_HOME="$TMP/xdg"
    export XDG_CACHE_HOME="$TMP/cache"
    export PATH="$WT/bin:$PATH"
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

    W "INSERT INTO tasks (id, project_id, title, status, phase, tier)
       VALUES ($TASK, $pid, 'Notice probe task', 'ready', 'now', 2)"

    # Two sessions, both bound to the task and both holding it in session_tasks.
    W "INSERT INTO sessions (id, session_id, project_id, platform, state, active_task_id, kind_id, started_at, last_activity)
       VALUES ($S1, '$SESS1', $pid, 'claude', 'working', $TASK, 1, '2026-08-07T00:00:00', '2026-08-07T00:00:00'),
              ($S2, '$SESS2', $pid, 'claude', 'working', $TASK, 1, '2026-08-07T00:00:00', '2026-08-07T00:00:00')"
    W "INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
       VALUES ($S1, $TASK, '2026-08-07T00:00:00', '2026-08-07T00:00:00'),
              ($S2, $TASK, '2026-08-07T00:00:00', '2026-08-07T00:00:00')"

    [[ "$(Q "SELECT count(*) FROM session_tasks WHERE task_id=$TASK")" == "2" ]] || return 1
    return 0
}

teardown_fixture() { [[ -n "$TMP" && -d "$TMP" ]] && rm -rf "$TMP"; }

# ─── locate the worktree + binary ───────────────────────────────────────────

WT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
EGO="$WT/bin/endless-go"

if [[ ! -x "$EGO" ]]; then
    printf '%sSETUP FAILED%s: %s not built. Run `just build` first.\n' \
        "${RED}" "${RESET}" "$EGO" >&2
    exit 2
fi

# ─── 0. fail-fast unit layer ────────────────────────────────────────────────

section "Trigger + renderer unit tests (fail-fast)"

if go_out="$(cd "$WT" && go test ./internal/monitor/ -run 'Notice|TaskHeadline' 2>&1)"; then
    report_pass "go test ./internal/monitor -run 'Notice|TaskHeadline'"
else
    report_fail "go test ./internal/monitor -run 'Notice|TaskHeadline'" "all tests pass" "$go_out"
    summary
    exit 1
fi

# The self-suppression regression suite. These drive the EXECUTOR with a real
# environment rather than seeding changed_by_session with SQL — the gap that let
# the original defect ship past 20 green checks.
if go_out="$(cd "$WT" && go test ./internal/events/ -run StampTaskActor 2>&1)"; then
    report_pass "go test ./internal/events -run StampTaskActor"
else
    report_fail "go test ./internal/events -run StampTaskActor" "all tests pass" "$go_out"
    summary
    exit 1
fi

# ─── end-to-end ─────────────────────────────────────────────────────────────

if ! setup_fixture; then
    printf '%sSETUP FAILED%s: could not build the isolated fixture.\n' "${RED}" "${RESET}" >&2
    teardown_fixture
    exit 2
fi
trap teardown_fixture EXIT

section "Arm 1 — the trigger records a change for every holder"

# First turn: consumes the one-shot full-task-list injection so later turns take
# the single-line active-task branch this feature extends.
HOOK "$SESS1" >/dev/null
HOOK "$SESS2" >/dev/null

# An external change: no session made it, exactly like the user typing
# `endless task update` in a bare terminal.
W "UPDATE tasks SET status='revisit' WHERE id=$TASK"

assert_eq "a NULL-actor change queues a notice for holder 1" \
    "1" "$(NOTICE_COUNT $S1)"
assert_eq "...and for holder 2" \
    "1" "$(NOTICE_COUNT $S2)"

# The rule that decides signal vs noise.
W "UPDATE tasks SET status='underway', changed_by_session=$S1 WHERE id=$TASK"
assert_eq "a session's OWN change queues nothing for itself" \
    "1" "$(NOTICE_COUNT $S1)"
assert_eq "...but still notifies the other holder" \
    "2" "$(NOTICE_COUNT $S2)"

# "changed → notify; unchanged → don't"
before="$(Q "SELECT count(*) FROM session_notices")"
W "UPDATE tasks SET status='underway' WHERE id=$TASK"
assert_eq "setting a field to the value it already holds queues nothing" \
    "$before" "$(Q "SELECT count(*) FROM session_notices")"

# Freeform content must never be reproduced.
W "UPDATE tasks SET description='a-secret-description-body', changed_by_session=NULL WHERE id=$TASK"
assert_not_contains "a description change carries no content" \
    "a-secret-description-body" "$(Q "SELECT changes FROM session_notices ORDER BY id DESC LIMIT 1")"

section "Arm 2 — the hook delivers each notice exactly once"

out="$(HOOK "$SESS1")"
assert_contains "the turn after a change carries the FYI line" \
    "FYI — E-$TASK" "$out"
assert_contains "...naming the status transition it missed" \
    "status: ready → revisit" "$out"
assert_contains "...and the freeform change without its content" \
    "description added" "$out"
assert_not_contains "...still never leaking the content" \
    "a-secret-description-body" "$out"

again="$(HOOK "$SESS1")"
assert_not_contains "the NEXT turn does not repeat it" "FYI —" "$again"
assert_eq "...because delivery flipped notified" "0" "$(NOTICE_COUNT $S1)"

# S2 has never had a turn, so it still holds everything: both NULL-actor changes
# (status, description) plus the status change S1 authored and was spared.
assert_eq "one session draining does not consume the other's" \
    "3" "$(NOTICE_COUNT $S2)"

section "Arm 2 — the active-task line re-asserts current state"

assert_contains "the active-task line carries current status" \
    "Active task: E-$TASK (underway" "$again"
assert_contains "...tier and phase too" "tier 2 · now)" "$again"

# Re-assertion is what survives a compaction, so it must track the live row.
W "UPDATE tasks SET phase='next' WHERE id=$TASK"
HOOK "$SESS1" >/dev/null   # drain the notice that change just queued
moved="$(HOOK "$SESS1")"
assert_contains "...and follows the row when it moves" "tier 2 · next)" "$moved"

section "Arm 3 — delivery is logged for tail -f"

if [[ -f "$LOG" ]]; then
    report_pass "the delivery log exists at .endless/logs/session-notices.jsonl"
else
    report_fail "the delivery log exists at .endless/logs/session-notices.jsonl" \
        "$LOG" "missing"
fi

log_ok="$(python3 - "$LOG" <<'PY'
import json, sys, pathlib
p = pathlib.Path(sys.argv[1])
if not p.exists():
    print("missing"); raise SystemExit
lines = [l for l in p.read_text().splitlines() if l.strip()]
if not lines:
    print("empty"); raise SystemExit
try:
    entries = [json.loads(l) for l in lines]
except Exception as e:
    print(f"not-jsonl: {e}"); raise SystemExit
required = {"at", "session", "task", "notice_id", "changes", "rendered"}
missing = required - set(entries[0])
print("ok" if not missing else f"missing-keys: {sorted(missing)}")
PY
)"
assert_eq "every log line is JSON carrying the rendered text and raw changes" "ok" "$log_ok"

logged_sessions="$(python3 - "$LOG" <<'PY'
import json, sys, pathlib
lines = [l for l in pathlib.Path(sys.argv[1]).read_text().splitlines() if l.strip()]
print(",".join(sorted({str(json.loads(l)["session"]) for l in lines})))
PY
)"
assert_eq "only the session that was actually shown a notice is logged" \
    "$S1" "$logged_sessions"

summary
