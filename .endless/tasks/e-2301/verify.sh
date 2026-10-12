#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2301 and records what was true when E-2301
# landed. Edit it only if you ARE E-2301. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2301: an epic is startable (claim, spawn, prime) on its context with no
# plan, as brainstorm and research are (E-1994) — a stopgap until E-2254 gates
# it on its objective and strategy.
#
# What is verified here:
#   A. Fail-fast: the gate, prime and handoff test files, whole.
#   B. Each deliverable's own test runs and passes, by name:
#      - a framed epic passes spawn and claim, and claim starts it;
#      - an unframed epic, and an unframed brainstorm, are refused naming
#        --context-file;
#      - a framed bugfix is still refused for having no plan;
#      - a primed epic does not draft, and its handoff starts from its framing;
#      - the epic handoff reads "context and plan" and stops on "context or plan".
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

just build >/dev/null 2>&1 || setup_error "just build failed: the handoff templates are embedded in bin/endless-go"

if out=$(go test ./internal/templatecmd 2>&1); then
    report_pass "go test: templatecmd"
else
    report_fail "go test: templatecmd" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

PYFILES=(tests/test_prime.py tests/test_spawn_gate.py tests/test_handoff.py)
if out=$(uv run pytest "${PYFILES[@]}" -q 2>&1); then
    report_pass "pytest: prime, spawn gate, handoff"
else
    report_fail "pytest: prime, spawn gate, handoff" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# ---------------------------------------------------------------------------
section "B. Each deliverable's test runs and passes"
# ---------------------------------------------------------------------------

pycontract() {
    local t="$1"
    if uv run pytest "tests/test_prime.py::${t}" -q 2>&1 | grep -q "1 passed"; then
        report_pass "pytest runs and passes: ${t}"
    else
        report_fail "pytest runs and passes: ${t}" "1 passed" "not run, or failed"
    fi
}
pycontract test_a_framed_epic_is_not_refused_as_planless
pycontract test_claim_starts_a_framed_epic
pycontract test_an_unframed_epic_is_refused_naming_context
pycontract test_an_unframed_brainstorm_is_refused_naming_context
pycontract test_a_framed_bugfix_still_needs_a_plan
pycontract test_a_framed_todo_still_needs_a_plan
pycontract test_prime_does_not_draft_for_a_framed_epic
pycontract test_a_primed_epic_handoff_starts_from_its_framing

t=TestRender_EpicHandoff_ContextOrPlan
if go test ./internal/templatecmd -run "^${t}$" -v 2>&1 | grep -q "^--- PASS: ${t}"; then
    report_pass "go test runs and passes: ${t}"
else
    report_fail "go test runs and passes: ${t}" "--- PASS: ${t}" "no PASS line"
fi

summary
