#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2183 and records what was true when E-2183
# landed. Edit it only if you ARE E-2183. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2183: a worktree-scoped `just install` runs its machine-level config
# recipes (go-work-init, dev-sandbox-init, claude-settings-init) from MAIN's
# justfile, and `build` from the branch's. Machine-level config comes from
# main; candidate behaviour comes from the branch.
#
# The durable coverage is tests/test_install_worktree_recipes.py: a real repo
# whose task branch carries an older justfile than main, both stubbing the four
# recipes with loggers, asserting which justfile each call resolved to.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

section "Fail-fast: the task's own test"
if out="$(uv run pytest -q tests/test_install_worktree_recipes.py 2>&1)"; then
    report_pass "config recipes resolve from main's justfile, build from the branch's"
else
    report_fail "config recipes resolve from main's justfile, build from the branch's" "${out}"
    summary
fi

section "The recipe as committed"
body="$(just --show install)"
for r in go-work-init dev-sandbox-init claude-settings-init; do
    assert_contains "install runs ${r} through main's justfile" "recipe ${r}" "${body}"
    assert_not_contains "install never runs a bare 'just ${r}'" "just ${r}" "${body}"
done
assert_contains "install builds with the branch's own justfile" "just build" "${body}"

section "The one-worktree recovery is documented beside the recipe"
assert_contains "the detect command is in the justfile" \
    "grep -l '\"hooks\"' .endless/worktrees/*/.claude/settings.local.json" "$(cat justfile)"
assert_contains "the repair command is in the justfile" \
    "--working-directory <worktree> claude-settings-init" "$(cat justfile)"

summary
