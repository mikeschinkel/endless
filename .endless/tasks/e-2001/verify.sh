#!/usr/bin/env bash
#
# E-2001 verification script — UserPromptSubmit and SessionStart additionalContext
# must reach the agent.
#
# The bug: everything Endless injected on those two events was composed
# correctly, written to stdout, and silently discarded by the harness. The hook
# emitted a bare top-level `{"additionalContext":"…"}`. The Claude Code hooks
# contract accepts `additionalContext` in exactly one place — nested under
# `hookSpecificOutput` alongside the event name — and the harness decides how to
# read a hook's stdout from its FIRST CHARACTER: a leading `{` means "parse as
# JSON", so the plain-text-stdout channel these two events also support is not a
# fallback for an object it fails to recognise. The object parsed, carried no
# field the event honors, and was dropped: no error, no transcript entry, exit 0.
#
# Silently disabled for as long as each had existed: the `endless guide` pointer
# (E-1854), the one-shot task-list context, the per-turn active-task line and
# change notices (E-1917), the pending inter-session message banner, and the
# report-channel rule. PostToolUse was unaffected — it already nested, which is
# why the E-1822 claim handoff arrived while none of the above did.
#
# Run from anywhere inside the worktree:
#   endless task verify E-2001
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# What this asserts, and why it is the SHAPE rather than the text: every test
# that came before asserted what the hook WRITES, and the broken hook wrote the
# right text. Nothing downstream of the hook can distinguish "emitted" from
# "delivered" — the drop is silent, and injected context is not recorded in the
# transcript either, so its absence there proves nothing in either direction.
# The framing on stdout is therefore the only thing a test can hold.
#
# Isolation: a throwaway git repo as project root under a temp dir, plus a temp
# XDG_CONFIG_HOME (its own DB) and XDG_CACHE_HOME. No real DB, ledger, cache or
# log is touched. The freshly-built worktree binary is driven end to end by
# feeding real payloads to `endless-go hook claude` on stdin.
#
# Fail-fast: the Go shape tests run FIRST. They pin the contract at a
# granularity the shell cannot reach (the exact bytes, the absence of a
# top-level field, one JSON document), so if they fail there is no point running
# the end-to-end checks.

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
# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output contains: $2" "$3"; fi
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

section "Contract shape unit tests (fail-fast)"

if go_out="$(cd "$WT" && go test ./internal/hookcmd/ \
        -run 'ContextInjection|WriteContextInjection|PostToolUseConstructors|ClaimHandoffResponse|ReportRelayResponse|RevisitBlockResponse' 2>&1)"; then
    report_pass "go test ./internal/hookcmd -run '…ContextInjection…'"
else
    report_fail "go test ./internal/hookcmd -run '…ContextInjection…'" "all tests pass" "$go_out"
    summary
    exit 1
fi

# ─── fixture ────────────────────────────────────────────────────────────────

TMP=""; REPO=""; DBDIR=""
TASK=7201
# Two sessions, because the guide pointer + task list are ONE-SHOT per session:
# whichever event fires first consumes them. Sharing one session id would leave
# the UserPromptSubmit section silently asserting against the active-task line
# it had already fallen through to.
SESS_START="sess-e2001-start"; SID_START=9201
SESS_PROMPT="sess-e2001-prompt"; SID_PROMPT=9202

E() { ( cd "$REPO" && uv run --project "$WT" endless "$@" ); }
Q() { E sql "$1" --tsv 2>/dev/null; }
W() { E sql "$1" --write >/dev/null 2>&1; }

# RAW <session_id> <event> [source]: the literal bytes the hook writes on stdout.
#
# CLAUDE_CODE_ENTRYPOINT=cli is REQUIRED, not decoration (E-1962): the hook is
# gated on the harness and returns immediately — exit 0, no stdout — when the
# environment is not a supported agent host, and a bare shell is not one.
# Without it every check below would fail for a human running this by hand while
# passing for an agent whose environment happens to carry the variable.
#
# TMUX_PANE is stripped for the opposite reason: left in place the probe reads
# the spawn marker of whatever live session is running this script and tries to
# bind itself to that session's task.
RAW() {
    printf '{"session_id":"%s","cwd":"%s","hook_event_name":"%s","source":"%s","prompt":"probe"}' \
        "$1" "$REPO" "$2" "${3:-startup}" \
    | env -u TMUX_PANE CLAUDE_CODE_ENTRYPOINT=cli \
        "$EGO" --config-dir "$DBDIR" hook claude 2>/dev/null
}

# FIELD <json> <selector>: read one thing out of a raw hook response.
#   keys      → the top-level keys, comma-joined and sorted
#   event     → hookSpecificOutput.hookEventName
#   context   → hookSpecificOutput.additionalContext
#   bare      → "present" iff a top-level additionalContext exists (the bug)
#   docs      → how many JSON documents stdout carried
FIELD() {
    python3 -c '
import json, sys
raw = sys.argv[1].strip()
sel = sys.argv[2]
if sel == "docs":
    print(len([l for l in raw.splitlines() if l.strip()]))
    sys.exit(0)
if not raw:
    print("<no output>"); sys.exit(0)
try:
    doc = json.loads(raw)
except Exception as e:
    print("<unparseable: %s>" % e); sys.exit(0)
hso = doc.get("hookSpecificOutput") or {}
print({
    "keys":    ",".join(sorted(doc)),
    "event":   hso.get("hookEventName", "<missing>"),
    "context": hso.get("additionalContext", ""),
    "bare":    "present" if "additionalContext" in doc else "absent",
}[sel])
' "$1" "$2"
}

setup_fixture() {
    # -P is load-bearing on macOS, where mktemp hands back /var/... while the
    # Go hook resolves cwd to /private/var/.... Registering one and probing the
    # other makes the hook auto-register a SECOND project for the same
    # directory, and every task-list assertion then reads an empty one.
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

    # The task-list context renders DESCRIPTIONS, not titles, so the fixture
    # needs both: the description is what proves the list arrived, the title is
    # what proves the active-task line did.
    W "INSERT INTO tasks (id, project_id, title, description, status, phase, tier)
       VALUES ($TASK, $pid, 'Shape probe task', 'shape-probe-description', 'ready', 'now', 2)"
    W "INSERT INTO sessions (id, session_id, project_id, platform, state, active_task_id, kind_id, started_at, last_activity)
       VALUES ($SID_START, '$SESS_START', $pid, 'claude', 'working', $TASK, 1, '2026-08-20T00:00:00', '2026-08-20T00:00:00'),
              ($SID_PROMPT, '$SESS_PROMPT', $pid, 'claude', 'working', $TASK, 1, '2026-08-20T00:00:00', '2026-08-20T00:00:00')"
    W "INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
       VALUES ($SID_START, $TASK, '2026-08-20T00:00:00', '2026-08-20T00:00:00'),
              ($SID_PROMPT, $TASK, '2026-08-20T00:00:00', '2026-08-20T00:00:00')"

    [[ "$(Q "SELECT count(*) FROM projects")" == "1" ]] || return 1
    [[ "$(Q "SELECT count(*) FROM session_tasks WHERE task_id=$TASK")" == "2" ]] || return 1
    return 0
}

teardown_fixture() { [[ -n "$TMP" && -d "$TMP" ]] && rm -rf "$TMP"; }

# ─── end-to-end ─────────────────────────────────────────────────────────────

if ! setup_fixture; then
    printf '%sSETUP FAILED%s: could not build the isolated fixture.\n' "${RED}" "${RESET}" >&2
    teardown_fixture
    exit 2
fi
trap teardown_fixture EXIT

section "SessionStart — the shape the harness accepts"

out="$(RAW "$SESS_START" SessionStart startup)"

assert_eq "stdout is exactly one JSON document" "1" "$(FIELD "$out" docs)"
assert_eq "...starting with '{', so the harness parses it as JSON" \
    "{" "${out:0:1}"
assert_eq "the only top-level key is hookSpecificOutput" \
    "hookSpecificOutput" "$(FIELD "$out" keys)"
assert_eq "there is NO top-level additionalContext (the discarded shape)" \
    "absent" "$(FIELD "$out" bare)"
assert_eq "hookSpecificOutput names the event that fired" \
    "SessionStart" "$(FIELD "$out" event)"
assert_contains "the guide pointer rides inside it (E-1854)" \
    "endless guide" "$(FIELD "$out" context)"
assert_contains "...as does the task-list context" \
    "shape-probe-description" "$(FIELD "$out" context)"

section "UserPromptSubmit — first turn (one-shot task list)"

out="$(RAW "$SESS_PROMPT" UserPromptSubmit)"

assert_eq "the only top-level key is hookSpecificOutput" \
    "hookSpecificOutput" "$(FIELD "$out" keys)"
assert_eq "there is NO top-level additionalContext" \
    "absent" "$(FIELD "$out" bare)"
assert_eq "hookSpecificOutput names the event that fired" \
    "UserPromptSubmit" "$(FIELD "$out" event)"
assert_contains "the one-shot task list rides inside it" \
    "shape-probe-description" "$(FIELD "$out" context)"

section "UserPromptSubmit — every later turn (active-task line)"

# The strongest probe in the whole task: this line ships on EVERY prompt and
# needs no notice to exist, so its absence from a session's context is what
# proved the injection was being dropped rather than merely never generated.
out="$(RAW "$SESS_PROMPT" UserPromptSubmit)"

assert_eq "hookSpecificOutput names the event that fired" \
    "UserPromptSubmit" "$(FIELD "$out" event)"
assert_eq "there is NO top-level additionalContext" \
    "absent" "$(FIELD "$out" bare)"
assert_contains "the active-task line rides inside it, with live state" \
    "Active task: E-$TASK (ready · tier 2 · now) — Shape probe task." \
    "$(FIELD "$out" context)"

section "UserPromptSubmit — a change made from another terminal (E-1917)"

# A NULL-actor update is exactly the user typing `endless task update` in a bare
# terminal. The notice is marked delivered at render time, so a notice framed in
# the discarded shape is consumed and never re-told — which is why the framing,
# not the rendering, is what this task had to fix.
W "UPDATE tasks SET status='unverified' WHERE id=$TASK"
out="$(RAW "$SESS_PROMPT" UserPromptSubmit)"

assert_eq "hookSpecificOutput names the event that fired" \
    "UserPromptSubmit" "$(FIELD "$out" event)"
assert_eq "there is NO top-level additionalContext" \
    "absent" "$(FIELD "$out" bare)"
assert_contains "the FYI line rides inside it" \
    "FYI — E-$TASK" "$(FIELD "$out" context)"
assert_contains "...naming the transition the session missed" \
    "status: ready → unverified" "$(FIELD "$out" context)"

section "The discarded shape cannot come back"

# One struct field in the package carries this tag, and it is the one nested
# under hookSpecificOutput. A future top-level `additionalContext` — the exact
# revert this task exists to prevent — adds a second and fails here, whether or
# not any behavioural test happens to cover the event it was added to.
tags="$(grep -c 'json:"additionalContext' "$WT"/internal/hookcmd/*.go | \
        awk -F: '{n += $2} END {print n+0}')"
assert_eq "exactly one additionalContext JSON tag exists in internal/hookcmd" \
    "1" "$tags"

summary
