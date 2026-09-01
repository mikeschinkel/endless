#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2097 and records what was true when E-2097
# landed. Edit it only if you ARE E-2097. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2097: a refusal's verdict was line 2 of 17. An agent piping
# `2>&1 | tail -3` kept the tail of the guidance block and never saw the one
# line naming the constraint — measured across five refusals in a single
# session, one of which shaved 34 characters off a title without ever reading
# the `107>100` the error had stated twice, and one of which wrote a junk
# value to a LIVE task to probe the validator.
#
# The fix: when an agent is reading (or a human asked to see what one sees
# with --agent-view), a refusal repeats a dense one-line verdict as its FIRST
# and LAST line, identical, with today's guidance unchanged between them.
#
# What is verified here:
#   A. Fail-fast: this task's own tests, plus the neighbours it touched.
#   B. The property, through the real CLI and a real truncating pipe: head -3
#      and tail -3 of the same refusal EACH keep the verdict, and the first
#      and last lines are byte-identical.
#   C. The verdict carries what would have stopped the failure — measured
#      numbers, the command, where the overflow goes, and whether anything
#      changed.
#   D. A human sees today's message byte for byte. The expected text below was
#      captured from the pre-change build; the duplication is agent-only.
#   E. --agent-view renders the bracket for a human who asked for it.
#   F. Title and description violations are named in ONE verdict, once.
#
# See .endless/plans/E-2097.md.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

# A 107-character title: the exact overflow from the measured failure.
LONG_TITLE="Add $(printf 'x%.0s' $(seq 1 103))"
[[ ${#LONG_TITLE} -eq 107 ]] || setup_error "fixture title is ${#LONG_TITLE} chars, expected 107"
LONG_DESC="$(printf 'd%.0s' $(seq 1 1032))"

# The marker that makes one line identifiable as an Endless refusal out of
# context. Not "ERROR" — Click already prints that, and a bare ERROR: greps to
# noise; not the command alone — `task add` is Taskwarrior's verb too.
SENTINEL="[Endless]"

# The refusal as an agent meets it, and as a human meets it. Both go through
# the real binary in a real process: the property is about what lands on the
# terminal, and only a rendered run can see Click's own `Error: ` prefix.
agent_run() { env CLAUDE_CODE_ENTRYPOINT=cli uv run --quiet endless "$@" 2>&1; }
human_run() {
    env -u CLAUDE_CODE_ENTRYPOINT -u __CFBundleIdentifier -u CLAUDE_AGENT_SDK_VERSION \
        uv run --quiet endless "$@" 2>&1
}

# Warm the interpreter once, so a first-run message from the launcher cannot
# land inside a `head -3` window below and be read as the refusal's first line.
uv run --quiet endless --version >/dev/null 2>&1 \
    || setup_error "cannot run the worktree's endless"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# First and fail-fast, so the one command is a complete proof rather than a
# spot check bolted onto a green build somebody else made. The neighbours are
# here because this task moved the predicate they gate on: the refusal now
# shares `agent_help.agent_facing` with `--help` and the wind-down nudge, and
# `task_cmd._running_under_agent` is gone.

if out=$(uv run pytest -q \
        tests/test_agent_error_bracket.py \
        tests/test_validate_title_length.py \
        tests/test_validate_description.py \
        tests/test_verb_gate.py \
        tests/test_agent_env.py \
        tests/test_report_reminder.py 2>&1); then
    report_pass "pytest: bracket, validators, verb gate, agent env, report nudge"
else
    report_fail "pytest: bracket, validators, verb gate, agent env, report nudge" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# ---------------------------------------------------------------------------
section "B. A truncating pipe keeps the verdict, from either end"
# ---------------------------------------------------------------------------
# This is the failure, reproduced: `tail -3` is what the session that motivated
# this task actually typed.

head3="$(agent_run task add "${LONG_TITLE}" | head -3)"
tail3="$(agent_run task add "${LONG_TITLE}" | tail -3)"
assert_contains "head -3 keeps the verdict" "${SENTINEL}" "${head3}"
assert_contains "tail -3 keeps the verdict" "${SENTINEL}" "${tail3}"

full="$(agent_run task add "${LONG_TITLE}")"
first_line="$(printf '%s\n' "${full}" | head -1)"
last_line="$(printf '%s\n' "${full}" | tail -1)"
assert_eq "first and last lines are byte-identical" "${first_line}" "${last_line}"

# Identical, not split. Split them — problem first, remedy last — and head -N
# yields the problem without the fix while tail -N yields the fix without the
# problem.
verdict_lines="$(printf '%s\n' "${full}" | grep -cF "${SENTINEL}")"
assert_eq "the verdict appears exactly twice, and nowhere else" "2" "${verdict_lines}"

# The guidance is unchanged BETWEEN the brackets — the bracket adds, it does
# not replace.
assert_contains "the existing guidance survives between the brackets" \
    "Consider using this template:" "${full}"

# ---------------------------------------------------------------------------
section "C. What the verdict line carries"
# ---------------------------------------------------------------------------
# Ordered by what would have stopped the measured failure at attempt 3.

assert_contains "the measured numbers, not an adjective" \
    "title 107>100 chars" "${first_line}"
assert_contains "a fixed sentinel plus the command" \
    "${SENTINEL} task add:" "${first_line}"
assert_contains "where the overflow goes (--analysis)" "--analysis" "${first_line}"
assert_contains "where the overflow goes (--text)" "--text" "${first_line}"
assert_contains "whether anything changed" "Nothing was created." "${first_line}"

# One dense line, not a paragraph: a paragraph invites the same skimming that
# produced the failure.
assert_eq "the verdict is one line" "1" \
    "$(printf '%s\n' "${first_line}" | wc -l | tr -d ' ')"

# The other verb, `task update`, names ITSELF and its own no-change statement
# ("Nothing was changed."), so a verdict read out of context still says which
# call refused. That one is asserted in the pytest gate above
# (test_update_names_its_own_verb_and_says_nothing_changed) rather than here:
# reaching update's validators needs a task row, and `task update` hits the
# database before it validates, so a shell run would either refuse for want of
# `--db` or have to write a throwaway task into the sandbox to have something
# to refuse about.

# A refused call leaves no trace of a write. The DB-level assertion lives in
# tests/test_agent_error_bracket.py; here it is the observable one.
assert_not_contains "a refused add never reports adding anything" \
    "Added E-" "${full}"

# ---------------------------------------------------------------------------
section "D. A human sees today's message, byte for byte"
# ---------------------------------------------------------------------------
# The expected text below was captured from the build BEFORE this change. A
# repeated long line is noise to a reader who was never going to truncate it,
# so the duplication is agent-only by design — and this is the assertion that
# keeps it that way.

expected_human=$(cat <<'GOLDEN'

Error: Title is 107 characters; max is 100.

If it does not fit in 100 chars, the title is usually naming HOW instead
of WHAT. Long-form belongs elsewhere: analysis in --analysis, design/plan in
--text, a brief blurb in --description — not the title.

Consider using this template:

    Shape: <verb> <subject>'s <symptom> on/when <trigger> [via <mechanism>]
    Subject   = user-facing name (e.g. 'just land'), not internal symbol
    Symptom   = what the user observes breaking (e.g. 'recording failure'),
                not the implementation cause
    Trigger   = when the symptom shows up (e.g. 'on self-modifying branches')
    Mechanism = optional; the flag/verb that fixes it (e.g. 'via --no-record').
                Include only when it sharpens understanding.
GOLDEN
)
actual_human="$(human_run task add "${LONG_TITLE}")"
assert_eq "the human title refusal is unchanged from before this task" \
    "${expected_human}" "${actual_human}"

expected_desc=$(cat <<'GOLDEN'
Error: Description is 1032 characters; max is 1024.
  Description is a 2-3 sentence blurb, not a dissertation. Long-form context
  belongs in a dedicated field: analysis in --analysis, plans/verification in --text.
GOLDEN
)
actual_desc="$(human_run task add "Add a short title" --description "${LONG_DESC}")"
assert_eq "the human description refusal is unchanged from before this task" \
    "${expected_desc}" "${actual_desc}"

# ---------------------------------------------------------------------------
section "E. --agent-view shows a human what an agent sees"
# ---------------------------------------------------------------------------
# The flag is a human's override for previewing agent-facing output while
# debugging, not a detection mechanism. A version firing on detection alone
# would make the refusal the one agent-facing output --agent-view cannot show.

view="$(human_run task add "${LONG_TITLE}" --agent-view)"
view_first="$(printf '%s\n' "${view}" | head -1)"
view_last="$(printf '%s\n' "${view}" | tail -1)"
assert_contains "--agent-view renders the verdict" "${SENTINEL}" "${view_first}"
assert_eq "--agent-view brackets both ends" "${view_first}" "${view_last}"

# ---------------------------------------------------------------------------
section "F. Every problem at once, in one refusal"
# ---------------------------------------------------------------------------
# The validators used to raise on the first failure, so a call that was wrong
# in two ways cost two round trips to learn — steps 1-2 and 3-6 of the measured
# failure were both that shape.

both="$(agent_run task add "${LONG_TITLE}" --description "${LONG_DESC}")"
both_first="$(printf '%s\n' "${both}" | head -1)"
assert_contains "the title overflow is named" "title 107>100 chars" "${both_first}"
assert_contains "the description overflow is named" \
    "description 1032>1024 chars" "${both_first}"
assert_eq "still exactly one refusal, bracketed once at each end" "2" \
    "$(printf '%s\n' "${both}" | grep -cF "${SENTINEL}")"
assert_contains "both guidance blocks are present (title)" \
    "Consider using this template:" "${both}"
assert_contains "both guidance blocks are present (description)" \
    "not a dissertation" "${both}"

summary
