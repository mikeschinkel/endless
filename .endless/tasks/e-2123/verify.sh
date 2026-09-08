#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2123 and records what was true when E-2123
# landed. Edit it only if you ARE E-2123. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2123 verification — every handoff a session can receive now tells it to
# read EVERY field, and the write flag that spells itself the same way is
# untouched.
#
# Before: `endless task show <id> --text` renders a populated `analysis` as a
# one-line teaser, so a spawned session that followed its handoff literally
# never read it. The commit "Change --text to --all-fields" fixed two docs and
# one inert test string; the six handoff templates that render every spawn and
# claim prompt — the reason sessions were reading `--text` at all — were not
# touched.
#
# After: all six templates say `--all-fields`, and so does the guide's
# read-a-task example and the orchestration prose that describes what a handoff
# carries.
#
# The trap this suite exists to hold shut: `--text` names TWO flags. The read
# toggle on `task show` (changed) and the content flag on
# `task add`/`task update`/`lesson write` (must never change). `claim.md.tmpl`
# carries both on ONE line, so any sweep that matched the flag without reading
# the verb in front of it would have corrupted the write site while looking
# green. Sections 3 and 4 assert the write sites still exist and still work.
#
# Sections 2-3 render the real templates through a binary built from THIS
# worktree; nothing is stubbed and no golden string is copied from the source
# being tested.
#
#   endless task verify E-2123
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
trap 'rm -rf "${TMP}"' EXIT

# ── 1. unit gate (fail fast) ────────────────────────────────────────────────
# The durable coverage lives in the project's own suites, per
# .endless/tasks/CLAUDE.md: tests/test_handoff.py owns the rendered handoff
# through the Python `render_handoff` path that `task spawn` actually calls,
# tests/test_analysis_show.py owns the root-cause claim that `--all-fields`
# emits every content section (analysis included) where `--text` does not, and
# internal/{hookcmd,templatecmd} own the claim-handoff delivery and the
# template renderer. A failure there makes every drive below meaningless, so
# the suite stops rather than reporting a cascade.
section "1. Unit gate (fail fast)"

if uv run pytest -q tests/test_handoff.py tests/test_analysis_show.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest — rendered handoff, and --all-fields emits analysis"
else
    report_fail "pytest tests/test_handoff.py tests/test_analysis_show.py" \
        "exit 0" "$(tail -25 "${TMP}/py.log")"
    summary
fi

if go test ./internal/hookcmd/ ./internal/templatecmd/ \
        >"${TMP}/go.log" 2>&1; then
    report_pass "go test — claim-handoff delivery and the template renderer"
else
    report_fail "go test ./internal/{hookcmd,templatecmd}" "exit 0" \
        "$(tail -25 "${TMP}/go.log")"
    summary
fi

# Built from THIS worktree rather than taken off PATH: the templates are
# embedded in the binary, so an installed one would render a different tree.
mkdir -p "${TMP}/bin"
if go build -o "${TMP}/bin/endless-go" ./cmd/endless-go >"${TMP}/build.log" 2>&1; then
    report_pass "go build ./cmd/endless-go (the binary section 2 renders through)"
else
    report_fail "go build ./cmd/endless-go" "exit 0" "$(tail -25 "${TMP}/build.log")"
    summary
fi

BIN="${TMP}/bin/endless-go"

# render <template> <type> <child_count> <report_gate> — one real handoff, off
# the embedded templates, exactly as `task spawn` renders it.
render() {
    printf '{"spawned_id":2123,"label_prefix":"","title":"Finish the sweep",' \
        >"${TMP}/vars.json"
    printf '"task_type":"%s","worktree_path":"/repo/.endless/worktrees/e-2123",' \
        "$2" >>"${TMP}/vars.json"
    printf '"branch":"task/2123-sweep","child_count":%s,"children_state":"",' \
        "$3" >>"${TMP}/vars.json"
    printf '"report_gate":%s}' "$4" >>"${TMP}/vars.json"
    "${BIN}" template render "handoff/$1" <"${TMP}/vars.json" 2>&1
}

# ── 2. every handoff points the session at every field ──────────────────────
# The actual bug. Six templates render every spawn and every claim; each is
# checked across the branches its partials take (children present or not, the
# report channel on or off), because a `--text` surviving inside a conditional
# is exactly as broken as one on the main line and does not show in a single
# rendering.
section "2. Every spawn and claim handoff says --all-fields"

for pair in "todo todo" "bugfix bugfix" "research research" "epic epic" \
            "brainstorm brainstorm" "claim todo"; do
    tmpl="${pair%% *}"; type="${pair##* }"
    saw_all_fields="yes"; saw_bare_text="no"; renders=0
    for children in 0 2; do
        for gate in true false; do
            out="$(render "${tmpl}" "${type}" "${children}" "${gate}")"
            renders=$((renders + 1))
            [[ "${out}" == *"task show E-2123 --all-fields --db main"* ]] \
                || saw_all_fields="no"
            [[ "${out}" == *"task show E-2123 --text"* ]] && saw_bare_text="yes"
        done
    done
    assert_eq "${tmpl}: reads the task with --all-fields --db main (${renders} renderings)" \
        "yes" "${saw_all_fields}"
    assert_eq "${tmpl}: no rendering still says \`task show ... --text\`" \
        "no" "${saw_bare_text}"
done

# ── 3. the trap: --text names two flags, and only one moved ─────────────────
# claim.md.tmpl carries the read toggle and the write flag on ONE line. A sweep
# that matched the string rather than the verb would have rewritten
# `task update --text <path>` into `--all-fields <path>` — an invalid command
# that no test above would have caught, because the claim handoff would still
# have contained the string "--all-fields".
section "3. The claim handoff keeps its write flag intact"

claim="$(render claim todo 2 true)"
assert_contains "the fold-your-context line still writes with \`task update ... --text <path>\`" \
    "endless task update E-2123 --text <path> --db main" "${claim}"
assert_contains "and the read on the same line is the widened one" \
    "endless task show E-2123 --all-fields --db main" "${claim}"

# ── 4. the write flags the sweep must not have touched ──────────────────────
# Asked of the shipped CLI, not of the source the sweep edited: if a `sed` had
# reached `task_cmd.py`, `cli.py` or `lesson_cmd.py`, these options would be
# gone or renamed and every one of these lookups would come back empty.
section "4. The content flags still exist on the commands that own them"

help_has() {
    local label="$1" needle="$2"; shift 2
    local out
    out="$(uv run endless "$@" --help 2>&1)"
    assert_contains "${label}" "${needle}" "${out}"
}

help_has "task add --text still sets the plan inline"      "--text TEXT"      task add
help_has "task add --text-file still loads it from a file" "--text-file"      task add
help_has "task update --text survives the sweep"           "--text TEXT"      task update
help_has "task update --text-file survives the sweep"      "--text-file"      task update
help_has "lesson write --text survives the sweep"          "--text"           lesson write
help_has "task show --text is still there as a single-field read" "--text" task show
help_has "task show --all-fields is what the handoffs now call" "--all-fields" task show

# ── 5. the guide agrees with what the handoff renders ───────────────────────
# The guide is where a session is sent when the handoff says "run
# `endless guide`". A guide that still says `--text` would undo the templates
# one turn later.
section "5. The guide's own instructions match"

idx="$(cat docs/guide/index.md)"
assert_contains "happy-path step 1 reads the task with --all-fields" \
    'endless task show <id> --all-fields' "${idx}"
assert_not_contains "and no \`task show <id> --text\` example remains in index.md" \
    'task show <id> --text' "${idx}"

orch="$(cat docs/guide/orchestration.md)"
assert_contains "orchestration.md describes the handoff's pointer as --all-fields" \
    'endless task show <id> --all-fields' "${orch}"

tasks_md="$(cat docs/guide/tasks.md)"
assert_contains "tasks.md still documents the write flag it always did" \
    'endless task update <id> --text-file /path/to/plan.md' "${tasks_md}"
assert_contains "tasks.md lists --all-fields on task show" \
    'endless task show <id> --all-fields' "${tasks_md}"
assert_contains "tasks.md now names the flag that shows analysis" \
    'task show --analysis' "${tasks_md}"

# ── 6. the inert assertion was reverted, not left looking like coverage ─────
# In TestHandlePostToolUseSession_NonClaimYieldsNoHandoff the flag on that
# string is arbitrary — the fixture is a list of commands that are NOT a claim
# and must yield "". Sweeping it asserted nothing and made the commit read as
# though the handoff path had been covered when it had not been.
section "6. The non-claim fixture string is back to --text"

fixture="$(cat internal/hookcmd/claim_handoff_test.go)"
assert_contains "the non-claim fixture carries the flag it always carried" \
    '"endless task show E-10 --text --db main"' "${fixture}"

# ── 7. the diff touched no write-flag site ──────────────────────────────────
# The whole-change shape, asked of git rather than of a reviewer's memory.
section "7. The change touched only read sites"

BASE="$(git merge-base main HEAD 2>/dev/null)" \
    || setup_error "cannot resolve a merge base with main"
# The suite file itself is excluded: it necessarily quotes both flags, and
# scanning it would make this check assert against its own text.
SRC=(-- . ":(exclude).endless/")
changed="$(git diff --name-only "${BASE}" "${SRC[@]}" | sort)"

# Added and removed lines only. A context line is not a change, and the
# tasks.md hunk that documents `--analysis` sits directly under the row that
# documents `--text-file`.
assert_eq "no --text-file on any changed line" "0" \
    "$(git diff "${BASE}" "${SRC[@]}" \
        | grep -E '^[+-]' | grep -Ev '^(\+\+\+|---) ' \
        | grep -c -- '--text-file' || true)"

for untouched in src/endless/task_cmd.py src/endless/cli.py \
                 src/endless/lesson_cmd.py src/endless/worktree_cmd.py \
                 internal/events; do
    assert_not_contains "left ${untouched} alone" "${untouched}" "${changed}"
done

# The plan-divergence message deliberately keeps `--text`: it offers the
# operator the one field under comparison, and widening it would dilute the
# message. Asserted so the decision is recorded rather than assumed.
assert_contains "the plan-divergence message still offers the single field" \
    'endless task show E-{task_id} --text' "$(cat src/endless/worktree_cmd.py)"

summary
