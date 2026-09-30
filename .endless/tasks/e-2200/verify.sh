#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2200 and records what was true when E-2200
# landed. Edit it only if you ARE E-2200. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2200 verification — a submitted task can be spawned, and everything that
# describes spawn says so. The plan and its open questions are the whole spawn
# and claim gate; `task approve` records a review and gates nothing.
#
#   endless task verify E-2200
#
# Exit 0 on all-passed, 1 on any failure, 2 on setup error.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not in a git repo"
cd "${WT}" || setup_error "cannot cd to ${WT}"
TMP="$(mktemp -d)" || setup_error "could not create a temp dir"
trap 'rm -rf "${TMP}"' EXIT

# ── 1. fail-fast unit gate ──────────────────────────────────────────────────
section "1. Unit gate (fail fast)"

if go test ./internal/taskstatus/ ./internal/sessionstatuscmd/ >"${TMP}/go.log" 2>&1; then
    report_pass "go test: transition table, ClaimPromotes == claims edges, board glyphs"
else
    report_fail "go test taskstatus / sessionstatuscmd" "exit 0" "$(tail -25 "${TMP}/go.log")"
    summary
fi

if uv run pytest -q \
        tests/test_spawn_gate.py \
        tests/test_task_claim_worktree.py \
        tests/test_status_lifecycle_sync.py \
        tests/test_plan_required_status_model.py \
        >"${TMP}/py.log" 2>&1; then
    report_pass "pytest: claim/spawn take a submitted task; gate text; lifecycle sync"
else
    report_fail "pytest spawn gate / claim / lifecycle" "exit 0" "$(tail -25 "${TMP}/py.log")"
    summary
fi

# ── 2. the lifecycle diagram draws the edge ─────────────────────────────────
section "2. Lifecycle diagram"

# Built from THIS worktree: the check renders from the Go table.
if go build -o bin/endless-go ./cmd/endless-go >"${TMP}/build.log" 2>&1 \
    && uv run python -m endless.lifecycle_map check >"${TMP}/lc.log" 2>&1; then
    report_pass "lifecycle-check: committed diagrams match the Go table"
else
    report_fail "lifecycle-check" "exit 0" "$(tail -15 "${TMP}/build.log" "${TMP}/lc.log")"
fi
for f in docs/status-lifecycle.mmd README.md docs/guide/index.md; do
    assert_contains "${f} draws submitted --> underway" \
        "submitted --> underway: session claims" "$(cat "${f}")"
done

# ── 3. nothing still says approval is required ──────────────────────────────
section "3. Sweep"

for phrase in "may pick up only \`ready\`" "claim gate refuses" "provably means human-approved" \
              "two-step gate that makes \`ready\` mean"; do
    HITS="$(git grep -n -F "${phrase}" -- ':!.endless' || true)"
    assert_eq "no tracked file outside .endless/ says \"${phrase}\"" "" "${HITS}"
done

HELP="$(uv run endless task approve --help 2>&1)"
assert_contains "task approve --help says it does not unlock spawning" \
    "does not unlock spawning" "$(tr -s ' \n' ' ' <<<"${HELP}")"

summary
