#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1562 and records what was true when E-1562
# landed. Edit it only if you ARE E-1562. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1562: a research justification is typed task content, not a
# `## Justification` heading composed into notes.
#
# The heading produced a refusal nobody intended: `--justification` refused
# when the section already existed, so a task that had EVER been research could
# never be retyped to research again, and a justification could never be
# reworded. As its own content it is an ordinary write.
#
# What is verified here:
#   A. Fail-fast: this task's own Go and Python tests.
#   B. The claims, named one by one — the defect's round trip, rewording, the
#      type gate, the display order.
#   C. The move of the 29 existing headings: notes kept or pruned, repeatable,
#      and a rebuild from the ledger agreeing with the live apply — the
#      property the one-map design and the move's timestamp exist for.
#   D. Nothing of the heading composer survives, and the post-land script that
#      runs the move on main is in place.
#
# See E-1562's plan (endless task show E-1562 --all-fields).
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# The Python suite shells out to bin/endless-go, so build it from this tree
# first — a stale binary would decide the result instead of the source.

if out=$(go build -o bin/endless-go ./cmd/endless-go 2>&1); then
    report_pass "endless-go builds from this tree"
else
    report_fail "endless-go builds from this tree" "exit 0" "${out}"
    summary
fi

go_pkg() { # go_pkg <package> <label>
    if out=$(go test "$1" 2>&1); then
        report_pass "go test: $2"
    else
        report_fail "go test: $2" "exit 0" "$(printf '%s' "${out}" | tail -30)"
        summary
    fi
}

py_file() { # py_file <path> <label>
    if out=$(uv run pytest -q "$1" 2>&1); then
        report_pass "pytest: $2"
    else
        report_fail "pytest: $2" "exit 0" "$(printf '%s' "${out}" | tail -30)"
        summary
    fi
}

go_pkg ./internal/taskcontent/ "the content vocabulary, Justification first"
go_pkg ./internal/events/      "both write paths, and the section move"
py_file tests/test_research_gate.py         "the research gate and the justification write path"
py_file tests/test_justification_content.py "justification as content: CLI, clear, display order"
py_file tests/test_context_content.py       "context still renders first on an implementation task"

# ---------------------------------------------------------------------------
section "B. The claims, named"
# ---------------------------------------------------------------------------
# Section A already ran these. They are re-run by name so this report states
# each claim rather than collapsing them into green files.

py_claim() { # py_claim <file::test> <claim>
    if out=$(uv run pytest -q "$1" 2>&1); then
        report_pass "$2"
    else
        report_fail "$2" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    fi
}

G=tests/test_research_gate.py
J=tests/test_justification_content.py

py_claim "${G}::test_retype_research_todo_research_with_new_justification" \
    "THE DEFECT: research → todo → research with a new justification succeeds and stores the second text"
py_claim "${G}::test_justification_rewords_in_place" \
    "--justification twice rewords it in place instead of refusing"
py_claim "${G}::test_stored_justification_satisfies_the_gate_on_retype" \
    "the gate reads the stored row: retyping back to research needs no restatement"
py_claim "${G}::test_cleared_justification_does_not_satisfy_the_gate" \
    "and a justification cleared in the same call leaves nothing to pass it"
py_claim "${J}::test_cli_add_update_and_clear" \
    "--clear justification falls out of the clearable-content machinery"
py_claim "${G}::test_justification_refused_on_todo_add" \
    "type gate: --justification on a todo add is refused, naming research and brainstorm"
py_claim "${G}::test_justification_refused_on_todo_update" \
    "type gate: and on a todo update, writing nothing"
py_claim "${G}::test_justification_accepted_on_brainstorm" \
    "brainstorm may carry one"
py_claim "${G}::test_justification_leaves_notes_alone" \
    "writing a justification never touches notes"
py_claim "${J}::test_show_renders_justification_before_context" \
    "task show renders Justification before Context"

# ---------------------------------------------------------------------------
section "C. Moving the existing headings"
# ---------------------------------------------------------------------------

py_claim "${G}::test_move_sections_moves_and_prunes" \
    "the move keeps other notes prose, deletes a notes row it empties, and is repeatable"
py_claim "${G}::test_move_sections_dry_run_writes_nothing" \
    "--dry-run emits nothing"

go_claim() { # go_claim <regex> <claim>
    if out=$(go test ./internal/events/ -run "$1" -count=1 2>&1); then
        report_pass "$2"
    else
        report_fail "$2" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    fi
}

go_claim '^TestSectionMove_LiveAndRebuildAgree$' \
    "rebuilding from the ledger reproduces the moved content, including a task whose notes were rewritten after the heading was filed"
go_claim '^TestTaskContent_LiveAndRebuildAgree$' \
    "live apply and rebuild agree for every content name, justification included"
go_claim '^TestSplitSection$' \
    "a section ends at the next heading; text around it is kept"
go_claim '^TestSectionMove_SkipsAnExistingTarget$' \
    "a task already holding a justification is skipped, never overwritten"

# ---------------------------------------------------------------------------
section "D. Nothing of the heading composer survives; the move runs on land"
# ---------------------------------------------------------------------------

if hits=$(grep -rn "_compose_justification_notes\|_JUSTIFICATION_HEADING_RE" src/ internal/ cmd/ 2>/dev/null); then
    report_fail "the heading composer and its regex are gone from the source" "no matches" "${hits}"
else
    report_pass "the heading composer and its regex are gone from the source"
fi

# A string literal opening with the heading is what composing one looks like;
# prose that names the old shape in a comment or docstring is not.
if hits=$(grep -rn --include='*.py' '"## Justification' src/ 2>/dev/null); then
    report_fail "no source builds a ## Justification heading" "no matches" "${hits}"
else
    report_pass "no source builds a ## Justification heading"
fi

SCRIPT=.endless/hooks/post-land/e-1562.sh
if [[ -x "${SCRIPT}" ]] && bash -n "${SCRIPT}"; then
    report_pass "the post-land script exists, is executable and parses"
else
    report_fail "the post-land script exists, is executable and parses" "executable, valid bash" "$(ls -l "${SCRIPT}" 2>&1)"
fi
assert_contains "it runs the move against main" \
    "db move-section --from notes --to justification" "$(cat "${SCRIPT}")"
assert_contains "and names --db main explicitly" "--db main" "$(cat "${SCRIPT}")"

help="$(uv run endless db move-section --help 2>&1)"
assert_contains "endless db move-section is a command" "--dry-run" "${help}"

summary
