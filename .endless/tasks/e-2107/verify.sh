#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2107 and records what was true when E-2107
# landed. Edit it only if you ARE E-2107. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2107 verification — "Distinguish a never-started task from a
# started-and-settled one in session status".
#
# WHAT LANDED
#   `session status` renders one column between the task-type letter and the
#   task id. It had two states: ◆ when the row's worktree was unsettled, blank
#   otherwise. Blank collapsed two opposite facts — "there is no work product
#   here yet" and "there is work product and all of it landed".
#
#   It now has three, and together they answer "is there work product here, and
#   where is it?":
#
#     ◆      work product, still outstanding      (any unsettled worktree)
#     ⊙      no work product yet                  (never spawned, or claimed and empty)
#     blank  work product, all of it landed       (unverified/confirmed + settled)
#
# THE CLAIMS (this suite checks these, not the plumbing)
#   C1  THREE STATES, NOT TWO. `underway` + settled and `unverified` + settled
#       differ only in status, and now render differently. That collapse is the
#       whole defect.
#   C2  ◆ STILL WINS. Unsettled takes precedence over the ⊙/blank split, for
#       every status, exactly as it did before (E-1701).
#   C3  ALIGNMENT HOLDS. All three states measure ONE terminal column, so the
#       fixed 13-col prefix and the E-1765 guarantee survive. This is the
#       assertion that protects every other row in the table, not just marked
#       ones — a double-width glyph here shifts the id, phase and title.
#   C4  ⊙ DOES NOT UN-DIM. ◆ vetoes dim because it means "still something to do
#       here" (E-1707). ⊙ means the opposite, so it must NOT inherit the veto —
#       and it is the COMMON state, so getting this wrong un-dims most of the
#       table at once.
#   C5  STATUS IS THE DISCRIMINATOR, at no new cost. The rule reads
#       taskstatus.Shipped, which is already on the row. It adds no git probe
#       and no DB read to a per-row hot path, and it does not consult
#       task_landings — E-2087 measured branches whose content is demonstrably
#       on main with no landing row at all. Structural half: internal/monitor
#       and the --json surface are UNTOUCHED by this task.
#   C6  THE LEGEND CANNOT DISAGREE WITH THE COLUMN. buildLegend derives the
#       decoration by CALLING unsettledMark rather than re-deriving the rule,
#       and labels ⊙ by what it MEANS ("not started"), not by the status test
#       it happens to come from — so the derivation can be sharpened later
#       without the vocabulary changing.
#
# ISOLATION
#   Every section is a build, a Go unit test, a grep over the tree, or a `just`
#   doc gate. Nothing here reads or writes a database, a ledger or a config —
#   the change is renderer-only and so is its proof.
#
# Layers:
#   A. FAIL-FAST — the tree builds and the changed package's suite is green.
#      Nothing below is worth reading if these fail.
#   B. The three states and the rule that picks between them — C1, C2, C5.
#   C. Alignment and dim — C3, C4.
#   D. The legend — C6.
#   E. The blast radius — C5's structural half, and the glyph's uniqueness.
#   F. The documented vocabulary matches the shipped one.
#   G. Project-wide regression.
#
# Output: pass/fail per check, then a summary. Exit 0 all-passed, 1 on any
# failure, 2 on a setup problem.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT=""   # worktree root
PKG="internal/sessionstatuscmd"

# ── setup ───────────────────────────────────────────────────────────────────

WT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../.." && pwd -P)"
[[ -f "${WT}/go.mod" && -d "${WT}/${PKG}" ]] \
    || setup_error "worktree root not found from ${BASH_SOURCE[0]} (got ${WT})"
cd "${WT}" || setup_error "cannot cd to ${WT}"

SRC="${WT}/${PKG}/session_status.go"
[[ -f "${SRC}" ]] || setup_error "renderer source not found at ${SRC}"

# assert_ok DESC CMD... — the command exits 0.
assert_ok() {
    local desc="$1"; shift
    local out rc
    out="$("$@" 2>&1)"; rc=$?
    if [[ "${rc}" -eq 0 ]]; then report_pass "${desc}"; return 0; fi
    report_fail "${desc}" "exit 0" "exit ${rc} | $(printf '%s' "${out}" | tail -15)"
    return 1
}

# gotest DESC TESTNAME — one named test in the changed package, as one assertion.
gotest() {
    assert_ok "$1" go test "./${PKG}/" -count=1 -run "^$2\$"
}

# ── A. fail-fast ────────────────────────────────────────────────────────────

section "A — the tree builds and the changed package is green (FAIL-FAST)"

if ! assert_ok "go build ./... — the whole tree compiles" go build ./...; then
    report_fail "FAIL-FAST" "the tree builds" "it does not; nothing below is meaningful"
    summary
fi
if ! assert_ok "go test ./${PKG} — the renderer's own suite" go test "./${PKG}/" -count=1; then
    report_fail "FAIL-FAST" "the changed package's tests pass" \
        "they do not; nothing below is meaningful"
    summary
fi

# ── B. the three states ─────────────────────────────────────────────────────

section "B — three states, and the rule that picks between them (C1, C2, C5)"

gotest "the column's full truth table, ◆ precedence included" TestUnsettledMark

# C1 stated as the one comparison the task exists to make. Both rows are
# settled; only the status differs. Before E-2107 both rendered blank.
assert_contains "C1: \`underway\` + settled and \`unverified\` + settled are DIFFERENT states" \
    '{"claimed but empty", monitor.SessionStatusRow{Status: "underway"}, "⊙"' \
    "$(cat "${WT}/${PKG}/session_status_test.go")"
assert_contains "C1: ...and blank is now reserved for the shipped-and-landed row" \
    '{"unverified and settled", monitor.SessionStatusRow{Status: "unverified"}, " "' \
    "$(cat "${WT}/${PKG}/session_status_test.go")"

# C5: the discriminator is the status vocabulary, so adding a status forces the
# decision instead of silently defaulting to blank. Asserted against the
# FUNCTION BODY rather than the file — the file's prose explains at length why
# landing history was rejected, and a grep over that would pass on the words
# while a landing probe sat in the code.
MARK_BODY="$(awk '/^func unsettledMark\(/{f=1} f{print} f && /^}$/{exit}' "${SRC}")"
[[ -n "${MARK_BODY}" ]] || setup_error "could not extract unsettledMark's body from ${SRC}"

assert_contains "C5: the ⊙/blank split reads taskstatus.Shipped" \
    "taskstatus.Has(taskstatus.Shipped, r.Status)" "${MARK_BODY}"
assert_not_contains "C5: ...and not landing history, which E-2087 showed is unreliable" \
    "Landed" "${MARK_BODY}"
assert_not_contains "C5: ...and shells out to nothing on this per-row hot path" \
    "exec" "${MARK_BODY}"

# ── C. alignment and dim ────────────────────────────────────────────────────

section "C — the invariants a new glyph could break (C3, C4)"

gotest "C3: every state of the column measures one terminal column" TestUnsettledMarkGlyphWidths
gotest "C3: one frame, all three states, id column unshifted" TestRenderThreeStateColumn
gotest "C3: ◆-vs-blank alignment, unchanged from E-1701" TestRenderUnsettledIndicator
gotest "C4: ⊙ does not inherit ◆'s dim veto" TestNotStartedDoesNotVetoDim

# ── D. the legend ───────────────────────────────────────────────────────────

section "D — the legend follows the column (C6)"

gotest "the legend is conditional, ordered, and names ⊙" TestBuildLegend
assert_contains "C6: buildLegend CALLS unsettledMark rather than re-deriving the rule" \
    "switch unsettledMark(r) {" \
    "$(cat "${SRC}")"
assert_contains "C6: ⊙ is labelled by meaning, not by its derivation" \
    'notStartedGlyph+" not started"' \
    "$(cat "${SRC}")"
assert_not_contains "C6: ...so no legend label leaks the status test" \
    '" shipped"' \
    "$(cat "${SRC}")"

# ── E. blast radius ─────────────────────────────────────────────────────────

section "E — what this task did NOT touch (C5, structural)"

BASE="$(git merge-base main HEAD 2>/dev/null || true)"
if [[ -z "${BASE}" ]]; then
    report_skip "blast radius" "no merge-base with main (detached or renamed default branch)"
else
    assert_eq "internal/monitor is untouched — no new per-row git or DB probe" \
        "" "$(git diff --name-only "${BASE}" -- internal/monitor)"
    assert_eq "the --json surface is untouched — it already carries status and unsettled" \
        "" "$(git diff --name-only "${BASE}" -- "${PKG}/json.go")"
    # The shipped change, exhaustively: one renderer file, its tests, one guide
    # section. Anything else appearing here means the task grew a second job.
    # LC_ALL=C so the comparison does not depend on the runner's collation.
    assert_eq "the shipped change is the renderer, its tests and the guide — nothing else" \
        "docs/guide/orchestration.md ${PKG}/session_status.go ${PKG}/session_status_test.go" \
        "$(git diff --name-only "${BASE}" -- docs internal cmd src tests \
            | LC_ALL=C sort | tr '\n' ' ' | sed 's/ $//')"
fi

# ⊙ must not already mean something else on another surface: the column shares
# the row with ⊗/⏸/⊘/⊕, and position is the only thing disambiguating them.
assert_eq "⊙ is spelled in exactly one Go file — the renderer" \
    "${PKG}/session_status.go" \
    "$(grep -rl '⊙' internal cmd src --include='*.go' --include='*.py' 2>/dev/null \
        | grep -v '_test\.go' | LC_ALL=C sort | tr '\n' ' ' | sed 's/ $//')"

# ── F. the documented vocabulary ────────────────────────────────────────────

section "F — the guide describes the column that shipped"

GUIDE="$(cat "${WT}/docs/guide/orchestration.md")"
assert_contains "the guide documents ⊙" "**⊙**" "${GUIDE}"
assert_contains "...labelled by meaning" "No work product yet" "${GUIDE}"
assert_contains "...and says blank is now the narrow case" \
    "Work product, and all of it landed" "${GUIDE}"
assert_contains "...and records that ⊙ does not un-dim a row (C4)" \
    "does **not** un-dim a row" "${GUIDE}"

# ── G. project-wide regression ──────────────────────────────────────────────

section "G — project-wide regression"

assert_ok "go vet ./..." go vet ./...
assert_ok "go test ./... (the whole Go suite)" go test ./... -count=1
assert_ok "just test (the whole Python suite)" just test
assert_ok "just guide-check (the guide cross-reference is current)" just guide-check
assert_ok "just lifecycle-check (the status diagram has not drifted)" just lifecycle-check

summary
