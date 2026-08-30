#!/usr/bin/env bash
#
# E-1825 verification — "Replace union-sense 'dirty' with 'unsettled' in
# worktree-lifecycle code and docs" (ED-1540 vocabulary).
#
# This is a rename/vocabulary change with no runtime-behavior delta, so the
# checks are:
#   1. The overloaded working-tree-state term ('dirty'/'dirt') is purged from
#      the worktree-lifecycle code + guide.
#   2. The new vocabulary is present at the key symbol/wording sites
#      (union-sense -> 'unsettled'; uncommitted sub-state -> 'modified').
#   3. The affected Go packages and Python test cascade pass.
#   4. The Go tree builds.
#
# Fail-fast: the first failing check exits non-zero immediately. Run from
# anywhere inside the worktree:  ./tests/tasks/e-1825-verify.sh
#
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

# ── 1. overloaded term purged from worktree-lifecycle code + guide ───────────
# The union-sense 'dirty' and its mass-noun 'dirt' must be gone from the
# scoped source. config/errors.go's "ConfigDir" is not the word 'dirt' and is
# excluded by the whole-word match; the config-path test's verb usage
# ("writes ... dirty the DB", E-1140) is a different concept, out of scope.
section "1. Overloaded 'dirty'/'dirt' purged from scoped code + guide"
residue=$(grep -rInw -i 'dirty\|dirt' \
    internal/monitor internal/sessionstatuscmd internal/events \
    src/endless/worktree_cmd.py src/endless/cli.py src/endless/matchers.py \
    docs/guide/orchestration.md 2>/dev/null || true)
if [[ -n "${residue}" ]]; then
    fail "residual 'dirty'/'dirt' in scoped code" "${residue}"
fi
pass "no residual union/modified-sense 'dirty'/'dirt' in scoped code"

# ── 2. new vocabulary present at the key sites ───────────────────────────────
section "2. New vocabulary present at rename sites"
assert_grep() { # DESC PATTERN FILE
    if grep -Iq -- "$2" "$3"; then pass "$1"; else fail "$1" "missing: $2  in  $3"; fi
}
assert_grep "monitor: SessionStatusRow.Unsettled field"        'Unsettled  bool'                    internal/monitor/session_status.go
assert_grep "monitor: taskWorktreeUnsettled"                    'func taskWorktreeUnsettled'         internal/monitor/reap_worktrees.go
assert_grep "monitor: AnnotateSessionStatusUnsettled"          'func AnnotateSessionStatusUnsettled' internal/monitor/reap_worktrees.go
assert_grep "render: unsettledMark"                             'func unsettledMark'                 internal/sessionstatuscmd/session_status.go
assert_grep "render: legend '◆ unsettled'"                      '◆ unsettled'                        internal/sessionstatuscmd/session_status.go
assert_grep "python: _guard_modified_worktree"                  'def _guard_modified_worktree'       src/endless/worktree_cmd.py
assert_grep "cli: drop help 'modified/unlanded/foreign'"        'modified/unlanded/foreign'          src/endless/cli.py
assert_grep "guide: 'modified/unlanded/foreign'"                'modified/unlanded/foreign'          docs/guide/orchestration.md

# The renamed public Go symbols must have no lingering old callers anywhere.
section "2b. No lingering references to renamed Go symbols"
old=$(grep -rInw 'taskWorktreeDirty\|AnnotateSessionStatusDirty\|dirtyMark\|_guard_dirty_worktree' \
    internal src 2>/dev/null || true)
if [[ -n "${old}" ]]; then fail "old symbol still referenced in code" "${old}"; fi
pass "no old symbol references in internal/ or src/"

# ── 3. affected test suites pass ─────────────────────────────────────────────
section "3. Affected Go packages"
if go test ./internal/monitor/... ./internal/sessionstatuscmd/... ./internal/events/...; then
    pass "go test (monitor, sessionstatuscmd, events)"
else
    fail "go test for affected packages"
fi

section "4. Python cascade (renamed guard + its patchers)"
if uv run pytest -q \
    tests/test_worktree_land_modified_guard.py \
    tests/test_worktree_land_post_land_residue.py \
    tests/test_worktree_land_post_land_script.py; then
    pass "pytest (modified guard cascade)"
else
    fail "pytest for the guard cascade"
fi

# ── 5. build ─────────────────────────────────────────────────────────────────
section "5. Go build"
if go build ./...; then pass "go build ./..."; else fail "go build ./..."; fi

printf '\n%sALL PASSED%s\n\n' "${GREEN}${BOLD}" "${RESET}"
