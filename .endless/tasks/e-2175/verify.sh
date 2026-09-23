#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2175 and records what was true when E-2175
# landed. Edit it only if you ARE E-2175. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2175: obsoleting a task must say why, as declining already does.
#
# Of the three ways a task can be abandoned, `obsolete` was the only one
# recording nothing. Measured against the live ledger on 2026-09-23:
#
#     status                total   no reason   span of the reasonless rows
#     declined (guarded)       74          32   2026-06-10 → 2026-06-10
#     obsolete (unguarded)    223          74   2026-06-10 → 2026-09-23
#
# Every reasonless `declined` row is from the seed import; since ED-1022
# covered all THREE of decline's call sites, not one has leaked in three and a
# half months. `obsolete` produced one the day this task was filed.
#
# What is verified here:
#   A. Fail-fast: this task's own tests, plus the landed suites whose
#      `obsolete` transitions this guard now sits in front of.
#   B. The claims, named one by one — every route refused, and the one
#      exemption that falls out of guarding the transition rather than a verb.
#   C. The guard is keyed on the STATUS, not on a verb: it fires for exactly
#      two of the vocabulary's statuses and is reachable from exactly the three
#      front doors. Single-site enforcement is the failure this task exists to
#      prevent, so that is asserted structurally, not by reading the diff.
#   D. The refusal is actionable and the help text tells the truth — a
#      mandatory field whose refusal does not name the flag that satisfies it
#      is how junk values get typed.
#
# See E-2175's plan (endless task show E-2175 --all-fields).
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# test_outcome.py is where the guard has always been covered, and is where the
# new coverage went. The other four are landed suites that drove a task to
# `obsolete` without a reason — they are the population this change had to stop
# breaking, so they are re-run here rather than taken on faith.

py_file() { # py_file <path> <label>
    if out=$(uv run pytest -q "$1" 2>&1); then
        report_pass "pytest: $2"
    else
        report_fail "pytest: $2" "exit 0" "$(printf '%s' "${out}" | tail -30)"
        summary
    fi
}

py_file tests/test_outcome.py               "the guard, and every route to it"
py_file tests/test_replaced_by_inline.py    "replace/obsolete on shipped and unshipped work"
py_file tests/test_research_gate.py         "the type gate's universal terminals"
py_file tests/test_type_status_gate.py      "obsolete still allowed for every type"
py_file tests/test_status_lifecycle_gate.py "a retired status can still escape"

# ---------------------------------------------------------------------------
section "B. The claims only visible from inside, named"
# ---------------------------------------------------------------------------
# Section A already ran these. They are re-run by name so this report states
# each claim rather than collapsing all of them into five green files.

py_claim() { # py_claim <file::test> <claim>
    if out=$(uv run pytest -q "$1" 2>&1); then
        report_pass "$2"
    else
        report_fail "$2" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    fi
}

T=tests/test_outcome.py

py_claim "${T}::test_task_update_status_obsolete_requires_outcome" \
    "task update --status obsolete with no outcome is refused, naming the flag"
py_claim "${T}::test_task_update_status_obsolete_blank_outcome_rejected" \
    "whitespace does not satisfy it — a blank reason is no reason"
py_claim "${T}::test_task_update_status_obsolete_with_outcome_stores_it" \
    "the same call WITH an outcome succeeds and stores it"
py_claim "${T}::test_epic_update_status_obsolete_requires_outcome" \
    "epic update inherits the guard rather than needing one of its own"
py_claim "${T}::test_task_replace_with_status_obsolete_requires_outcome" \
    "task replace --status obsolete is refused: replaced_by says WHAT, not WHY"
py_claim "${T}::test_task_replace_with_status_obsolete_and_outcome" \
    "and succeeds with one, recording both the relation and the reason"
py_claim "${T}::test_task_replace_on_shipped_work_needs_no_outcome" \
    "the one exemption is structural: shipped work keeps its terminal, never reaching obsolete"
py_claim "${T}::test_task_replace_default_superseded_needs_no_outcome" \
    "superseded is untouched — it is guarded by the relation it names"
py_claim "${T}::test_existing_reasonless_obsolete_rows_still_read" \
    "pre-existing reasonless rows still show, list and serialize — the guard is on the transition"
py_claim "${T}::test_task_decline_writes_outcome" \
    "task decline is unchanged — still stores its reason as the outcome"
py_claim "${T}::test_task_decline_blank_reason_rejected" \
    "and still refuses a blank one"

# ---------------------------------------------------------------------------
section "C. Keyed on the status, not on a verb — enumerated, not eyeballed"
# ---------------------------------------------------------------------------
# A second guard bolted to one command would leak the moment someone reached
# `obsolete` by another route, which is exactly how `obsolete` accumulated 74
# reasonless rows while `declined` accumulated none. These assertions read the
# source, so the next route added is a test failure rather than a hole.

py_claim "${T}::test_the_guard_fires_for_exactly_two_statuses" \
    "the guard fires for declined and obsolete, and for no other status"
py_claim "${T}::test_the_guard_is_called_from_exactly_the_expected_front_doors" \
    "reachable from exactly update_plan, replace_task and decline_item"
py_claim "${T}::test_task_cmd_status_emitters_are_pinned_or_guarded" \
    "every status-change emitter in task_cmd is pinned to a safe literal or guarded"
py_claim "${T}::test_triage_can_never_reach_an_abandonment_status" \
    "triage's verdict vocabulary cannot name an abandonment status"
py_claim "${T}::test_session_cmd_only_ever_emits_pinned_non_abandonment_statuses" \
    "session_cmd's status emissions are pinned literals, none of them abandonment"

# The old name is gone, not aliased: a surviving `_require_outcome_for_declined`
# would mean a caller still routes through a declined-only check.
if grep -rqn "_require_outcome_for_declined" src/ tests/; then
    report_fail "the declined-only guard name is gone from src/ and tests/" \
        "no matches" "$(grep -rn '_require_outcome_for_declined' src/ tests/ | head -5)"
else
    report_pass "the declined-only guard name is gone from src/ and tests/"
fi

# ---------------------------------------------------------------------------
section "D. The refusal and the help text tell the truth"
# ---------------------------------------------------------------------------
# Obsoleting is bulk backlog grooming, so this is the mandatory field most
# likely to be met at speed. A refusal that does not name the flag that
# satisfies it is how "n/a" gets typed into it.

refusal() { # refusal <status>
    uv run python -c "
import click
from endless import task_cmd
try:
    task_cmd._require_outcome_for_abandonment('$1', None)
except click.ClickException as e:
    print(e.message)
" 2>/dev/null
}

OBS="$(refusal obsolete)"
DEC="$(refusal declined)"
[[ -n "${OBS}" && -n "${DEC}" ]] || setup_error "the guard did not refuse either status"

assert_contains "obsoleting is refused in the caller's own words" \
    "obsoleting a task" "${OBS}"
assert_contains "and the refusal names the flag that satisfies it" \
    "--outcome" "${OBS}"
assert_contains "and the file form, for a reason longer than a shell argument" \
    "--outcome-file" "${OBS}"
assert_contains "declining still names its own flag, which is spelled differently" \
    "--reason" "${DEC}"
assert_not_contains "and obsolete does NOT offer --reason: there is no task obsolete verb" \
    "--reason" "${OBS}"

help_for() { # help_for <subcommand...>
    uv run endless "$@" --help 2>&1
}

UPDATE_HELP="$(help_for task update)"
REPLACE_HELP="$(help_for task replace)"
EPIC_HELP="$(help_for epic update)"
[[ -n "${UPDATE_HELP}" ]] || setup_error "could not read \`task update --help\`"

assert_contains "task update's --outcome says obsolete requires it" \
    "obsolete" "$(printf '%s' "${UPDATE_HELP}" | grep -A2 -- '--outcome ')"
assert_contains "epic update's does too" \
    "obsolete" "$(printf '%s' "${EPIC_HELP}" | grep -A2 -- '--outcome ')"
assert_contains "task replace's does too" \
    "obsolete" "$(printf '%s' "${REPLACE_HELP}" | grep -A2 -- '--outcome ')"
# E-2144 moved the unshipped default to `superseded`; this help string still
# said 'obsolete', and a reader sent here by the new requirement would have
# been told the wrong default. Fixed as part of this change.
assert_contains "and task replace no longer claims obsolete is its default status" \
    "superseded" "$(printf '%s' "${REPLACE_HELP}" | grep -A4 -- '--status ')"

assert_contains "the guide's status table states the requirement" \
    "Requires a reason" "$(grep '^| `obsolete`' docs/guide/index.md)"

summary
