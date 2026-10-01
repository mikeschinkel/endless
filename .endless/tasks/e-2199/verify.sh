#!/usr/bin/env bash
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2199 and records what was true when E-2199
# landed. Edit it only if you ARE E-2199. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2199 verification — "Stop session status marking landed tasks as not started".
#
# WHAT LANDED
#   `session status` picked ⊙ ("not started") vs blank from the row's own
#   status, so a task that landed mid-flight and kept working (still
#   `underway`) wore ⊙ despite real landed code. The ⊙/blank split now reads
#   evidence that depends on the task type (monitor.HasWorkProduct):
#
#     epic                  descendants' statuses     (E-2198, unchanged)
#     research, brainstorm  its own status (Shipped)  (unchanged)
#     todo, bugfix, other   a task_landings row       (E-2199)
#
# THE CLAIMS
#   C1  A landed `underway` todo/bugfix renders blank, not ⊙.
#   C2  An unlanded todo renders ⊙ whatever its status; ◆ and ~ keep precedence.
#   C3  Completed/unreviewed research and brainstorm stay blank without a landing.
#   C4  E-2198's epic rule is preserved; the legend still derives from
#       unsettledMark; the renderer still runs no query.
#
# ISOLATION
#   Go unit tests and greps over the tree. Nothing reads or writes a real
#   database or config.
#
# Exit 0 all-passed, 1 on any failure, 2 on a setup problem.

set -u

WT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
[[ -f "${WT}/go.mod" ]] || setup_error "worktree root not found (got ${WT})"
cd "${WT}" || setup_error "cannot cd to ${WT}"

RENDER="internal/sessionstatuscmd/session_status.go"
ROW="internal/monitor/session_status.go"

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
assert_ok "go test ./internal/sessionstatuscmd -run UnsettledMark|Legend|FourState (C1–C4)" \
    go test ./internal/sessionstatuscmd/ -count=1 -run 'UnsettledMark|Legend|FourState|ColumnsAndTruncation' || summary
assert_ok "go test ./internal/monitor -run EpicWorkProduct (C4)" \
    go test ./internal/monitor/ -count=1 -run 'EpicWorkProduct' || summary

# ── B. the rule, structurally ───────────────────────────────────────────────

section "B — where the decision lives"

HWP_BODY="$(awk '/^func \(r SessionStatusRow\) HasWorkProduct\(/{f=1} f{print} f && /^}$/{exit}' "${ROW}")"
[[ -n "${HWP_BODY}" ]] || setup_error "could not extract HasWorkProduct from ${ROW}"
MARK_BODY="$(awk '/^func unsettledMark\(/{f=1} f{print} f && /^}$/{exit}' "${RENDER}")"
[[ -n "${MARK_BODY}" ]] || setup_error "could not extract unsettledMark from ${RENDER}"

assert_contains "C1: code-bearing rows read their landing" "return r.Landed" "${HWP_BODY}"
assert_contains "C3: findings rows read their status" \
    "taskstatus.Has(taskstatus.Shipped, r.Status)" "${HWP_BODY}"
assert_contains "C4: epic rows still read their descendants" "return r.DescendantShipped" "${HWP_BODY}"
assert_contains "C4: unsettledMark asks the row" "r.HasWorkProduct()" "${MARK_BODY}"
assert_not_contains "C4: the renderer runs no query" "DB()" "${MARK_BODY}"
assert_contains "C4: the legend reads column 4 through columnFourMark" \
    "switch columnFourMark(r) {" "$(cat "${RENDER}")"
assert_contains "C4: ...which falls through to unsettledMark" \
    "return unsettledMark(r)" \
    "$(awk '/^func columnFourMark\(/{f=1} f{print} f && /^}$/{exit}' "${RENDER}")"
assert_contains "the pre-May consequence is documented where the rule is written" \
    "E-1715" "$(cat "${RENDER}")"
assert_not_contains "no stale callers of the old name" "HasShippedWork" \
    "$(grep -rn HasShippedWork internal cmd 2>/dev/null)"
assert_contains "the guide documents the per-type rule" \
    "has produced work once it has landed at least once" "$(cat docs/guide/orchestration.md)"

# ── C. regression ───────────────────────────────────────────────────────────

section "C — Go regression"

assert_ok "go vet ./..." go vet ./...
assert_ok "go test ./... (the whole Go suite)" go test ./... -count=1

summary
