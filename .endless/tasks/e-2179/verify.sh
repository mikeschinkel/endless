#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2179 and records what was true when E-2179
# landed. Edit it only if you ARE E-2179. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2179 verification — `endless task chat` and chat-only session mode are gone.
#
# THE CLAIMS
#   C1  The command is gone: `endless task chat` exits as an unknown command.
#   C2  Nothing is left behind: no start_chat, StartChatSession, actionChat or
#       `task chat` in source, tests or the guide, and no `chat` default matcher.
#   C3  The declaration gate no longer offers chat, and still offers
#       `endless task claim` and `endless task show` (pinned in
#       internal/hookcmd/declaration_gate_test.go, run in section A).
#   C4  The declaration context tells a conversation it needs no action.
#
# ISOLATION: unit tests, a --help-level CLI call and greps. No database.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || setup_error "not in a git tree"
cd "${ROOT}" || setup_error "cannot cd to ${ROOT}"

section "A. Fail-fast: the touched packages' unit tests"
if out=$(go test ./internal/hookcmd/ ./internal/monitor/ ./internal/sessionstate/ 2>&1); then
    report_pass "go test hookcmd, monitor, sessionstate"
else
    report_fail "go test hookcmd, monitor, sessionstate" "${out}"
    summary
fi
if out=$(uv run --quiet pytest -q tests/test_agent_help.py tests/test_matchers.py 2>&1); then
    report_pass "pytest test_agent_help, test_matchers"
else
    report_fail "pytest test_agent_help, test_matchers" "${out}"
    summary
fi

section "B. C1 — the command is gone"
out=$(uv run --quiet endless task chat 2>&1); rc=$?
assert_eq "endless task chat exits non-zero" "nonzero" "$([[ ${rc} -ne 0 ]] && echo nonzero || echo zero)"
assert_contains "endless task chat is an unknown command" "No such command" "${out}"
assert_not_contains "task --help does not list chat" "chat-only" "$(uv run --quiet endless task --help 2>&1)"

section "C. C2 — no residue in source, tests or the guide"
residue=$(grep -rnI -e 'task chat' -e 'start_chat' -e 'StartChatSession' -e 'actionChat' \
    src/ internal/ cmd/ docs/guide/ tests/ 2>/dev/null \
    | grep -v '^internal/hookcmd/declaration_gate_test.go:')  # asserts the absence
assert_eq "no chat references remain" "" "${residue}"
matcher=$(uv run --quiet python -c \
    'from endless.matchers import DEFAULT_MATCHERS as M; print(sorted(m["type"] for m in M))' 2>&1)
assert_not_contains "DEFAULT_MATCHERS has no chat entry" "'chat'" "${matcher}"

section "D. C4 — the declaration context"
assert_contains "a conversation needs no action" \
    "no action is needed — nothing is refused until you write" \
    "$(cat internal/monitor/task.go)"

summary
