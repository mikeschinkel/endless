#!/usr/bin/env bash
#
# E-2073 verification — the worktree-removal prohibition is categorical.
#
# Before: every spawned session's handoff carried
#
#     7. Don't run `endless worktree land`/`drop` without asking.
#
# One sentence, two verbs, one precondition — have I been asked? — governing
# both. Landing is the normal end of a task, so a precondition is right there.
# Removal is destructive, is never part of finishing a task, and a spawned
# session has no benign case for it. On 2026-08-25 two sessions ten minutes
# apart destroyed worktrees. Neither OVERRODE the rule; both concluded the
# precondition was satisfied by conversational text that did not satisfy it —
# in one case a remark about what was worth reporting, read as authorization to
# act. One caused real damage.
#
# After: the verbs are split. `land` keeps "without asking". Removal is NEVER,
# with no precondition to mis-evaluate, and it names the OUTCOME rather than one
# command — `endless worktree drop`, `endless worktree reap`, and `git worktree
# remove` all reach the same destroyed worktree, so a drop-only prohibition is
# satisfiable by reaching for the next one down the list.
#
# The rule lives in ONE place: handoff/_mechanics.tmpl's handoff_land_drop
# partial, which E-1947 extracted from the six wrappers that had carried it
# verbatim. E-2073 rewrites that partial rather than the wrappers, and shares it
# with E-1947's guidance — a diverged branch is fixed in place, never by
# deleting the checkout — so both rules reach every handoff or neither does.
#
# Run from inside the worktree (esu puts you there):
#   esu && ./tests/tasks/e-2073-verify.sh
#
# What it proves:
#   1. FAIL-FAST unit gate: internal/templatecmd passes, including the new
#      TestRender_Handoff_WorktreeRemovalIsCategorical. Everything below renders
#      through the same code, so a red gate makes it all noise.
#   2. RENDERED, not grepped: every one of the ten handoffs a session can
#      actually be handed (five spawn types x the spawn and claim wrappers)
#      carries the categorical rule, rendered by a binary built from THIS tree.
#      Not one still carries the precondition form.
#   3. The rule names the outcome: all three removal routes appear, and the
#      script demonstrates why by showing a drop-only rule leaves two.
#   4. No collateral damage: `land` still says "without asking", the word never
#      attaches itself to removal, and E-1947's guidance — never OFFER a
#      removal; a diverged branch is a rebase or reset IN PLACE — survives the
#      rewrite of the partial it shares.
#   5. NEGATIVE CONTROL: the old sentence, injected into a throwaway project's
#      template override, is caught by the same check that passes above — so
#      section 2 is capable of failing.
#   6. Nothing live still carries the old sentence. The historical record
#      (.endless/LESSONS.md, .endless/plans/) is excluded by name, because it
#      quotes the old wording on purpose.
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

set -u

WT="$(git rev-parse --show-toplevel)" || { echo "SETUP ERROR: not in a git repo" >&2; exit 2; }
cd "${WT}" || exit 2

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section() { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }

report_pass() {
    PASS_COUNT=$((PASS_COUNT + 1))
    printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"
}

report_fail() {
    FAIL_COUNT=$((FAIL_COUNT + 1))
    FAILED_TESTS+=("$1")
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    [[ -n "${2:-}" ]] && printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    [[ -n "${3:-}" ]] && printf '      %sactual:  %s %s\n' "${DIM}" "${RESET}" "$3"
    return 0
}

setup_error() { printf '%sSETUP ERROR:%s %s\n' "${RED}${BOLD}" "${RESET}" "$1" >&2; exit 2; }

TMP_E2073=""
cleanup() { [[ -n "${TMP_E2073}" ]] && rm -rf "${TMP_E2073}"; }
trap cleanup EXIT

TMPL_DIR="${WT}/internal/templatecmd/templates/handoff"
[[ -d "${TMPL_DIR}" ]] || setup_error "missing ${TMPL_DIR}"
command -v go >/dev/null || setup_error "go is required"

# The two forms, spelled once. OLD_FORM is what two sessions mis-evaluated.
OLD_FORM='land`/`drop` without asking'
NEVER='NEVER remove a worktree'
# Every route to a destroyed worktree. Naming one is not naming the outcome.
REMOVAL_ROUTES=('endless worktree drop' 'endless worktree reap' 'git worktree remove')

# flatten — collapse whitespace so an expectation is the sentence a session
# reads, not one template's line wrapping. The numbered wrappers wrap the rule
# across four lines; the claim wrapper keeps it on one. Same prose either way.
flatten() { tr -s '[:space:]' ' '; }

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
section "1. Unit gate (fail-fast)"

for pkg in internal/templatecmd internal/hookcmd; do
    if go test "${WT}/${pkg}/" >"/tmp/e2073-gotest-$(basename ${pkg}).log" 2>&1; then
        report_pass "go test ./${pkg}/"
    else
        report_fail "go test ./${pkg}/" "pass" \
            "failed — see /tmp/e2073-gotest-$(basename ${pkg}).log"
        printf '\n%sFAIL-FAST: unit gate red; later assertions suppressed.%s\n' "${RED}" "${RESET}"
        exit 1
    fi
done

if uv run --project "${WT}" pytest "${WT}/tests/test_setup_hook_missing_events.py" \
        "${WT}/tests/test_setup_hook_sync.py" -q >/tmp/e2073-pytest.log 2>&1; then
    report_pass "pytest (hook installer: missing events + sync flags)"
else
    report_fail "pytest (hook installer)" "pass" "failed — see /tmp/e2073-pytest.log"
    printf '\n%sFAIL-FAST: unit gate red; later assertions suppressed.%s\n' "${RED}" "${RESET}"
    exit 1
fi

# The gate must include THIS task's test by name — a suite that passes because
# the test was renamed away would look identical from out here.
if go test "${WT}/internal/templatecmd/" \
        -run TestRender_Handoff_WorktreeRemovalIsCategorical -v \
        2>/dev/null | grep -q '^--- PASS: TestRender_Handoff_WorktreeRemovalIsCategorical'; then
    report_pass "TestRender_Handoff_WorktreeRemovalIsCategorical exists and passes"
else
    report_fail "TestRender_Handoff_WorktreeRemovalIsCategorical exists and passes" \
        "a named PASS line" "absent — the regression guard is gone or renamed"
    printf '\n%sFAIL-FAST: the guard is missing; later assertions suppressed.%s\n' "${RED}" "${RESET}"
    exit 1
fi

# ── setup: a binary built from THIS tree, and a throwaway project ────────────
# The templates are EMBEDDED, so a stale ./bin/endless-go would render the old
# prose and pass nothing. Build fresh.
TMP_E2073=$(mktemp -d "${TMPDIR:-/tmp}/e2073.XXXXXX") || setup_error "mktemp failed"
BIN="${TMP_E2073}/endless-go"
go build -o "${BIN}" "${WT}/cmd/endless-go" >/tmp/e2073-build.log 2>&1 \
    || setup_error "could not build endless-go (see /tmp/e2073-build.log)"

# Used to compose hook payloads in section 7; resolved once.
PY_BIN=$(command -v python3) || setup_error "python3 is required"
command -v sqlite3 >/dev/null || setup_error "sqlite3 is required"

# The hook is exercised in an ISOLATED environment, never against the real
# database or this worktree (E-1734's isolate-hook-verification rule). Three
# separate reasons, each of which broke a naive version of section 7:
#   - the binary DEFERS to <worktree>/bin/endless-go when cwd is inside a
#     self_dev worktree, and the re-exec does not carry stdin, so the payload
#     is lost and the hook allows;
#   - the hook touches its session row before dispatching, so a sandbox DB
#     whose schema has drifted fails the run before the gate is ever reached —
#     a red section 7 that says nothing about the gate;
#   - and a hook run is a WRITE. Pointing it at the real database to test a
#     refusal would be testing it by polluting it.
HOOK_HOME="${TMP_E2073}/hookhome"
HOOK_PROJ="${TMP_E2073}/hookproj"
mkdir -p "${HOOK_HOME}/endless" "${HOOK_PROJ}/.endless" \
    || setup_error "mkdir failed"
sqlite3 "${HOOK_HOME}/endless/endless.db" < "${WT}/internal/schema/schema.sql" \
    >/dev/null 2>&1 || setup_error "could not apply the schema to the fixture DB"

# Rendering MATERIALIZES the embedded template into <root>/.endless/templates/,
# so the fixture project lives outside this repo — rendering from the worktree
# would write files the task explicitly does not create.
PROJ="${TMP_E2073}/proj"
mkdir -p "${PROJ}/.endless" || setup_error "mkdir failed"

vars_for() {
    cat <<JSON
{
  "spawned_id": 9999,
  "label_prefix": "E-9999",
  "title": "Fixture task",
  "task_type": "$1",
  "worktree_path": "/tmp/wt/e-9999",
  "branch": "task/9999-fixture",
  "child_count": 0,
  "children_state": "3 ready (3 total)",
  "report_gate": false,
  "bg": false
}
JSON
}

# render <template-name> <task_type> — flattened stdout, or empty on failure.
render() {
    (cd "${PROJ}" && vars_for "$2" | "${BIN}" template render "$1" 2>/dev/null) | flatten
}

TYPES=(todo bugfix research epic brainstorm)

# ── 2. every handoff a session can be handed ────────────────────────────────
# Ten renders: five spawn wrappers plus the claim wrapper for each type. All six
# wrappers pull the rule from handoff/_mechanics.tmpl's handoff_land_drop
# partial (E-1947 extracted it), so this is one paragraph reaching ten handoffs
# — and rendering is how you prove it arrives at all ten rather than assuming
# the partial is wired everywhere it should be.
section "2. The rule reaches every rendered handoff"

for typ in "${TYPES[@]}"; do
    for name in "handoff/${typ}" "handoff/claim"; do
        label="${name} (task_type=${typ})"
        out=$(render "${name}" "${typ}")
        if [[ -z "${out}" ]]; then
            report_fail "${label}: renders" "output" "empty — render failed"
            continue
        fi
        if grep -qF "${NEVER}" <<<"${out}"; then
            report_pass "${label}: carries '${NEVER}'"
        else
            report_fail "${label}: carries '${NEVER}'" "${NEVER}" "${out}"
        fi
        if grep -qF "${OLD_FORM}" <<<"${out}"; then
            report_fail "${label}: no precondition form" \
                "no '${OLD_FORM}'" "still present"
        else
            report_pass "${label}: no precondition form"
        fi
    done
done

# ── 3. the rule names the outcome, not one command ──────────────────────────
section "3. All three routes to a destroyed worktree"

for typ in "${TYPES[@]}"; do
    for name in "handoff/${typ}" "handoff/claim"; do
        out=$(render "${name}" "${typ}")
        missing=()
        for route in "${REMOVAL_ROUTES[@]}"; do
            grep -qF "${route}" <<<"${out}" || missing+=("${route}")
        done
        if (( ${#missing[@]} == 0 )); then
            report_pass "${name} (task_type=${typ}): names drop, reap and git worktree remove"
        else
            report_fail "${name} (task_type=${typ}): names all three routes" \
                "drop, reap, git worktree remove" "missing: ${missing[*]}"
        fi
    done
done

# WHY three and not one, demonstrated rather than asserted: a rule naming only
# `drop` is fully honoured by a session that reaches for either of the others,
# and the worktree is just as gone.
out=$(render "handoff/todo" "todo")
drop_only_leaves=0
for route in 'endless worktree reap' 'git worktree remove'; do
    grep -qF "${route}" <<<"${out}" && drop_only_leaves=$((drop_only_leaves + 1))
done
if (( drop_only_leaves == 2 )); then
    report_pass "a drop-only prohibition would have left 2 routes open — both are named"
else
    report_fail "a drop-only prohibition would have left 2 routes open" \
        "reap and git worktree remove both named" "${drop_only_leaves} of 2"
fi

# ── 4. no collateral damage to land ─────────────────────────────────────────
# Landing IS the normal end of a task and the spawning session owns the timing,
# so "ask first" is correct there. Splitting the verbs must not have deleted it,
# and must not have left "without asking" attached to removal.
section "4. land keeps ask-first; removal keeps none"

for typ in "${TYPES[@]}"; do
    for name in "handoff/${typ}" "handoff/claim"; do
        out=$(render "${name}" "${typ}")
        if grep -qF 'endless worktree land` without asking' <<<"${out}"; then
            report_pass "${name} (task_type=${typ}): land still asks first"
        else
            report_fail "${name} (task_type=${typ}): land still asks first" \
                'endless worktree land` without asking' "${out}"
        fi
    done
done

# The precondition must not survive anywhere near a removal verb. Any "without
# asking" that follows drop/reap/remove would reintroduce the thing to
# mis-evaluate.
leaks=0
for typ in "${TYPES[@]}"; do
    for name in "handoff/${typ}" "handoff/claim"; do
        render "${name}" "${typ}" \
            | grep -qE '(drop|reap|worktree remove)[^.]*without asking' && leaks=$((leaks + 1))
    done
done
if (( leaks == 0 )); then
    report_pass "no removal verb carries a 'without asking' precondition"
else
    report_fail "no removal verb carries a 'without asking' precondition" \
        "0 renders" "${leaks} render(s) attach a precondition to removal"
fi

# The rule says who DOES own removal, so a session that thinks removal is
# warranted has somewhere to send it instead of a decision to make.
for typ in "${TYPES[@]}"; do
    for name in "handoff/${typ}" "handoff/claim"; do
        out=$(render "${name}" "${typ}")
        if grep -qF "removal is not an agent's to perform" <<<"${out}" \
                && grep -qF 'If removal looks warranted, say so once and stop.' <<<"${out}"; then
            report_pass "${name} (task_type=${typ}): says who owns removal, and what to do instead"
        else
            report_fail "${name} (task_type=${typ}): says who owns removal, and what to do instead" \
                "the ownership sentence and 'say so once and stop'" "${out}"
        fi
    done
done

# E-1947 shares this partial, and E-2073 rewrote it. Its guidance — a diverged
# branch is fixed IN PLACE, never by deleting the checkout — must survive the
# rewrite. Its own anti-drift test pins this string; asserting it here too means
# a regression is caught by the suite that touched the prose.
for typ in "${TYPES[@]}"; do
    for name in "handoff/${typ}" "handoff/claim"; do
        if grep -qF 'the fix is `git rebase main` or `git reset --hard main` **in place**' \
                <<<"$(render "${name}" "${typ}")"; then
            report_pass "${name} (task_type=${typ}): E-1947's in-place fix survives the rewrite"
        else
            report_fail "${name} (task_type=${typ}): E-1947's in-place fix survives the rewrite" \
                "the rebase/reset-in-place guidance" "absent — E-1947 regressed"
        fi
    done
done

# E-1947 also forbids OFFERING a removal, which is a separate failure from
# performing one: a recommendation the user acts on does the same damage.
for typ in "${TYPES[@]}"; do
    for name in "handoff/${typ}" "handoff/claim"; do
        if grep -qF 'never OFFER to' <<<"$(render "${name}" "${typ}")"; then
            report_pass "${name} (task_type=${typ}): offering a removal is forbidden too"
        else
            report_fail "${name} (task_type=${typ}): offering a removal is forbidden too" \
                "'never OFFER to'" "absent — E-1947 regressed"
        fi
    done
done

# ── 5. negative control ─────────────────────────────────────────────────────
# Everything above is a set of greps that pass. This proves they can also fail:
# the old sentence, put back through the documented per-developer override, is
# caught by the exact same two checks section 2 runs.
section "5. Negative control — the old sentence is still detectable"

mkdir -p "${PROJ}/.endless/templates/handoff" || setup_error "mkdir failed"
cat > "${PROJ}/.endless/templates/handoff/todo.md.local.tmpl" <<'TMPL'
You're a worktree-bound Claude Code session spawned to take this task end to end:

- {{.label_prefix}}: {{.title}}.

7. Don't run `endless worktree land`/`drop` without asking.
TMPL

out=$(render "handoff/todo" "todo")
if grep -qF "${OLD_FORM}" <<<"${out}"; then
    report_pass "an injected old-form handoff IS flagged by the precondition check"
else
    report_fail "an injected old-form handoff is flagged" \
        "the check to notice '${OLD_FORM}'" "it did not — section 2 cannot fail"
fi
if grep -qF "${NEVER}" <<<"${out}"; then
    report_fail "an injected old-form handoff FAILS the categorical check" \
        "no '${NEVER}' in a template that lacks it" "matched anyway"
else
    report_pass "an injected old-form handoff FAILS the categorical check"
fi

rm -f "${PROJ}/.endless/templates/handoff/todo.md.local.tmpl"
out=$(render "handoff/todo" "todo")
if grep -qF "${NEVER}" <<<"${out}" && ! grep -qF "${OLD_FORM}" <<<"${out}"; then
    report_pass "with the override removed, the shipped template renders again"
else
    report_fail "with the override removed, the shipped template renders again" \
        "the categorical rule and no precondition form" "${out}"
fi

# ── 6. nothing live still carries the old sentence ──────────────────────────
# Five places are allowed to hold the old wording, and each is asserted below
# rather than merely skipped:
#   .endless/LESSONS.md      the account of the two incidents
#   .endless/plans/          the mirrored plans, this task's included
#   .endless/db-ledger/      durable, append-only, never hand-edited
#   the templatecmd test     a guard — it asserts the form is ABSENT from a render
#   THIS SCRIPT              a guard — OLD_FORM above is what section 2 greps for
# A guard has to quote the sentence it forbids, or it cannot notice its return.
# Everything else is live prose a session may read, and must not still say it.
section "6. Only the record and the guards still quote it"

GUARDS=(
    "internal/templatecmd/claim_handoff_test.go"
    "tests/tasks/e-2073-verify.sh"
    # The gate's own doc comment quotes the sentence it exists to replace —
    # that is the rationale for why it has no bypass, and it belongs there.
    "internal/hookcmd/claude.go"
)

excludes=(':!.endless/LESSONS.md' ':!.endless/plans/' ':!.endless/db-ledger/')
for g in "${GUARDS[@]}"; do excludes+=(":!${g}"); done

live_hits=$(git -C "${WT}" grep -l -F -- "${OLD_FORM}" -- "${excludes[@]}" 2>/dev/null)
if [[ -z "${live_hits}" ]]; then
    report_pass "no tracked live file still carries the precondition form"
else
    report_fail "no tracked live file still carries the precondition form" \
        "no hits outside the record and the guards" \
        "$(tr '\n' ' ' <<<"${live_hits}")"
fi

# The record itself must survive: an over-eager sweep that rewrote the lessons
# would erase the account of the two incidents this task exists because of.
if git -C "${WT}" grep -q -F -- "${OLD_FORM}" -- .endless/LESSONS.md 2>/dev/null; then
    report_pass "LESSONS.md still quotes the old wording — the record is intact"
else
    report_fail "LESSONS.md still quotes the old wording" \
        "the incident record, untouched" "the quote is gone"
fi

# Each guard must keep quoting it. One that stopped naming the old sentence
# would go on passing while detecting nothing.
for g in "${GUARDS[@]}"; do
    if git -C "${WT}" grep -q -F -- "${OLD_FORM}" -- "${g}" 2>/dev/null; then
        report_pass "${g} still names the form it forbids"
    else
        report_fail "${g} still names the form it forbids" \
            "the old sentence quoted in ${g}" "absent — the guard detects nothing"
    fi
done

# The task never writes a project-local override into this repo; a materialized
# copy here would shadow the embedded template it just edited.
if [[ -e "${WT}/.endless/templates" ]]; then
    report_fail "the worktree has no .endless/templates override" \
        "absent" "present — it would shadow the embedded templates"
else
    report_pass "the worktree has no .endless/templates override"
fi

# ── 7. the gate is wired where it has to be ─────────────────────────────────
# Sections 2-6 are about PROSE. This is the half that does not depend on a
# session choosing to honour it.
#
# The matcher itself is proven by TestWorktreeRemovalRes in the fail-fast gate
# above — 20 blocked routes and 20 allowed commands, including the mention-vs-
# invoke cases. What a matcher test cannot see is whether the gate is CALLED,
# and from where; that is what this section asserts.
#
# It is asserted from source rather than by running the hook. Driving the real
# binary was tried and abandoned: `endless-go hook` pins the MAIN database
# regardless of XDG_CONFIG_HOME, so every invocation writes there — a hook run
# from a fixture directory auto-registers that directory as a project. A verify
# script cannot exercise this path without polluting the database it is meant to
# leave alone. (It also cannot currently pass: see the note at the end of this
# section.)
section "7. The tool-layer gate is wired into PreToolUse"

HOOK_SRC="${WT}/internal/hookcmd/claude.go"
[[ -f "${HOOK_SRC}" ]] || setup_error "missing ${HOOK_SRC}"

if grep -q 'func blockWorktreeRemovalIfApplicable' "${HOOK_SRC}"; then
    report_pass "blockWorktreeRemovalIfApplicable exists"
else
    report_fail "blockWorktreeRemovalIfApplicable exists" "the gate function" "absent"
fi

# Everything about placement is a property of handlePreToolUse's BODY, so read
# that function once. Grepping the whole file finds `if !isRegistered` in other
# functions and compares against the wrong line.
BODY="${TMP_E2073}/handlePreToolUse.go"
sed -n '/^func handlePreToolUse(/,/^}/p' "${HOOK_SRC}" > "${BODY}"
[[ -s "${BODY}" ]] || setup_error "could not extract handlePreToolUse from ${HOOK_SRC}"

# Order is the property under test. The gate must be called BEFORE the
# `if !isRegistered { return nil }` early-return, or a session in an
# unregistered or failed-to-resolve project — if anything MORE likely to reach
# for a removal — walks straight past it.
call_line=$(grep -n 'blockWorktreeRemovalIfApplicable(payload)' "${BODY}" | head -1 | cut -d: -f1)
guard_line=$(grep -n 'if !isRegistered {' "${BODY}" | head -1 | cut -d: -f1)
if [[ -n "${call_line}" && -n "${guard_line}" ]] && (( call_line < guard_line )); then
    report_pass "the gate runs before the registration early-return (line ${call_line} < ${guard_line})"
else
    report_fail "the gate runs before the registration early-return" \
        "the call above the !isRegistered guard" \
        "call=${call_line:-absent} guard=${guard_line:-absent}"
fi

# It must be reached only for Bash. The nearest enclosing tool-name test above
# the call is the one that governs it — found by walking back up the body
# rather than with a fixed -B window, which a comment block would break.
bash_line=$(grep -n 'payload.ToolName == "Bash"' "${BODY}" | cut -d: -f1 \
                | awk -v c="${call_line}" '$1 < c' | tail -1)
if [[ -n "${bash_line}" ]]; then
    report_pass "the gate is reached from the Bash branch (line ${bash_line})"
else
    report_fail "the gate is reached from the Bash branch" \
        'a payload.ToolName == "Bash" test above the call' "none above line ${call_line:-?}"
fi
if sed -n "/func blockWorktreeRemovalIfApplicable/,/^}/p" "${HOOK_SRC}" \
        | grep -q 'blockToolUse('; then
    report_pass "the gate refuses via blockToolUse (stderr + exit 2)"
else
    report_fail "the gate refuses via blockToolUse" "a blockToolUse call" "absent"
fi

# No bypass, deliberately. A session that talked itself into "I was asked" would
# equally talk itself into "this is the case the flag is for".
if sed -n "/func blockWorktreeRemovalIfApplicable/,/^}/p" "${HOOK_SRC}" \
        | grep -qiE '\-\-no-verify|ENDLESS_[A-Z_]*(FORCE|ALLOW|SKIP)|bypass' ; then
    report_fail "the gate names no bypass" "no escape hatch" "one is offered"
else
    report_pass "the gate names no bypass"
fi

# The refusal has to say what to do INSTEAD, or it is a wall rather than a
# signpost — and "fix the branch in place" is the answer it must give.
refusal=$(sed -n "/func blockWorktreeRemovalIfApplicable/,/^}/p" "${HOOK_SRC}")
if grep -qF 'rebase main' <<<"${refusal}" && grep -qF 'reset --hard main' <<<"${refusal}"; then
    report_pass "the refusal points at fixing the branch in place"
else
    report_fail "the refusal points at fixing the branch in place" \
        "the rebase/reset alternative in the message" "absent"
fi

# ── 8. the gate reaches every machine, not just this one ────────────────────
# A gate installed under an event the machine does not hook is not a gate. The
# hook is registered once per machine in ~/.claude/settings.json, and
# `setup_claude_hook` early-returns as soon as it finds endless-go under ANY
# event — so a machine whose install predates an event never gains it, while
# the command reports itself correctly set up.
section "8. A missing hook event repairs itself"

SETUP_SRC="${WT}/src/endless/setup.py"
[[ -f "${SETUP_SRC}" ]] || setup_error "missing ${SETUP_SRC}"

if grep -q 'def _repair_missing_hook_events' "${SETUP_SRC}"; then
    report_pass "_repair_missing_hook_events exists"
else
    report_fail "_repair_missing_hook_events exists" "the repair function" "absent"
fi

# It must be CALLED from the already-installed branch — the branch that returns
# early — or it can never fix the case it was written for.
if sed -n '/if _has_endless_hook(settings):/,/^    # Show what/p' "${SETUP_SRC}" \
        | grep -q '_repair_missing_hook_events(settings, hook_bin)'; then
    report_pass "it runs on the already-installed path (the one that early-returns)"
else
    report_fail "it runs on the already-installed path" \
        "a call inside the _has_endless_hook branch" "absent"
fi

if grep -q '"PreToolUse"' "${SETUP_SRC}" \
        && sed -n '/^SYNC_EVENTS/p' "${SETUP_SRC}" | grep -q 'PreToolUse'; then
    report_pass "PreToolUse is hooked, and synchronous (async cannot block)"
else
    report_fail "PreToolUse is hooked and synchronous" \
        "PreToolUse in CLAUDE_HOOK_EVENTS and SYNC_EVENTS" "missing from one"
fi

# ── summary ─────────────────────────────────────────────────────────────────
section "Summary"
printf '  %s%d passed%s, %s%d failed%s\n' \
    "${GREEN}" "${PASS_COUNT}" "${RESET}" \
    "$([[ ${FAIL_COUNT} -gt 0 ]] && printf '%s' "${RED}")" "${FAIL_COUNT}" "${RESET}"

if (( FAIL_COUNT > 0 )); then
    printf '\n  Failed:\n'
    for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    exit 1
fi
exit 0
