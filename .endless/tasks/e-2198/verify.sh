#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2198 and records what was true when E-2198
# landed. Edit it only if you ARE E-2198. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2198 verification — "Derive an epic's work-product mark from its children".
#
# WHAT LANDED
#   `session status`'s column between the type letter and the id picked ⊙ from
#   the ROW'S OWN status. An epic's status is derived from its children and its
#   own branch is normally empty, so every epic wore ⊙ forever — E-1991 did with
#   five children landed. For an epic row the data layer now rolls the column up
#   from its descendants:
#
#     ◆      any descendant's worktree (or the epic's own) is unsettled
#     ~      else, any of those verdicts is not yet determined
#     blank  else, at least one non-epic descendant is in taskstatus.Shipped
#     ⊙      else
#
# THE CLAIMS
#   C1  The four acceptance states, on a real schema, through the real
#       annotation pass (internal/monitor).
#   C2  Descendants at any depth, along effective_parent_id — a sub-epic's
#       derived `completed` is not shipped work by itself.
#   C3  Non-epic rows are untouched: same rule, and nothing probed for them.
#   C4  The renderer still only reads fields off the row, and the legend still
#       derives its entry by calling unsettledMark.
#
# ISOLATION
#   Go unit tests against an in-memory database with the real schema, plus greps
#   over the tree. Nothing here reads or writes a real database or config.
#
# Exit 0 all-passed, 1 on any failure, 2 on a setup problem.

source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
[[ -f "${WT}/go.mod" ]] || setup_error "worktree root not found (got ${WT})"
cd "${WT}" || setup_error "cannot cd to ${WT}"

RENDER="internal/sessionstatuscmd/session_status.go"

assert_ok() {
    local desc="$1"; shift
    local out rc
    out="$("$@" 2>&1)"; rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return 0; fi
    report_fail "${desc}" "exit 0" "exit ${rc} | $(printf '%s' "${out}" | tail -15)"
    return 1
}

# ── A. fail-fast ────────────────────────────────────────────────────────────

section "A — the tree builds and this task's tests pass (FAIL-FAST)"

assert_ok "go build ./..." go build ./... || summary
assert_ok "go test ./internal/monitor -run EpicWorkProduct (C1, C2, C3)" \
    go test ./internal/monitor/ -count=1 -run 'EpicWorkProduct' || summary
assert_ok "go test ./internal/sessionstatuscmd -run UnsettledMark|Legend (C4)" \
    go test ./internal/sessionstatuscmd/ -count=1 -run 'UnsettledMark|Legend' || summary

# ── B. the rule, structurally ───────────────────────────────────────────────

section "B — where the decision lives"

MARK_BODY="$(awk '/^func unsettledMark\(/{f=1} f{print} f && /^}$/{exit}' "${RENDER}")"
[[ -n "${MARK_BODY}" ]] || setup_error "could not extract unsettledMark from ${RENDER}"

assert_contains "C4: the ⊙/blank split asks the row, which knows it is an epic" \
    "r.HasShippedWork()" "${MARK_BODY}"
assert_not_contains "C4: ...and no longer reads the row's own status directly" \
    "taskstatus.Has(taskstatus.Shipped, r.Status)" "${MARK_BODY}"
assert_not_contains "C4: the renderer runs no query" "DB()" "${MARK_BODY}"
assert_contains "C4: the legend still derives its entry by calling unsettledMark" \
    "switch unsettledMark(r) {" "$(cat "${RENDER}")"
assert_contains "C2: descendants are walked along effective_parent_id" \
    "effective_parent_id" "$(cat internal/monitor/reap_worktrees.go)"
assert_contains "the guide documents the epic rule" \
    "An **epic** row is the exception" "$(cat docs/guide/orchestration.md)"

# ── C. regression ───────────────────────────────────────────────────────────

section "C — Go regression"

assert_ok "go vet ./..." go vet ./...
assert_ok "go test ./... (the whole Go suite)" go test ./... -count=1

summary
