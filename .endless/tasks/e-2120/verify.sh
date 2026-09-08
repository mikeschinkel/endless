#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2120 and records what was true when E-2120
# landed. Edit it only if you ARE E-2120. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2120 verification — the two agent-facing surfaces that were teaching
# sessions to over-file and to narrate status changes.
#
# A. The spawn/claim handoff's mid-task discovery rule. Bullet 1 asked whether a
#    finding could be done "inside the work already underway" — a topical
#    membership question. Sessions answered it honestly ("a stray id in a guide
#    paragraph is not part of `verb update`'s subject") and fell through to
#    "otherwise file it". Now the axis is stated as cost and reviewer confusion,
#    folding work in carries a third obligation (record the grown scope on the
#    task), and filing is a QUESTION put to the user rather than a branch the
#    session takes alone.
#
# B. The status-change surface. Every status-affecting `task update` printed a
#    `• Status: <old> -> <new>` line plus a `--keep-status` advisory, both
#    addressed to the agent, which relayed them to its user as news. Rewording
#    was tried and failed. Now a human sees all of it and an agent sees only the
#    fields it asked for — and the plan-edit auto-revisit that made this
#    dangerous to hide is gone entirely, so there is no transition left to hide.
#
# Sections 3 onward drive the real CLI as a subprocess against a real database
# in a temp XDG_CONFIG_HOME. Nothing is stubbed: the audiences are the product's
# own detection (a real CLAUDE_CODE_ENTRYPOINT, and the --agent-view flag), the
# statuses are read back out of the database through `task show`, and the
# handoff is the one `endless task handoff` renders.
#
#   endless task verify E-2120
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
trap 'rm -rf "${TMP}"' EXIT

# ── 1. unit gate (fail fast) ────────────────────────────────────────────────
# The durable coverage lives in tests/ and internal/, per .endless/tasks/CLAUDE.md:
# test_status_change_audience.py owns the audience gate, test_keep_status.py owns
# what became of the removed inference, and the two Go handoff packages own the
# rendered discovery branches. A failure there makes every drive below
# meaningless, so the suite stops rather than reporting a cascade.
section "1. Unit gate (fail fast)"

if uv run pytest -q \
        tests/test_status_change_audience.py \
        tests/test_keep_status.py \
        tests/test_untriaged_status.py \
        tests/test_text_auto_promote.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest audience gate + every remaining status inference"
else
    report_fail "pytest audience gate + status inferences" "exit 0" \
        "$(tail -25 "${TMP}/py.log")"
    summary
fi

if go test ./internal/templatecmd/ ./internal/hookcmd/ >"${TMP}/gotest.log" 2>&1; then
    report_pass "go test templatecmd + hookcmd (the rendered handoff)"
else
    report_fail "go test templatecmd + hookcmd" "exit 0" \
        "$(tail -25 "${TMP}/gotest.log")"
    summary
fi

# ── 2. build ────────────────────────────────────────────────────────────────
# Built from THIS worktree rather than taken off PATH: every drive below writes
# through the event pipeline, and an installed binary would prove something
# about a different tree.
section "2. Build"

mkdir -p "${TMP}/bin"
if go build -o "${TMP}/bin/endless-go" ./cmd/endless-go >"${TMP}/build.log" 2>&1; then
    report_pass "go build ./cmd/endless-go (the pipeline sections 3+ write through)"
else
    report_fail "go build ./cmd/endless-go" "exit 0" "$(tail -25 "${TMP}/build.log")"
    summary
fi

# ── fixture: a real config dir, a real git project, a real projects row ─────
# The runner already replaced HOME and XDG_CONFIG_HOME; this narrows them again
# to a directory this suite seeds.
CFG_HOME="${TMP}/cfg"
CFG="${CFG_HOME}/endless"
PROJ="${TMP}/proj"
mkdir -p "${CFG}" "${PROJ}/.endless" "${TMP}/home"

printf '{"name": "e2120"}\n' >"${PROJ}/.endless/config.json"
git -C "${PROJ}" init -q -b main             >/dev/null 2>&1 || setup_error "git init failed"
git -C "${PROJ}" config user.email t@e.com   >/dev/null 2>&1
git -C "${PROJ}" config user.name  T         >/dev/null 2>&1
git -C "${PROJ}" config commit.gpgsign false >/dev/null 2>&1
git -C "${PROJ}" add .endless/config.json    >/dev/null 2>&1
git -C "${PROJ}" commit -qm init             >/dev/null 2>&1 || setup_error "git commit failed"

cat >"${TMP}/seed.py" <<'PY'
import os
from pathlib import Path
from endless import config
config.set_db_context(Path(os.environ["E2120_CFG"]))
from endless import db
db.execute(
    "INSERT INTO projects (id, name, path, status) VALUES (1, 'e2120', ?, 'active')",
    (os.environ["E2120_PROJ"],),
)
PY

E2120_CFG="${CFG}" E2120_PROJ="${PROJ}" uv run python "${TMP}/seed.py" \
    >"${TMP}/seed.log" 2>&1 \
    || setup_error "could not seed the fixture database: $(tail -5 "${TMP}/seed.log")"

# human — one `endless` invocation with every harness signal stripped, which is
# what a person at a terminal looks like to agent_env.
human() {
    ( cd "${PROJ}" && env -u ENDLESS_SESSION_ID -u CLAUDECODE \
        -u CLAUDE_CODE_ENTRYPOINT -u __CFBundleIdentifier \
        HOME="${TMP}/home" XDG_CONFIG_HOME="${CFG_HOME}" \
        PATH="${TMP}/bin:${PATH}" \
        uv run --project "${WT}" endless "$@" 2>&1 )
}

# agent — the same invocation carrying the harness signal Claude Code sets. Not
# a flag and not a stub: this is the detection the product ships.
agent() {
    ( cd "${PROJ}" && env -u ENDLESS_SESSION_ID -u __CFBundleIdentifier \
        CLAUDECODE=1 CLAUDE_CODE_ENTRYPOINT=cli \
        HOME="${TMP}/home" XDG_CONFIG_HOME="${CFG_HOME}" \
        PATH="${TMP}/bin:${PATH}" \
        uv run --project "${WT}" endless "$@" 2>&1 )
}

# task_id pulls E-NNN out of a `task add` line.
task_id() { sed -n 's/.*\(E-[0-9][0-9]*\).*/\1/p' <<<"$1" | head -1; }

# status_of reads the status back out of the database, so a suppressed render is
# never mistaken for a suppressed transition.
status_of() { human task show "$1" --json | uv run python -c \
    'import json,sys; print(json.load(sys.stdin)["status"])'; }

new_task() {
    local out
    out="$(human task add "$1" --description "$2")" \
        || setup_error "fixture task add failed: ${out}"
    task_id "${out}"
}

# ── 3. an inferred status change: a human is told, an agent is not ──────────
# The E-1845 description reset is the inference that fires most often. It is
# correct, it is complete by the time anything is printed, and there is nothing
# the agent can do about it — so the agent gets the field it asked to change and
# nothing else, while a person at a terminal gets the whole story.
section "3. The status change an agent did not ask for"

T_AGENT="$(new_task 'Add a widget' 'the original spec')"
human task update "${T_AGENT}" --status ready >/dev/null
out="$(agent task update "${T_AGENT}" --description 'a materially different spec')"

assert_contains "an agent sees the field it asked to change" "Description:" "${out}"
assert_not_contains "an agent is not told about the inferred status" "Status:" "${out}"
assert_not_contains "an agent gets no --keep-status advisory" "re-triage" "${out}"
assert_eq "the reset still happened — the render was suppressed, not the transition" \
    "untriaged" "$(status_of "${T_AGENT}")"

T_HUMAN="$(new_task 'Add a gadget' 'the original spec')"
human task update "${T_HUMAN}" --status ready >/dev/null
out="$(human task update "${T_HUMAN}" --description 'a materially different spec')"

assert_contains "a human still sees the status line" "Status:" "${out}"
assert_contains "a human still gets the advisory naming the escape hatch" \
    "re-triage" "${out}"
assert_contains "and the flag it names" "--keep-status" "${out}"

# --agent-view is how a person previews the agent's view. It composes with
# harness detection in one predicate (agent_help.agent_facing), so a human
# passing it must reach the same output an agent reaches.
T_VIEW="$(new_task 'Add a doohickey' 'the original spec')"
human task update "${T_VIEW}" --status ready >/dev/null
out="$(human --agent-view task update "${T_VIEW}" --description 'a materially different spec')"

assert_not_contains "--agent-view reaches the same gate: no status line" "Status:" "${out}"
assert_not_contains "--agent-view reaches the same gate: no advisory" "re-triage" "${out}"

# ── 4. a status the agent DID ask for still renders ─────────────────────────
# The gate is "did it ask", not "is it a status". Suppressing an explicit
# --status would hide the agent's own edit from it.
section "4. The status an agent named"

T_EXPLICIT="$(new_task 'Add a sprocket' 'a spec')"
human task update "${T_EXPLICIT}" --status ready >/dev/null
out="$(agent task update "${T_EXPLICIT}" --status revisit)"

assert_contains "an explicit --status still renders for an agent" "Status:" "${out}"
assert_contains "and shows what it became" "revisit" "${out}"

# The tier-1 advance is the other inference on this path, held to the same rule.
T_TIER="$(new_task 'Add a flange' 'a spec')"
out="$(agent task update "${T_TIER}" --tier 1)"

assert_contains "an agent sees the tier it asked for" "Tier:" "${out}"
assert_not_contains "but not the status the tier advanced" "Status:" "${out}"
assert_eq "the advance still happened" "ready" "$(status_of "${T_TIER}")"

# ── 5. editing a finished task's plan infers nothing ────────────────────────
# The live case this task came from. Recording E-2114's grown scope in its plan
# — obeying the rule section 6 now imposes — flipped it `assumed -> revisit`,
# and `revisit` has no edge back to `assumed`: the verification granted an hour
# earlier was destroyed by the act of documenting what shipped.
section "5. Recording what shipped is not reopening it"

T_DONE="$(new_task 'Add a bracket' 'a spec')"
human task update "${T_DONE}" --text '# plan' >/dev/null
for step in ready underway unverified assumed; do
    human task update "${T_DONE}" --status "${step}" >/dev/null \
        || setup_error "could not walk ${T_DONE} to ${step}"
done
assert_eq "fixture: the task is assumed" "assumed" "$(status_of "${T_DONE}")"

out="$(human task update "${T_DONE}" --text '# plan

## Also shipped
the drive-by that was folded in')"

assert_eq "a real plan edit leaves a finished task exactly where it was" \
    "assumed" "$(status_of "${T_DONE}")"
assert_not_contains "and says nothing about status, to anyone" "Status:" "${out}"
assert_contains "the plan edit itself lands" "Text: <set> -> <set>" "${out}"
assert_contains "and the text is what was written" "Also shipped" \
    "$(human task show "${T_DONE}" --text)"

# The flag the discovery rule tells sessions to pass is now redundant here. It
# must stay a no-op rather than an error or a surprise.
out="$(agent task update "${T_DONE}" --text '# plan

## Also shipped
folded in, recorded again' --keep-status)"
assert_eq "--keep-status on the same edit is a harmless no-op" \
    "assumed" "$(status_of "${T_DONE}")"
assert_not_contains "and still narrates no status" "Status:" "${out}"

# ── 6. the handoff a spawned session actually reads ─────────────────────────
# Rendered through the product's own command, not read off the template file:
# the defect was what a session is TOLD at the decision point.
section "6. The discovery rule in the rendered handoff"

T_HANDOFF="$(new_task 'Add a grommet' 'a spec')"
# Flattened, the way internal/templatecmd's own guard flattens it: an
# expectation here is the sentence a session reads, not one template's line
# breaks. Two of these sentences wrap, and pinning where they wrap would make
# this suite fail on a reflow that changed nothing a session sees.
handoff="$(human task handoff "${T_HANDOFF}" | tr -s '[:space:]' ' ')"

assert_not_contains "the kinship test is gone" \
    "inside the work already underway" "${handoff}"
assert_contains "the axis is named outright" \
    "The test is COST and reviewer confusion, not kinship" "${handoff}"
assert_contains "and so is the failure mode it replaces" \
    "is not a reason to file" "${handoff}"
assert_contains "the real bound survives, measured in size" \
    "still splits out" "${handoff}"
assert_contains "folding in carries the third obligation" \
    "record the grown scope on the task" "${handoff}"
assert_contains "with the command that discharges it" \
    "--text-file <path> --keep-status --db main" "${handoff}"
assert_contains "filing is a question, not a branch the session takes alone" \
    "ASK me before filing" "${handoff}"
assert_contains "and the question has a shape" \
    "the case for filing, the case against, and your recommendation" "${handoff}"
assert_contains "filing still links back to the task that surfaced it" \
    "--cleans-up" "${handoff}"

# The two clauses this partial had compressed away. Both were in the guide and
# in neither rendering, so a session that never opened the guide met neither.
assert_contains "ED-1550's framing is back: the list is not a menu of equals" \
    "Filing is the exception, not the default." "${handoff}"
assert_contains "the override outranks the size bound" \
    "obliged to run and report" "${handoff}"
assert_contains "and it is phrased around the obligation, not around being blocked" \
    "You cannot report that suite green and you cannot leave it red" "${handoff}"

# ── 7. the guide says the same thing ────────────────────────────────────────
# `docs/guide/tasks.md` is where a session that reads past the handoff lands. It
# used to HARDEN the kinship reading, and its override clause named a condition
# ("cannot complete the task without") its own example contradicted ("a test
# that fails for an unrelated reason").
section "7. The guide agrees with the handoff"

guide="$(human guide tasks)"

assert_not_contains "the guide no longer hardens the kinship reading" \
    "is a real bound, not a license" "${guide}"
assert_contains "it states the axis" \
    "The test is cost and reviewer confusion, not kinship" "${guide}"
assert_contains "it names the bound as size" \
    "The bound this test really protects is **size**, not relatedness" "${guide}"
assert_contains "the guide keeps the same framing the handoff now carries" \
    "not the default" "${guide}"
assert_contains "the ask branch is a peer answer" \
    "Otherwise, ask your user before filing" "${guide}"
assert_contains "with the case for" "The case for filing" "${guide}"
assert_contains "the case against" "The case against" "${guide}"
assert_contains "and a recommendation" "Your recommendation" "${guide}"
assert_contains "the override is about a check you must run and report" \
    "a failure in a check you are obliged to run and report" "${guide}"
assert_contains "not about being blocked" \
    "rarely stops you finishing the task" "${guide}"

# The inference table this task shortened.
assert_contains "the guide counts three inferences" \
    "in three places" "${guide}"
assert_not_contains "the removed one is not still listed" \
    "unshipped scope on a task that reads as finished" "${guide}"
assert_contains "and the removal is explained where it was documented" \
    "Editing a done task's plan changes nothing but the plan" "${guide}"

# ── 8. the drift gates this touched ─────────────────────────────────────────
section "8. Guide and lifecycle artifacts are in sync"

if uv run python -m endless.guide_map check >"${TMP}/guide.log" 2>&1; then
    report_pass "guide map resolves (guide-check)"
else
    report_fail "guide-check" "exit 0" "$(tail -15 "${TMP}/guide.log")"
fi

# The removal took nothing out of the transition table — the edges it fired
# (Assumed/Confirmed/Completed -> Revisit) are user-owned and stay. This proves
# the diagram did not need regenerating.
if uv run python -m endless.lifecycle_map check >"${TMP}/lifecycle.log" 2>&1; then
    report_pass "the status lifecycle diagram is unchanged and in sync"
else
    report_fail "lifecycle-check" "exit 0" "$(tail -15 "${TMP}/lifecycle.log")"
fi

summary
