#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1865 and records what was true when E-1865
# landed. Edit it only if you ARE E-1865. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1865 verification — "Add `task unsettled` to explain why a worktree is
# unsettled (modified vs unlanded)".
#
# The command's value rests on one invariant: its explanation must come from the
# SAME probe that raises the ◆ in `session status`, or the two can disagree and
# the explanation becomes a second, drifting opinion. So the checks are:
#   1. The predicate has ONE implementation — taskWorktreeUnsettled delegates to
#      the detail probe rather than re-running its own git commands.
#   2. The command is registered and reachable, with both list and item forms.
#   3. The Go core (including the legacy-equivalence oracle) passes.
#   4. The Python renderer tests pass.
#   5. The tree builds.
#
# Fail-fast: the first failing check exits non-zero immediately. Run from
# anywhere inside the worktree:  endless task verify E-1865
#
# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

ROOT=$(git rev-parse --show-toplevel) || { echo "not in a git repo" >&2; exit 1; }
cd "${ROOT}" || exit 1

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; BOLD=""; RESET=""
fi

pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; }
fail() { printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"; [[ -n "${2:-}" ]] && printf '      %s\n' "$2"; exit 1; }
section() { printf '\n%s%s%s\n' "${BOLD}" "$1" "${RESET}"; }

assert_grep() { # <label> <pattern> <file>
    if grep -qI -- "$2" "$3" 2>/dev/null; then pass "$1"; else fail "$1" "missing $2 in $3"; fi
}

# ── 1. the predicate has exactly one implementation ──────────────────────────
# This is the whole point of the task: the ◆ and its explanation must read the
# same probe. If taskWorktreeUnsettled ever grows its own `git status` /
# `rev-list` calls again, the two surfaces can drift apart silently.
section "1. Single source of truth for the unsettled predicate"
assert_grep "taskWorktreeUnsettled delegates to the detail probe" \
    'TaskWorktreeUnsettledDetail(projectID, taskID).Unsettled()' \
    internal/monitor/reap_worktrees.go

body=$(awk '/^func taskWorktreeUnsettled\(/,/^}/' internal/monitor/reap_worktrees.go)
if grep -q 'runGit' <<<"${body}"; then
    fail "taskWorktreeUnsettled runs its own git commands again" "${body}"
fi
pass "taskWorktreeUnsettled runs no git of its own"

assert_grep "probe core exists (path-based, DB-free)" \
    'func WorktreeUnsettledAt(' internal/monitor/worktree_unsettled.go
assert_grep "auto-managed churn still counts toward unsettled" \
    'len(d.Modified)+len(d.AutoManaged) > 0' internal/monitor/worktree_unsettled.go

# ── 2. the command is registered and wired ───────────────────────────────────
section "2. Command registration"
assert_grep "cli: task unsettled registered"   '@task_cmd.command("unsettled")' src/endless/cli.py
assert_grep "cli: dispatches both forms"       'unsettled_list, unsettled_item' src/endless/cli.py
assert_grep "task_cmd: unsettled_list"         'def unsettled_list('            src/endless/task_cmd.py
assert_grep "task_cmd: unsettled_item"         'def unsettled_item('            src/endless/task_cmd.py
assert_grep "go: session-query subcommand"     '"worktree-unsettled"'           internal/sessionquerycmd/session_query.go

# The renderer must never recompute the verdict — it reads it from the probe.
# Scoped to the E-1865 block (from _unsettled_probe to the research-gate helpers
# that follow it) and matched on real invocations: a `_git(` call, or a
# subprocess whose argv starts with git. Bare mentions of "git rev-list" are
# fine — _PROBE_ERROR_LABELS carries them as human labels.
region=$(awk '/^def _unsettled_probe\(/,/^# E-1544: research-gate helpers/' \
    src/endless/task_cmd.py)
if grep -qE '(^|[^_[:alnum:]])_git\(|subprocess\.run\(\s*\[\s*"git"' <<<"${region}"; then
    fail "Python renderer runs git itself; the verdict must come from Go"
fi
if ! grep -q 'session-query", "worktree-unsettled' <<<"${region}"; then
    fail "Python renderer does not call the shared Go probe"
fi
pass "Python renderer defers the verdict to the Go probe"

# ── 3. Go core, including the legacy-equivalence oracle ──────────────────────
section "3. Go unsettled core"
if go test ./internal/monitor/... ./internal/sessionquerycmd/... ./internal/sessionstatuscmd/...; then
    pass "go test (monitor, sessionquerycmd, sessionstatuscmd)"
else
    fail "go test for the unsettled core"
fi

# ── 4. Python renderer ───────────────────────────────────────────────────────
section "4. Python renderer tests"
if uv run pytest -q tests/test_task_unsettled.py tests/test_task_landed.py; then
    pass "pytest (task unsettled + landed sibling)"
else
    fail "pytest for the renderer"
fi

# ── 5. build ─────────────────────────────────────────────────────────────────
section "5. Go build"
if go build ./...; then pass "go build ./..."; else fail "go build ./..."; fi

printf '\n%sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"
