#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1523 and records what was true when E-1523
# landed. Edit it only if you ARE E-1523. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1523 verification — GetActiveTasks stops dropping in-progress
# tasks when tasks.description is NULL.
#
# Before: the SELECT in monitor.GetActiveTasks scanned tasks.description
# directly into a Go string. tasks.description is nullable, so any row
# with NULL description hit a Scan error and the loop silently
# continued — those tasks vanished from SessionStart's task-context
# prompt. After: the SELECT wraps description in COALESCE(description,
# ''), matching the pattern used throughout internal/web/queries.go.
#
#   endless task verify E-1523
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to worktree root"

TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
trap 'rm -rf "${TMP}"' EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
section "1. Unit gate (fail fast) — GetActiveTasks tests"

if go test ./internal/monitor/ -run TestGetActiveTasks -count=1 \
        >"${TMP}/go.log" 2>&1; then
    report_pass "go test ./internal/monitor/ -run TestGetActiveTasks"
else
    report_fail "go test ./internal/monitor/ -run TestGetActiveTasks" \
        "exit 0" "$(tail -25 "${TMP}/go.log")"
    summary
fi

# ── 2. the SELECT wraps description in COALESCE ─────────────────────────────
section "2. GetActiveTasks SELECT wraps description in COALESCE"

SRC="${WT}/internal/monitor/task.go"
assert_eq "task.go present" "yes" "$([[ -f "${SRC}" ]] && echo yes || echo no)"

# The whole SELECT string as one line (tolerates the "..."+ concatenation shape).
select_line="$(awk '/db\.Query\(/,/,$/' "${SRC}" | tr -d '\n' | tr -s ' ')"
assert_contains "SELECT wraps description in COALESCE(description, '')" \
    "COALESCE(description, '')" "${select_line}"
assert_not_contains "SELECT no longer scans bare 'description' into a Go string" \
    ", phase, description," "${select_line}"

# ── 3. the NULL-description regression test exists in the durable suite ─────
section "3. Regression test survives in the durable Go suite"

TEST="${WT}/internal/monitor/task_test.go"
assert_contains "task_test.go names the NULL-description regression test" \
    "TestGetActiveTasks_NullDescriptionNotDropped" "$(cat "${TEST}")"

summary
