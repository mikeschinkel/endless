#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2091 and records what was true when E-2091
# landed. Edit it only if you ARE E-2091. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2091 verification — record when a session is waiting on the user.
#
# WHAT LANDED
#   Endless hooked six Claude Code events and none of them reported that the
#   harness was asking the USER something, so a session sitting on a permission
#   prompt read `working` — indistinguishable from one doing work. E-1976 built
#   the attention board's first rank, `actWaiting`, and left the producer out.
#
#   This is the producer. `Notification` is installed as a seventh hook;
#   `prompted` joins internal/sessionstate and is classified into all five
#   groups; notification_type=permission_prompt writes it and idle_prompt writes
#   `idle`; the state clears on the session's next activity. The two writers that
#   were putting `needs_input` in the database — a row's initial value and the
#   revive-an-ended-row CASE — now write `idle`, which is what they meant, and
#   the board stops hiding the rows they left behind.
#
# THE CLAIMS (this suite checks these, not the plumbing)
#   C1  PROMPTED MAY WRITE. `prompted` is in sessionstate.MayWrite and the
#       declaration gate admits a prompted session holding a task. This is the
#       decision the whole design turns on, and its failure mode is not a wrong
#       glyph: it is every prompt-blocked session refused its next write the
#       moment the user approves, and told there is no command to run.
#   C2  WAITING MEANS PAUSED FOR INPUT. AwaitsHuman holds `prompted`, `idle` and
#       `needs_input` — every state in which the session has paused for a person,
#       question or no question — and nothing else. E-2105 left that group
#       reading oddly and handed the reading here; this pins the answer.
#   C3  THE STATE MACHINE. PromptSession writes `prompted` from anywhere;
#       ResumeFromPrompt returns it to `working` and touches nothing else;
#       `Stop` from `prompted` still ends `idle`. That last one is why a missed
#       clear is cosmetic rather than a wedged session.
#   C4  THE HANDLER ROUTES TWO VALUES AND IGNORES THE REST. permission_prompt
#       and idle_prompt act; every other notification_type writes no state. An
#       unrecognised value is also the shape a harness change arrives in.
#   C5  NOTHING WRITES `needs_input`. Both former writers write `idle`, so the
#       state means only what the declaration gate says it means. The rows still
#       carrying it are not migrated — they are revealed (C6) to be resolved.
#   C6  THE HOOK IS INSTALLED, AND THE ROWS ARE REVEALED. setup.py hooks seven
#       events with Notification among them and NOT in SYNC_EVENTS; an install
#       carrying only the old six is repaired to seven. The board's session
#       filter is sessionstate.Live, so a `prompted` session takes the top row
#       and a `needs_input` one renders instead of being suppressed.
#
# ISOLATION
#   Sections A–F touch no database at all — unit tests, a built binary's stdout,
#   and greps over the tree. Section G builds a throwaway git repo and its own
#   XDG_CONFIG_HOME under `mktemp -d`, and drives the board through its headless
#   --project-id seam, which is the one entry point that does NOT pin the main
#   database. No real DB, ledger or config is read or written.
#
# Layers:
#   A. FAIL-FAST unit — the packages this task touched. Nothing below is
#      meaningful if these fail.
#   B. C1 and C2 — the two decisions, asserted directly.
#   C. C3 — the state machine.
#   D. C4 — the handler, against synthetic payloads.
#   E. C5 — the sweep: no writer, and the table says so.
#   F. C6a — the hook is installed, and an old install is repaired.
#   G. C6b — end to end against a seeded throwaway database.
#   H. Project-wide regression.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 on any
# failure, 2 on a setup problem.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT=""        # worktree root
EGO=""       # the worktree's freshly built endless-go
TMP=""       # section G scratch

cleanup() { [[ -n "${TMP}" ]] && rm -rf "${TMP}"; return 0; }
trap cleanup EXIT

# ── setup ───────────────────────────────────────────────────────────────────

WT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
[[ -f "${WT}/go.mod" && -d "${WT}/internal/sessionstate" ]] \
    || setup_error "worktree root not found from ${BASH_SOURCE[0]} (got ${WT})"
cd "${WT}" || setup_error "cannot cd to ${WT}"
EGO="${WT}/bin/endless-go"

# assert_ok DESC CMD... — the command exits 0.
assert_ok() {
    local desc="$1"; shift
    local out rc
    out="$("$@" 2>&1)"; rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return 0; fi
    report_fail "${desc}" "exit 0" "exit ${rc} | $(printf '%s' "${out}" | tail -15)"
    return 1
}

# ── A. fail-fast unit ───────────────────────────────────────────────────────

section "A — the packages this task touched (FAIL-FAST)"

assert_ok "just go builds the worktree binaries" just go
[[ -x "${EGO}" ]] || setup_error "bin/endless-go was not built"

if ! assert_ok "go test ./internal/sessionstate — the vocabulary and its table" \
        go test ./internal/sessionstate/ -count=1; then
    report_fail "FAIL-FAST" "the vocabulary's own tests pass" \
        "they do not; nothing below is meaningful"
    summary
fi
if ! assert_ok "go test ./internal/hookcmd — the handler and the declaration gate" \
        go test ./internal/hookcmd/ -count=1; then
    report_fail "FAIL-FAST" "the hook handler's tests pass" \
        "they do not; nothing below is meaningful"
    summary
fi
assert_ok "go test ./internal/monitor — the writers and the board's read" \
    go test ./internal/monitor/ -count=1
assert_ok "go test ./internal/projectstatuscmd — the board's classify()" \
    go test ./internal/projectstatuscmd/ -count=1
assert_ok "go test ./internal/sessionstatecmd — the CLI seam Python reads" \
    go test ./internal/sessionstatecmd/ -count=1
assert_ok "go test ./internal/events — the claim revive" \
    go test ./internal/events/ -count=1
assert_ok "pytest tests/test_setup_notification_hook.py — the hook is installed" \
    uv run pytest tests/test_setup_notification_hook.py -q

# ── B. the two decisions ────────────────────────────────────────────────────

section "B — C1: prompted may write, and the gate agrees"

assert_eq "prompted is a member of may-write" \
    "yes" "$("${EGO}" session-state has may-write prompted && echo yes || echo no)"

assert_eq "may-write is exactly working, prompted, idle" \
    "working
prompted
idle" "$("${EGO}" session-state get may-write)"

assert_ok "the registry states the decision on its own (TestPromptedMayWrite)" \
    go test ./internal/sessionstate/ -count=1 -run TestPromptedMayWrite
assert_ok "the declaration gate admits a prompted session holding a task" \
    go test ./internal/hookcmd/ -count=1 -run 'TestSessionMayWrite$'
assert_ok "the gate answers whatever MayWrite says, for every state there is" \
    go test ./internal/hookcmd/ -count=1 -run TestSessionMayWriteFollowsTheGroup
assert_ok "an ended session still may not write (MayWrite ⊆ Live)" \
    go test ./internal/sessionstate/ -count=1 -run TestMayWriteIsASubsetOfLive

section "B — C2: waiting on the user means paused for input"

assert_eq "awaits-human is prompted, idle and needs_input, in that order" \
    "prompted
idle
needs_input" "$("${EGO}" session-state get awaits-human)"

assert_eq "a working session does not await a human" \
    "no" "$("${EGO}" session-state has awaits-human working && echo yes || echo no)"
assert_eq "an ended session does not await a human" \
    "no" "$("${EGO}" session-state has awaits-human ended && echo yes || echo no)"

assert_ok "the group is pinned as the statement of what waiting means" \
    go test ./internal/sessionstate/ -count=1 -run TestAwaitsHumanIsEveryPausedState
assert_ok "the two states in BOTH policy groups are the two mid-turn ones" \
    go test ./internal/sessionstate/ -count=1 \
    -run TestAwaitingAHumanAndMayWriteOverlapOnlyMidTurn

section "B — the state is classified into all five groups, and displays"

assert_eq "prompted is in the vocabulary, beside working" \
    "working
prompted
idle
needs_input
ended" "$("${EGO}" session-state get all)"
assert_eq "prompted is live" \
    "yes" "$("${EGO}" session-state has live prompted && echo yes || echo no)"
assert_eq "prompted sorts first in session list's reading order" \
    "0" "$("${EGO}" session-state rank display-order prompted)"
assert_eq "its label" "Prompted" "$("${EGO}" session-state label prompted)"
assert_eq "its glyph is the board's own waiting sign" \
    "⚠" "$("${EGO}" session-state glyph prompted)"
assert_ok "every glyph still measures one column, ⚠ included" \
    go test ./internal/sessionstate/ -count=1 -run TestGlyphsAreSingleWidth
assert_ok "every state still has a display rank, so no row sorts by NULL" \
    go test ./internal/sessionstate/ -count=1 -run TestDisplayOrderCoversAll
assert_contains "the --state option offers it, read from the registry not a copy" \
    "prompted" "$(uv run --project "${WT}" endless session list --help 2>&1 | tr -d '\n')"

# ── C. the state machine ────────────────────────────────────────────────────

section "C — C3: prompt, clear, and the turn boundary"

assert_ok "PromptSession writes prompted from every state" \
    go test ./internal/monitor/ -count=1 -run TestPromptSession_WritesPromptedFromEveryState
assert_ok "ResumeFromPrompt clears ONLY prompted — an idle session survives it" \
    go test ./internal/monitor/ -count=1 -run TestResumeFromPrompt_ClearsOnlyPrompted
assert_ok "a second clear in the same turn is a no-op" \
    go test ./internal/monitor/ -count=1 -run TestResumeFromPrompt_IsIdempotent
assert_ok "the clear restores a state, never manufactures a task claim" \
    go test ./internal/monitor/ -count=1 -run TestResumeFromPrompt_NeedsNoTask
assert_ok "WakeSession does not clear prompted — it fires on every event" \
    go test ./internal/monitor/ -count=1 -run TestWakeSession_LeavesEveryOtherState
assert_ok "prompt → approve → work, end to end through the handler" \
    go test ./internal/hookcmd/ -count=1 -run TestNotification_RoundTrip
assert_ok "Stop from prompted still ends idle — why a missed clear is cosmetic" \
    go test ./internal/hookcmd/ -count=1 -run TestNotification_StopFromPromptedEndsIdle

assert_eq "the transition table names the writer of any → prompted" \
    "yes" "$("${EGO}" session-state transitions \
        | grep -q '^\*	prompted	.*monitor\.PromptSession' && echo yes || echo no)"
assert_eq "and the writer of prompted → working" \
    "yes" "$("${EGO}" session-state transitions \
        | grep -q '^prompted	working	.*monitor\.ResumeFromPrompt' && echo yes || echo no)"
assert_ok "every transition still names the function that performs the write" \
    go test ./internal/sessionstate/ -count=1 -run TestEveryTriggerNamesItsWriter

# ── D. the handler ──────────────────────────────────────────────────────────

section "D — C4: two notification_types act, the rest write nothing"

assert_ok "permission_prompt blocks the session" \
    go test ./internal/hookcmd/ -count=1 \
    -run TestNotification_PermissionPromptBlocksTheSession
assert_ok "idle_prompt idles it — 60 seconds untouched is what idle already says" \
    go test ./internal/hookcmd/ -count=1 -run TestNotification_IdlePromptIdlesTheSession
assert_ok "an unmodelled type moves nothing (auth, elicitation, agent-team, empty)" \
    go test ./internal/hookcmd/ -count=1 -run TestNotification_AnUnmodelledTypeWritesNothing
assert_ok "the clear leaves every other state alone" \
    go test ./internal/hookcmd/ -count=1 -run TestClearPromptState_LeavesEveryOtherStateAlone

# The handler must read the payload's own field. A handler keyed off anything
# else would pass the tests above and see nothing from the real harness.
assert_eq "claudePayload carries notification_type" \
    "yes" "$(grep -q 'json:"notification_type' "${WT}/internal/hookcmd/claude.go" \
        && echo yes || echo no)"
assert_eq "Notification is dispatched from the event switch" \
    "yes" "$(grep -q 'case "Notification":' "${WT}/internal/hookcmd/claude.go" \
        && echo yes || echo no)"
# The two clearing events, at their handlers. A clear wired to only one of them
# leaves a stale glyph whenever the turn takes the other path.
assert_eq "PostToolUse and UserPromptSubmit both clear the prompt" \
    "2" "$(grep -c 'clearPromptState(payload)' "${WT}/internal/hookcmd/claude.go")"
# Notification is not a turn boundary and carries no new messages. Matched on
# the CALL — the file names the function in a comment saying exactly that.
assert_eq "the handler does not parse the transcript" \
    "no" "$(grep -q 'ParseTranscript(' "${WT}/internal/hookcmd/notification.go" \
        && echo yes || echo no)"

# ── E. the sweep ────────────────────────────────────────────────────────────

section "E — C5: nothing writes needs_input any more"

# The two former writers, named. Their files must not mention the constant at
# all: both used it as a write value and nothing else, so any surviving
# reference is the old behaviour.
for f in internal/monitor/session.go internal/events/executor.go; do
    assert_eq "${f} no longer writes sessionstate.NeedsInput" \
        "no" "$(grep -q 'sessionstate\.NeedsInput' "${WT}/${f}" && echo yes || echo no)"
done

assert_ok "InitSession creates the row idle" \
    go test ./internal/monitor/ -count=1 -run TestInitSession_InsertCreatesIdle
assert_ok "TouchSession's INSERT default is idle" \
    go test ./internal/monitor/ -count=1 -run TestTouchSession_InsertCreatesIdle
assert_ok "the revive-an-ended-row CASE lands idle, in both its copies" \
    go test ./internal/monitor/ -count=1 -run TestTouchSession_EndedRevivalStillLandsNeutral
assert_ok "and on the task.claimed path" \
    go test ./internal/events/ -count=1 -run TestClaim_RevivesEndedSession

# The table is documentation of the writers, so it has to agree.
assert_eq "no transition writes needs_input" \
    "0" "$("${EGO}" session-state transitions | awk -F'\t' '$2=="needs_input"' | wc -l | tr -d ' ')"
assert_ok "…and the table's own test names it as the one deliberate exception" \
    go test ./internal/sessionstate/ -count=1 -run TestEveryStateIsWritten

# NOT migrated. The 34 rows carrying the state are surfaced (section G), not
# rewritten in the dark — so the state must still be a valid, addressable value.
assert_eq "needs_input is still in the vocabulary" \
    "yes" "$("${EGO}" session-state has all needs_input && echo yes || echo no)"
assert_eq "…and still refused by the write gate" \
    "no" "$("${EGO}" session-state has may-write needs_input && echo yes || echo no)"

# ── F. the hook is installed ────────────────────────────────────────────────

section "F — C6a: setup installs Notification, async, and repairs an old install"

PYQ() { uv run --project "${WT}" python -c "$1" 2>&1; }

assert_eq "seven events are hooked" \
    "7" "$(PYQ 'from endless import setup; print(len(setup.CLAUDE_HOOK_EVENTS))')"
assert_eq "Notification is one of them" \
    "True" "$(PYQ 'from endless import setup; print("Notification" in setup.CLAUDE_HOOK_EVENTS)')"
assert_eq "and it is NOT synchronous — it records a state and gates nothing" \
    "False" "$(PYQ 'from endless import setup; print("Notification" in setup.SYNC_EVENTS)')"
assert_eq "an install carrying only the old six is repaired to seven" \
    "['Notification']" "$(PYQ '
from endless import setup
s = {"hooks": {e: [{"hooks": [{"type": "command",
                              "command": "/usr/local/bin/endless-go hook claude",
                              "async": e not in setup.SYNC_EVENTS}]}]
               for e in setup.CLAUDE_HOOK_EVENTS if e != "Notification"}}
assert setup._has_endless_hook(s), "precondition: setup would early-return"
print(setup._repair_missing_hook_events(s, "/usr/local/bin/endless-go"))')"

# ── G. end to end ───────────────────────────────────────────────────────────

section "G — C6b: the board surfaces both waiting states, prompted on top"

TMP="$(mktemp -d)" || setup_error "mktemp failed"
REPO="${TMP}/repo"
mkdir -p "${REPO}" "${TMP}/xdg/endless"
# The runner already isolated HOME and XDG_CONFIG_HOME; this narrows further to a
# database of this section's own making, and hands the runner's back before H so
# the regression layer runs in the environment the runner built for it.
SAVED_XDG_CONFIG="${XDG_CONFIG_HOME:-}"
SAVED_XDG_CACHE="${XDG_CACHE_HOME:-}"
export XDG_CONFIG_HOME="${TMP}/xdg"
export XDG_CACHE_HOME="${TMP}/cache"
export PATH="${WT}/bin:${PATH}"

git -C "${REPO}" init -q                         >/dev/null 2>&1
git -C "${REPO}" config user.email verify@test
git -C "${REPO}" config user.name verify
git -C "${REPO}" commit -q --allow-empty -m init >/dev/null 2>&1

E() { ( cd "${REPO}" && uv run --project "${WT}" endless "$@" ); }

E project register "${REPO}" --name probe --label Probe --desc d \
    --lang Go --status active >/dev/null 2>&1
PID="$(E sql "SELECT id FROM projects WHERE name='probe'" --tsv 2>/dev/null)"
[[ -n "${PID}" ]] || setup_error "could not register the throwaway project"

E sql "INSERT INTO tasks (id, project_id, title, status, phase) VALUES
   (8001,${PID},'Prompted session task','underway','now'),
   (8002,${PID},'Needs input session task','underway','now'),
   (8003,${PID},'Working session task','underway','now')" --write >/dev/null 2>&1

# Inserted so that the row that must sort FIRST is inserted LAST — the order can
# then only come from the ranking, never from insertion order. Activity
# timestamps ascend with the id for the same reason.
E sql "INSERT INTO sessions (id, session_id, project_id, state, task_id, started_at, last_activity) VALUES
   (8101,'uuid-8101-aaaaaaaa',${PID},'needs_input',8002,'2026-01-01T00:00:01','2026-01-01T00:00:01'),
   (8102,'uuid-8102-bbbbbbbb',${PID},'working',    8003,'2026-01-01T00:00:02','2026-01-01T00:00:02'),
   (8103,'uuid-8103-cccccccc',${PID},'prompted',   8001,'2026-01-01T00:00:03','2026-01-01T00:00:03')" \
   --write >/dev/null 2>&1

[[ "$(E sql "SELECT count(*) FROM sessions" --tsv 2>/dev/null)" == "3" ]] \
    || setup_error "the throwaway database was not seeded"

# --project-id is the headless seam (projectstatuscmd.resolveProject): the one
# entry point that does NOT pin the main database, so the board reads the
# throwaway config dir exported above. Driving `endless project status` instead
# would read the REAL database.
BOARD="$( cd "${REPO}" && "${EGO}" project-status --project-id "${PID}" --project probe 2>&1 )"

assert_contains "the prompted session is on the board" "E-8001" "${BOARD}"
assert_contains "the needs_input session is on the board, not suppressed" \
    "E-8002" "${BOARD}"
assert_contains "the working session is on the board" "E-8003" "${BOARD}"
assert_contains "both waiting rows carry the waiting glyph" \
    "⚠" "${BOARD}"
assert_eq "two rows are in the waiting rank" \
    "2" "$(printf '%s\n' "${BOARD}" | grep -c '^⚠ ')"
assert_eq "the waiting rank is above the working one" \
    "yes" "$([[ "$(printf '%s\n' "${BOARD}" | sed -n '2p' | cut -c1-1)" == "⚠" ]] \
        && echo yes || echo no)"
assert_contains "the header names the waiting rank first" \
    "⚠ waiting" "${BOARD}"

# An ended session must still be excluded: the filter widened to Live, not to
# everything.
E sql "UPDATE sessions SET state='ended' WHERE id=8102" --write >/dev/null 2>&1
BOARD_NO_ENDED="$( cd "${REPO}" && "${EGO}" project-status --project-id "${PID}" --project probe 2>&1 )"
assert_not_contains "an ended session is still excluded" "ES-8102" "${BOARD_NO_ENDED}"
E sql "UPDATE sessions SET state='working' WHERE id=8102" --write >/dev/null 2>&1

section "G — session list renders the new state"

LIST="$(E session list --all 2>&1)"
assert_contains "the legend documents prompted" "⚠ prompted" "${LIST}"
assert_eq "the prompted session is the first row" \
    "yes" "$(printf '%s\n' "${LIST}" | sed -n '5p' | grep -q '^8103  ⚠' && echo yes || echo no)"
FILTERED="$(E session list --all --state prompted 2>&1)"
assert_contains "--state prompted is accepted and selects the row" "8103" "${FILTERED}"
assert_not_contains "…and only that row" "8102" "${FILTERED}"

export XDG_CONFIG_HOME="${SAVED_XDG_CONFIG}"
export XDG_CACHE_HOME="${SAVED_XDG_CACHE}"

# ── H. project-wide regression ──────────────────────────────────────────────

section "H — project-wide regression"

assert_ok "go vet ./..." go vet ./...
assert_ok "go test ./... (the whole Go suite)" go test ./... -count=1
assert_ok "just test (the whole Python suite)" just test
assert_ok "just guide-check (the guide cross-reference is current)" just guide-check
assert_ok "just lifecycle-check (the status diagram has not drifted)" just lifecycle-check

summary
