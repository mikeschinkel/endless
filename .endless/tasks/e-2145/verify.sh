#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2145 and records what was true when E-2145
# landed. Edit it only if you ARE E-2145. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2145: end-of-turn handling was attached to ONE event when Claude Code has
# TWO.
#
# Everything Endless did when a turn ended hung off the `Stop` case in
# internal/hookcmd/claude.go — ParseTranscript, then IdleSession. Claude Code
# fires `StopFailure` INSTEAD OF `Stop` when a turn dies on an API error
# (rate_limit, overloaded, max_output_tokens, server_error and the rest), so a
# failed turn took none of that with it: the transcript went unparsed and the
# session never left `working`. Nothing else moved it — SessionEnd fires on
# session termination, TouchSession never clobbers a live state, and liveness
# sees a pane that is still there. `project monitor` then asserted work in flight
# on a dead turn, and auto-spawn's cap throttled against sessions that were gone.
#
# Installing the event alone would have been WORSE than not installing it:
# TouchSession runs before the event switch, so a bare install refreshes
# last_activity and leaves the session reading `working` with a timestamp
# vouching for it. The event is installed AND handled.
#
# What is verified here:
#   A. Fail-fast: this task's own tests pass — internal/hookcmd (the handler and
#      the dispatch), internal/monitor (the calls the handler leans on),
#      internal/faults (the two new codes and their documentation), and the
#      Python suite (setup.py's install list).
#   B. The defect, reproduced and named claim by claim — every one of these
#      failed before the fix, driven through runClaude rather than through the
#      handler, because the defect WAS an event falling through a switch.
#   C. Installing is not sufficient: the switch really carries a case, asserted
#      at the source so a future refactor that installs-without-handling is
#      caught by more than a behavioural test's absence.
#   D. The install half: hooked, NOT synchronous, and repaired into settings
#      that predate the event.
#   E. `Stop` is unchanged — the two cases were not merged behind a helper.
#   F. The catalog is real: the built binary prints both codes at the severities
#      the mapping assigns, and docs/errors.md documents each.
#
# See E-2145's plan (endless task show E-2145 --all-fields).
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

HOOK_GO="internal/hookcmd/claude.go"
HANDLER_GO="internal/hookcmd/stopfailure.go"
[[ -f "${HOOK_GO}" ]]    || setup_error "missing ${HOOK_GO}"
[[ -f "${HANDLER_GO}" ]] || setup_error "missing ${HANDLER_GO}"

# code_only strips Go comments so a source-level assertion reads the CODE and
# not a doc comment that happens to name the thing it is looking for. Three of
# the checks below are about something being ABSENT, and every one of those
# things is discussed at length in a comment sitting directly above the code
# that deliberately does not do it — which is exactly the shape that makes a
# naive grep report the opposite of the truth.
code_only() { sed -e 's://.*::' "$@"; }

has_code() { # has_code <pattern> <file>...
    local pattern="$1"; shift
    code_only "$@" | grep -q "${pattern}"
}

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# internal/hookcmd holds the handler, the payload field and the dispatch case.
# internal/monitor owns IdleSession and ParseTranscript — the two calls the
# handler makes, and the ones whose contract a failed turn now depends on.
# internal/faults is ERR-0013/ERR-0014 plus the catalog-matches-docs gate that a
# new code cannot ship past undocumented. The Python suite covers setup.py,
# where the event is installed; it runs in full because the install list is read
# by more of that file than the setup tests alone touch.

go_pkg() { # go_pkg <package> <label>
    if out=$(go test "$1" -count=1 2>&1); then
        report_pass "go test: $2"
    else
        report_fail "go test: $2" "exit 0" "$(printf '%s' "${out}" | tail -30)"
        summary
    fi
}

go_pkg ./internal/hookcmd/ "internal/hookcmd — the handler and the dispatch"
go_pkg ./internal/monitor/ "internal/monitor — IdleSession and ParseTranscript"
go_pkg ./internal/faults/  "internal/faults — the two new codes, documented"

if out=$(uv run pytest -q tests/ 2>&1); then
    report_pass "uv run pytest tests/ — the Python suite"
else
    report_fail "uv run pytest tests/ — the Python suite" "exit 0" \
        "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# ---------------------------------------------------------------------------
section "B. The defect, reproduced — every claim failed before the fix"
# ---------------------------------------------------------------------------
# Section A already ran these. They are re-run by name so this report states
# each claim rather than collapsing them into one green package. Each drives a
# synthetic StopFailure payload through runClaude — stdin, parse, dispatch —
# because a test calling the handler directly would have passed the day before
# the fix by exercising a function that did not exist.

go_claim() { # go_claim <package> <test-name> <claim>
    if out=$(go test "$1" -run "^$2\$" -count=1 2>&1); then
        report_pass "$3"
    else
        report_fail "$3" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    fi
}

hook() { go_claim ./internal/hookcmd/ "$1" "$2"; }

hook TestStopFailure_IdlesASessionLeftWorkingByADeadTurn \
    "a turn that dies on an API error leaves the session idle, not working"
hook TestStopFailure_DoesNotLeaveAFreshTimestampOnALiveState \
    "and the refreshed last_activity is honest, because the state moved with it"
hook TestStopFailure_RecordsTheFailureAsAFault \
    "the reason is not thrown away: one fault, sourced hook:stopfailure, naming the error type"
hook TestStopFailure_SeverityFollowsTheErrorType \
    "severity follows the error type — four need a person, everything else is transient"
hook TestStopFailure_AnAbsentErrorTypeIsRecordedAsUnknown \
    "a payload naming no error type is still a failed turn, recorded as unknown"
hook TestStopFailure_RepeatsOfOneErrorTypeAreOneIncident \
    "twenty rate limits are ONE incident with twenty occurrences, not twenty rows"
hook TestStopFailure_DifferentErrorTypesAreDifferentIncidents \
    "and a billing failure never collapses into a rate limit"
hook TestTurnFailureCode_IsTotal \
    "every error type maps to a real catalog entry, including one nobody has seen"

# ---------------------------------------------------------------------------
section "C. Installing is not enough — the event is HANDLED"
# ---------------------------------------------------------------------------
# The switch in claude.go has no `default`, so an installed-but-unhandled event
# is a process spawned per failed turn that does nothing — while TouchSession,
# which runs before the switch, refreshes the timestamp on the way past. A check
# that only read CLAUDE_HOOK_EVENTS would grade that build correct.

if has_code 'case "StopFailure":' "${HOOK_GO}"; then
    report_pass "the event switch carries a case \"StopFailure\""
else
    report_fail "the event switch carries a case \"StopFailure\"" \
        "a case in ${HOOK_GO}" "no case — the event would fall through"
fi

if has_code 'handleStopFailure' "${HOOK_GO}"; then
    report_pass "and that case dispatches to handleStopFailure"
else
    report_fail "and that case dispatches to handleStopFailure" \
        "a call in ${HOOK_GO}" "the case does not reach the handler"
fi

if has_code 'json:"error_type' "${HOOK_GO}"; then
    report_pass "the payload carries error_type — the field the event's matcher filters on"
else
    report_fail "the payload carries error_type" \
        'a json:"error_type" tag on claudePayload' "no such field"
fi

# The two calls a failed turn must take with it, and the one it must not.
if has_code 'monitor.ParseTranscript' "${HANDLER_GO}"; then
    report_pass "the handler parses the transcript the dead turn produced"
else
    report_fail "the handler parses the transcript the dead turn produced" \
        "monitor.ParseTranscript in ${HANDLER_GO}" "absent"
fi
if has_code 'monitor.IdleSession' "${HANDLER_GO}"; then
    report_pass "and idles the session"
else
    report_fail "and idles the session" "monitor.IdleSession in ${HANDLER_GO}" "absent"
fi
if has_code 'enforceReportGate' "${HANDLER_GO}"; then
    report_fail "the report gate is NOT carried over from Stop" \
        "no enforceReportGate in ${HANDLER_GO}" \
        "the gate is there — a turn that died has no final message to judge, and this event cannot block"
else
    report_pass "the report gate is NOT carried over from Stop — nothing to judge, and it cannot block"
fi

# ---------------------------------------------------------------------------
section "D. The install half"
# ---------------------------------------------------------------------------
# Hooked or nothing fires at all; async because Claude Code does not read this
# hook's output on any exit code; and repaired into an install that predates the
# event, or every machine already running Endless keeps the bug forever while
# reporting itself correctly set up.

# Written to a file rather than passed to `python -c`: every assertion here is
# about string membership in a Python list, so the expression is full of single
# quotes and there is no quoting of an inline -c that survives them.
py_claim() { # py_claim <expression> <claim>
    local script
    script="$(mktemp)" || setup_error "mktemp failed"
    {
        # 'src' first, so this asserts against THIS worktree's setup.py rather
        # than whatever version the venv happens to have installed.
        printf '%s\n' "import sys" "sys.path.insert(0, 'src')" \
                       "from endless import setup"
        printf 'assert %s\n' "$1"
    } >"${script}"
    if out=$(uv run python "${script}" 2>&1); then
        report_pass "$2"
    else
        report_fail "$2" "the assertion holds" "$(printf '%s' "${out}" | tail -10)"
    fi
    rm -f "${script}"
}

py_claim "'StopFailure' in setup.CLAUDE_HOOK_EVENTS" \
    "setup.py installs StopFailure"
py_claim "'StopFailure' not in setup.SYNC_EVENTS" \
    "and installs it async — Claude Code reads no output from it on any exit code"
py_claim "'Stop' in setup.CLAUDE_HOOK_EVENTS and 'Stop' in setup.SYNC_EVENTS" \
    "Stop is still installed, and still synchronous for the relay gate"

if out=$(uv run pytest -q tests/test_setup_stopfailure_hook.py 2>&1); then
    report_pass "an install predating the event is repaired to include it"
else
    report_fail "an install predating the event is repaired to include it" \
        "exit 0" "$(printf '%s' "${out}" | tail -20)"
fi

# ---------------------------------------------------------------------------
section "E. Stop is unchanged"
# ---------------------------------------------------------------------------
# The two cases stay separate rather than merging behind a helper taking a
# `failed bool`, which would put the difference inside a function whose name
# claimed the cases were the same. The witness is that Stop's own path is
# untouched and its tests pass.

if has_code 'enforceReportGate(projectID, isRegistered, payload)' "${HOOK_GO}"; then
    report_pass "Stop still runs the report gate before idling"
else
    report_fail "Stop still runs the report gate before idling" \
        "enforceReportGate on the Stop path" "gone — Stop's own handling changed"
fi

if ! has_code 'failed bool\|isFailure bool' "${HOOK_GO}" "${HANDLER_GO}"; then
    report_pass "and the cases were not merged behind a failed-bool helper"
else
    report_fail "and the cases were not merged behind a failed-bool helper" \
        "no boolean-parameterised end-of-turn helper" "one exists"
fi

hook TestNotification_StopFromPromptedEndsIdle \
    "Stop's own end-of-turn behaviour still holds from a prompted session"

# ---------------------------------------------------------------------------
section "F. The catalog, from the built binary"
# ---------------------------------------------------------------------------
# Severity here buys PROMINENCE, not longevity: the fault badge renders one line
# and the most severe open incident wins it. Two codes rather than one whose
# severity varies with its payload — severity is a property of the Code, and a
# code that was sometimes yellow and sometimes red would be invisible in both
# the catalog and this listing.

if [[ ! -x bin/endless-go ]]; then
    report_skip "the built binary prints both codes" "bin/endless-go not built — run just build"
else
    codes="$(./bin/endless-go errors codes 2>&1)"
    assert_contains "ERR-0013 is a warning: turn-failed-transient" \
        "ERR-0013  warning   turn-failed-transient" "${codes}"
    assert_contains "ERR-0014 is an error: turn-failed-fatal" \
        "ERR-0014  error     turn-failed-fatal" "${codes}"
fi

docs="$(cat docs/errors.md)"
assert_contains "docs/errors.md documents ERR-0013" \
    "## ERR-0013 — turn-failed-transient" "${docs}"
assert_contains "docs/errors.md documents ERR-0014" \
    "## ERR-0014 — turn-failed-fatal" "${docs}"

# The four that need a person, named in the source so the mapping is readable
# where it is decided rather than only where it is tested.
for t in authentication_failed billing_error oauth_org_not_allowed account_on_hold; do
    if has_code "\"${t}\": *true" "${HANDLER_GO}"; then
        report_pass "${t} is classified as needing a person"
    else
        report_fail "${t} is classified as needing a person" \
            "a fatalTurnErrorTypes entry" "absent from ${HANDLER_GO}"
    fi
done

summary
