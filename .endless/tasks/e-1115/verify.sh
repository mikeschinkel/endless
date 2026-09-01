#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1115 and records what was true when E-1115
# landed. Edit it only if you ARE E-1115. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1115 verification — regression coverage for _tmux_window_pane_ids.
#
# What E-1115 shipped (at land time):
#   - Three direct unit tests for src/endless/session_cmd.py's
#     `_tmux_window_pane_ids` helper, filed under `tests/test_session_cd.py`.
#   - No src/ change. The substantive fix (`-t $TMUX_PANE`) belongs to
#     E-1395 (landed 2026-05-17, commit e21242288). This suite proves the
#     coverage E-1115 added, not the fix itself.
#
# What is verified here:
#   A. The three tests exist in tests/test_session_cd.py.
#   B. All three pass on this worktree.
#   C. `_tmux_window_pane_ids` still calls `tmux list-panes` with
#      `-t "$TMUX_PANE"` — a fail-fast check against silent revert.
#
# See .endless/plans/E-1115.md for the ED-1550 rationale.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
TESTS="${WT}/tests/test_session_cd.py"
SESSION_CMD="${WT}/src/endless/session_cmd.py"

section "Tests are present"
for name in \
    test_tmux_window_pane_ids_scopes_by_calling_pane \
    test_tmux_window_pane_ids_returns_none_outside_tmux \
    test_tmux_window_pane_ids_returns_none_when_pane_unset ; do
    if grep -q "^def ${name}(" "${TESTS}"; then
        report_pass "${name} exists"
    else
        report_fail "${name} exists" "def ${name}(...) in ${TESTS}" "not found"
    fi
done

section "Tests pass"
if out=$(cd "${WT}" && uv run pytest -q \
        tests/test_session_cd.py::test_tmux_window_pane_ids_scopes_by_calling_pane \
        tests/test_session_cd.py::test_tmux_window_pane_ids_returns_none_outside_tmux \
        tests/test_session_cd.py::test_tmux_window_pane_ids_returns_none_when_pane_unset \
        2>&1); then
    report_pass "pytest: all three regression tests pass"
else
    report_fail "pytest" "exit 0" "$(printf '%s' "$out" | tail -25)"
fi

section "Helper still scopes by TMUX_PANE"
if grep -q '"tmux", "list-panes", "-t", pane' "${SESSION_CMD}"; then
    report_pass "_tmux_window_pane_ids passes -t \$TMUX_PANE to list-panes"
else
    report_fail "_tmux_window_pane_ids scoping" \
        'subprocess.run call containing ["tmux", "list-panes", "-t", pane, ...]' \
        "not found — E-1395's fix may have been reverted"
fi

summary
