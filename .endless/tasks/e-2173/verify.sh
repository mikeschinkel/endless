#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2173 and records what was true when E-2173
# landed. Edit it only if you ARE E-2173. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2173: a top-level `touch` that puts a task in the session's scope.
#
# WHAT LANDED
#   There was no way to add a task to session_tasks — to make it appear in
#   `session status` — without editing the task itself. The workaround in use
#   was to rewrite a field that did not need rewriting, most often `phase`,
#   purely for the capture side effect. That wrote a real field change into the
#   ledger to buy a display effect: the task's recorded phase stopped meaning
#   what it said, a later reader could not tell a deliberate re-prioritization
#   from a side-channel, and every other session holding the task got a change
#   notice for a change nobody made.
#
#   `endless touch E-NNN ...` is the verb whose whole job is scope entry. It
#   emits one new event kind, `session_tasks.touched`, which enrolls each named
#   task at relation `revisited` through the same upgrade-only ladder every
#   automatic capture obeys. Nothing about the task changes.
#
#   It is TOP-LEVEL, not `session touch` or `task touch`: many subcommands are
#   slated to move to top level, so placing it under a parent now would only
#   schedule a rename.
#
#   `session task add` (E-1696) was refactored onto the same shared body,
#   enrollSessionTasks, because the two verbs differ in exactly one thing —
#   the relation they offer the ladder — and that difference is the whole of
#   each verb's meaning.
#
# THE CLAIMS (this suite checks these, not the plumbing)
#   C1  THE VERB EXISTS, AT THE TOP LEVEL. `endless touch` is reachable as a
#       root command and is not parked under `session` or `task`.
#   C2  IT PUTS THE TASK IN SCOPE. A touched task gets a session_tasks row at
#       relation `revisited` for the emitting session.
#   C3  IT CHANGES NOTHING ELSE. The task row — title, status, phase,
#       updated_at — is byte-identical after a touch. This is the claim the
#       task exists for; C2 alone would pass for the workaround too.
#   C4  THE LADDER RUNS BOTH WAYS. `revisited` is the weakest relation any
#       emitter produces, so a stored `claimed`, `queued` or `surfaced`
#       survives a touch (and the report names which one), while a weaker
#       `referenced` is upgraded.
#   C5  A TYPO FAILS THE CALL. An id naming no live task refuses the whole
#       call rather than silently enrolling nothing, and the refusal names
#       `touch` rather than the `session task` group it shares a module with.
#   C6  THE KIND IS REGISTERED EVERYWHERE IT MUST BE. A kind that is declared
#       but not in ValidKinds is refused at the door by `endless-go event`;
#       one in ValidKinds with no dispatch arm reaches a dead switch. Both
#       failures are invisible until someone runs the verb.
#   C7  `session task add` STILL BEHAVES EXACTLY AS IT DID. It moved onto
#       shared code; it did not change.
#   C8  IT IS DOCUMENTED. `touch` resolves to a guide section rather than
#       landing in the coverage-gap list, and the sessions guide says what it
#       is for.
#
# ISOLATION
#   Nothing here touches the main database or the real ledger. Every check is
#   a package test, a pytest against the runner's temp HOME and
#   XDG_CONFIG_HOME, or a read of tracked source.
#
# Layers:
#   A. FAIL-FAST — this task's own unit tests, Go and Python. Nothing below is
#      meaningful if the behaviour they pin is broken.
#   B. The verb exists, at the top level — C1.
#   C. Scope entry, and nothing else — C2, C3.
#   D. The ladder, both directions — C4.
#   E. The refusal — C5.
#   F. The kind is wired end to end — C6.
#   G. The verb it shares a body with is unchanged — C7.
#   H. Documented — C8.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 on any
# failure, 2 on a setup problem.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

# Outside the repository on purpose: a scratch file inside .endless/ is the
# 0-byte ghost `endless worktree land` refuses over.
TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

# The Python tests drive the real Go executors through the real CLI, so a stale
# binary would fail them with "unknown event kind" and say nothing about the
# source. Build first and fail loudly if it cannot.
go build -o "${TMP}/endless-go" ./cmd/endless-go 2>"${TMP}/build.txt" \
    || setup_error "cannot build endless-go: $(tail -5 "${TMP}/build.txt")"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# Run first: they are the detailed proof, and everything below is the
# claim-level statement of what they establish.

if go test ./internal/events/ -run 'TestSessionTasks' >"${TMP}/go-events.txt" 2>&1; then
    report_pass "Go: the membership executors, touched and queued alike"
else
    report_fail "Go: the membership executors, touched and queued alike" \
        "go test passes" "$(tail -20 "${TMP}/go-events.txt")"
fi

if uv run pytest tests/test_session_task_membership.py -q \
        >"${TMP}/py-all.txt" 2>&1; then
    report_pass "Python: the three verbs end to end, through the real CLI"
else
    report_fail "Python: the three verbs end to end, through the real CLI" \
        "pytest passes" "$(tail -20 "${TMP}/py-all.txt")"
fi

# Per-claim helpers. Each claim gets its own line rather than one "the tests
# passed", so a failure names the property that broke.
py_check() {
    local label="$1" test_name="$2"
    if uv run pytest "tests/test_session_task_membership.py::${test_name}" -q \
            >"${TMP}/py-${test_name}.txt" 2>&1; then
        report_pass "${label}"
    else
        report_fail "${label}" "pytest passes" "$(tail -15 "${TMP}/py-${test_name}.txt")"
    fi
}

go_check() {
    local label="$1" test_name="$2"
    if go test ./internal/events/ -run "^${test_name}\$" -count=1 \
            >"${TMP}/go-${test_name}.txt" 2>&1; then
        report_pass "${label}"
    else
        report_fail "${label}" "go test passes" "$(tail -15 "${TMP}/go-${test_name}.txt")"
    fi
}

# ---------------------------------------------------------------------------
section "B. The verb exists, at the top level — C1"
# ---------------------------------------------------------------------------

py_check "touch is a root command, not a subcommand of task" \
    test_touch_is_reachable_at_the_top_level

# The help text is what a reader meets first, and it is the only place that
# says WHY the verb exists rather than what it does. Rendered through the real
# Click tree, which needs no database.
HELP="$(uv run python -c '
from click.testing import CliRunner
from endless.cli import main
print(CliRunner().invoke(main, ["touch", "--help"]).output)
' 2>"${TMP}/help-err.txt")" || setup_error "cannot render touch --help: $(tail -5 "${TMP}/help-err.txt")"

assert_contains "its help says the task itself is untouched" \
    "The task itself is untouched" "${HELP}"
assert_contains "its help names the relation it writes" "revisited" "${HELP}"
assert_contains "its help names the workaround it replaces" "phase" "${HELP}"
assert_contains "its help points at session task add for decided work" \
    "session task add" "${HELP}"

# ---------------------------------------------------------------------------
section "C. Scope entry, and nothing else — C2, C3"
# ---------------------------------------------------------------------------

py_check "a touched task joins session_tasks as revisited" \
    test_touch_enrolls_a_task_as_revisited
py_check "and the task row is unchanged — the point of the verb" \
    test_touch_changes_nothing_about_the_task
py_check "several ids in one call, repeats collapsed" \
    test_touch_accepts_several_ids_and_collapses_repeats
go_check "Go: enrollment writes the row and leaves the task alone" \
    TestSessionTasksTouched_EnrollsWithoutTouchingTheTask

# The structural half of C3. The Python test proves this tree does not mutate
# the task; this proves there is nothing in the emit path that could. `touch`
# emits one kind and one kind only — a `task.*` emission here would be the
# workaround wearing the new verb's name.
TOUCH_SRC="$(sed -n '/^def touch(/,/^def /p' src/endless/session_task_cmd.py)"
assert_contains "the emitter sends session_tasks.touched" \
    "session_tasks.touched" "${TOUCH_SRC}"
assert_not_contains "and emits no task event of its own" "task." "${TOUCH_SRC}"

# ---------------------------------------------------------------------------
section "D. The ladder, both directions — C4"
# ---------------------------------------------------------------------------
# `revisited` is the weakest relation any emitter produces, so THREE relations
# outrank it — and a demotion would lose a claim, an agenda entry or a filing
# record. The report has to name the survivor, or the reader cannot tell a
# claim from an incidental capture.

go_check "a stronger stored relation survives a touch, and is named" \
    TestSessionTasksTouched_LeavesStrongerRelationsAlone
go_check "a weaker referenced capture is upgraded by an explicit touch" \
    TestSessionTasksTouched_UpgradesReferenced
py_check "the CLI reports the survivor by label, not just 'already here'" \
    test_touch_leaves_a_stronger_relation_alone_and_says_which

# The ladder itself is the enum's, not this verb's: `touch` offers a relation
# and upsertSessionTask decides. That is why there is no rank comparison in the
# executor beyond the one shared helper.
MEMBERSHIP="$(grep -v '^[[:space:]]*//' internal/events/session_task_membership.go)"
assert_contains "enrollment defers to Outranks rather than a hand-rolled test" \
    "stored.Outranks(rel)" "${MEMBERSHIP}"

# ---------------------------------------------------------------------------
section "E. The refusal — C5"
# ---------------------------------------------------------------------------

py_check "an unknown id fails the whole call" test_touch_rejects_an_unknown_task
py_check "a malformed id is refused, naming touch and not session task" \
    test_touch_rejects_a_malformed_id_naming_the_verb
py_check "naming no task at all is refused" test_touch_requires_at_least_one_id
go_check "Go: one bad id rolls the whole call back" \
    TestSessionTasksTouched_RejectsUnknownTask

# ---------------------------------------------------------------------------
section "F. The kind is wired end to end — C6"
# ---------------------------------------------------------------------------
# Three registration points, each silently fatal on its own: the constant, the
# ValidKinds gate `endless-go event` refuses unknown kinds at, and the dispatch
# arm. A kind missing from the gate fails with "unknown event kind"; one
# missing from dispatch falls through the switch. Neither is visible until
# somebody runs the verb.

EVENT_GO="$(cat internal/events/event.go)"
assert_contains "the kind constant is declared" \
    'KindSessionTasksTouched Kind = "session_tasks.touched"' "${EVENT_GO}"
assert_contains "and listed in ValidKinds, which endless-go gates on" \
    "KindSessionTasksTouched: true" "${EVENT_GO}"
assert_contains "and dispatched to its executor" \
    "case KindSessionTasksTouched:" "$(cat internal/events/executor.go)"

# The end-to-end statement of the three above: the BUILT binary accepts the
# kind. The gate sits behind seven required-flag checks, so a probe that omits
# any of them never reaches it and passes for a kind that was never registered
# — which is what this probe originally did. Hence a positive control first:
# an invented kind must be refused, or the negative below proves nothing.
#
# Both probes stop before any write. The real kind clears the gate and dies on
# the deliberately-invalid node id a line later, so nothing reaches a ledger or
# a database.
kind_probe() {
    "${TMP}/endless-go" event emit \
        --kind "$1" --project e-2173-probe \
        --entity-type session_tasks --entity-id 0 \
        --actor-kind cli --actor-id probe \
        --node-id not-a-node-id --project-root "${TMP}" 2>&1 || true
}
assert_contains "the gate is reached at all (an invented kind is refused)" \
    'unknown event kind "e2173.not.a.kind"' "$(kind_probe e2173.not.a.kind)"
assert_not_contains "and the built binary accepts session_tasks.touched" \
    "unknown event kind" "$(kind_probe session_tasks.touched)"

# ---------------------------------------------------------------------------
section "G. The verb it shares a body with is unchanged — C7"
# ---------------------------------------------------------------------------
# `session task add` moved onto enrollSessionTasks. `claimed` is the only
# relation that outranks `queued`, so the generic "left alone" set is exactly
# the already-claimed set there — which is why its report could keep saying so
# in the same words.

py_check "session task add still enrolls an untouched task as queued" \
    test_add_enrolls_an_untouched_task_as_queued
py_check "session task add still promotes a weaker relation in place" \
    test_add_promotes_a_weaker_existing_relation
py_check "session task add still leaves the session's own claim alone" \
    test_add_leaves_the_session_claim_alone
go_check "Go: queued still adds and promotes" TestSessionTasksQueued_AddsAndPromotes
go_check "Go: queued still refuses to demote a claim" \
    TestSessionTasksQueued_LeavesClaimedAlone
go_check "Go: every membership verb still refuses an empty list" \
    TestSessionTasksMembership_RejectsEmptyList

# ---------------------------------------------------------------------------
section "H. Documented — C8"
# ---------------------------------------------------------------------------
# A command with no map file resolves to no guide section, and its --help tells
# every agent that reads it so. `guide_map check` is the pre-land gate for
# exactly that, and it reports such commands in a coverage-gap list.

GUIDE_CHECK="$(uv run python -m endless.guide_map check 2>&1)" \
    || setup_error "guide_map check failed: $(printf '%s' "${GUIDE_CHECK}" | tail -5)"
assert_contains "the guide map resolves every command" \
    "every command resolves" "${GUIDE_CHECK}"
assert_not_contains "and touch is not among the coverage gaps" \
    "- endless touch" "${GUIDE_CHECK}"
assert_not_contains "its --help carries no 'no guide section' warning" \
    "No guide section is mapped" "${HELP}"

SESSIONS_GUIDE="$(cat docs/guide/sessions.md)"
assert_contains "the sessions guide shows the verb" "endless touch E-101" "${SESSIONS_GUIDE}"
assert_contains "and says what it is instead of" \
    "rewriting \`phase\` to get the same display effect" "${SESSIONS_GUIDE}"
assert_contains "and the relation table credits touch" \
    "ran \`touch\` on it" "${SESSIONS_GUIDE}"

summary
