#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2317 and records what was true when E-2317
# landed. Edit it only if you ARE E-2317. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2317 — tests for what counts as a decision, in the decisions guide, and
# every filing prompt (brainstorm handoff, guide pages) pointing to them
# instead of asking for decisions unconditionally.
#
# Checks, fail-fast first:
#   1. The task's own tests: tests/test_guide_decision_tests.py and the Go
#      brainstorm-handoff render tests.
#   2. `endless guide decisions`, as an agent reads it, carries the section.
#   3. The rendered brainstorm handoff gates both `decision add` mentions.
#   4. The guide cross-reference is in sync and lists the new topic.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || setup_error "not inside a git worktree"
cd "${REPO_ROOT}" || setup_error "cannot cd to ${REPO_ROOT}"
for tool in go uv; do
    command -v "${tool}" >/dev/null 2>&1 || setup_error "${tool} not on PATH"
done

WORK_TMP=$(mktemp -d)
trap 'rm -rf "${WORK_TMP}"' EXIT

section "1. The task's own tests (fail fast)"
if out=$(uv run --quiet pytest -q tests/test_guide_decision_tests.py 2>&1); then
    report_pass "pytest tests/test_guide_decision_tests.py"
else
    report_fail "pytest tests/test_guide_decision_tests.py" "pass" "$(printf '%s' "${out}" | tail -30)"
    summary
fi
if out=$(go test ./internal/templatecmd/ -count=1 -run 'TestRender_Brainstorm' 2>&1); then
    report_pass "go test ./internal/templatecmd -run TestRender_Brainstorm"
else
    report_fail "go test ./internal/templatecmd -run TestRender_Brainstorm" "ok" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

section "2. endless guide decisions"
guide=$(as_agent uv run --quiet endless guide decisions 2>&1)
assert_contains "has the 'Is it a decision?' section" "## Is it a decision?" "${guide}"
assert_contains "all three tests must pass" "**All three must pass.**" "${guide}"
assert_contains "test: real alternative" "**Real alternative.**" "${guide}"
assert_contains "test: outlives the task" "**Outlives the task.**" "${guide}"
assert_contains "test: the user made it" "**The user made it.**" "${guide}"
assert_contains "at least one qualifying kind" "It must also be **at least one** of these kinds" "${guide}"
assert_contains "kind: user-visible convention, any interface" "web UI, API, CLI or other" "${guide}"
assert_contains "zero is normal" "Zero decisions is the normal outcome of a session." "${guide}"
assert_contains "failures route to a lesson" "**Anything that fails a test is a lesson, not a decision**" "${guide}"
assert_contains "unclear means lesson" "When it is unclear which, it is a lesson." "${guide}"
assert_not_contains "unconditional 'Always document' is gone" "Always document decisions" "${guide}"

section "3. Brainstorm handoff"
BIN="${WORK_TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go >"${WORK_TMP}/build.log" 2>&1 \
    || setup_error "go build ./cmd/endless-go: $(tail -20 "${WORK_TMP}/build.log")"
vars='{"spawned_id":1,"label_prefix":"E-1","title":"T","task_type":"brainstorm","worktree_path":"/w","branch":"b","child_count":0,"children_state":"none"}'
handoff=$(printf '%s' "${vars}" | "${BIN}" template render handoff/brainstorm 2>&1) \
    || setup_error "render handoff/brainstorm: ${handoff}"
adds=$(grep -o 'endless decision add' <<<"${handoff}" | wc -l | tr -d ' ')
gated=$(grep -o 'real alternative, outlives the task, the user made it (`endless guide decisions`); zero is normal' <<<"${handoff}" | wc -l | tr -d ' ')
assert_eq "still asks for decisions (both places)" "2" "${adds}"
assert_eq "each ask carries the tests, pointer and zero-is-normal" "${adds}" "${gated}"

section "4. Guide cross-reference"
if out=$(uv run --quiet python -m endless.guide_map check 2>&1); then
    report_pass "guide_map check (index block in sync)"
else
    report_fail "guide_map check" "exit 0" "$(printf '%s' "${out}" | tail -20)"
fi
assert_contains "index lists the 'is it a decision?' topic" "| is it a decision? | decisions |" "$(cat docs/guide/index.md)"

summary
