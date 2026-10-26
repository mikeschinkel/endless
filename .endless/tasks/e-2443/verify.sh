#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2443 and records what was true when E-2443
# landed. Edit it only if you ARE E-2443. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2443 — guide subsections addressed as `endless guide <section>/<sub>`.
#
# Rendering, refusals, --list, map-file addresses and the layout rules are
# proved by the unit tests over temp guide trees (the real guide has no
# subsection yet; the first arrives with a later task). The end-to-end checks
# drive this worktree's own `endless` against the real docs/guide/.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

ROOT=$(git rev-parse --show-toplevel) || setup_error "not inside a git worktree"
cd "${ROOT}" || setup_error "cannot cd to ${ROOT}"
PY="${ROOT}/.venv/bin/python"
[[ -x "${PY}" ]] || setup_error "no ${PY}; run 'uv sync' in the worktree"
endless_wt() { "${ROOT}/.venv/bin/endless" "$@"; }

section "1. Unit tests (fail fast)"
if out=$("${PY}" -m pytest -q tests/test_guide_subsections.py tests/test_guide_map.py \
        tests/test_guide_conditionals.py tests/test_agent_help.py 2>&1); then
    report_pass "guide subsection, map, conditionals and banner tests"
else
    report_fail "guide unit tests" "all pass" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

section "2. The help directory is _help/, and nothing names the old path"
assert_eq "docs/guide/help/ is gone" "no" "$([[ -e docs/guide/help ]] && echo yes || echo no)"
assert_eq "docs/guide/_help/_topics.md exists" "yes" \
    "$([[ -f docs/guide/_help/_topics.md ]] && echo yes || echo no)"
assert_eq "no 'guide/help' in src, tests, justfile, .claude/commands" "" \
    "$(git grep -l 'guide/help' -- src tests justfile .claude/commands)"
if out=$("${PY}" -m endless.guide_map check 2>&1); then
    report_pass "guide-check passes"
else
    report_fail "guide-check passes" "exit 0" "$(printf '%s' "${out}" | tail -20)"
fi

section "3. The real guide through this worktree's endless"
listing=$(endless_wt guide --list 2>&1)
assert_contains "--list shows the tasks section" "tasks" "${listing}"
assert_not_contains "--list shows no _help entry" "_help" "${listing}"
assert_not_contains "--list shows no _topics entry" "_topics" "${listing}"
assert_not_contains "--list shows no index entry" "index" "${listing}"
out=$(endless_wt guide _help 2>&1); rc=$?
assert_eq "guide _help is refused" "1" "${rc}"
assert_contains "guide _help is refused as unknown" "Unknown section '_help'" "${out}"
out=$(endless_wt guide tasks/nope 2>&1); rc=$?
assert_eq "guide tasks/nope is refused" "1" "${rc}"
assert_contains "the refusal names the section's subsections" \
    "Unknown subsection 'tasks/nope'" "${out}"
out=$(endless_wt guide index/x 2>&1); rc=$?
assert_eq "guide index/x is refused" "1" "${rc}"
out=$(endless_wt guide tasks 2>&1); rc=$?
assert_eq "guide tasks still prints" "0" "${rc}"
assert_contains "guide tasks prints the tasks section" "# Tasks" "${out}"

summary
