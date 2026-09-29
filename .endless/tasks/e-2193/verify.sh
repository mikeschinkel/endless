#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2193 and records what was true when E-2193
# landed. Edit it only if you ARE E-2193. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2193: restart monitors in place when their binary is replaced.
#
# What is verified here:
#   A. Fail-fast: this task's own Go tests, and each contract test by name so
#      deleting one cannot turn the section green by absence. The end-to-end
#      one runs a real liveview.Loop in a real process started through a
#      symlink, repoints the symlink, and asserts the SAME PID comes back on the
#      new build with its original argv — then repoints it at a build whose
#      probe fails and asserts the process stays put and says so.
#   B. The fault: WARN-0019 is in the catalog the binary built from this tree
#      prints, and documented in docs/errors.md.
#   C. The rename: nothing in the watch spells the binary's name, so it
#      survives E-1063 renaming endless-go to endless.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)" || setup_error "not inside a git worktree"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------

if out=$(go test -count=1 ./internal/liveview/ ./internal/faults/ 2>&1); then
    report_pass "go test: liveview, faults"
else
    report_fail "go test: liveview, faults" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

verbose=$(go test -count=1 -v -run 'TestRestarter_|TestLoopReexecsIntoReplacedBinary' ./internal/liveview/ 2>&1)
for t in \
    TestRestarter_IdentityRule/unchanged_never_restarts \
    TestRestarter_IdentityRule/changed_once_is_not_yet_a_replacement \
    TestRestarter_IdentityRule/still_changing_is_not_yet_a_replacement \
    TestRestarter_IdentityRule/changed_and_stable_restarts \
    TestRestarter_IdentityRule/symlink_repointed_with_the_old_target_untouched \
    TestRestarter_IdentityRule/a_missing_path_mid-install_is_neither_change_nor_failure \
    TestRestarter_SuccessfulRestart \
    TestRestarter_JobInFlightDefersTheExec \
    TestRestarter_FailedProbe \
    TestRestarter_FailedExec \
    TestRestarter_NilIsInert \
    TestLoopReexecsIntoReplacedBinary
do
    assert_contains "${t} passed" "--- PASS: ${t} " "${verbose}"
done

# ---------------------------------------------------------------------------
section "B. WARN-0019 is catalogued and documented"
# ---------------------------------------------------------------------------

go build -o "${TMP}/endless-go" ./cmd/endless-go || setup_error "cannot build endless-go from this tree"
codes=$("${TMP}/endless-go" errors codes 2>&1)
assert_contains "errors codes lists WARN-0019" "WARN-0019" "${codes}"
assert_contains "WARN-0019 is monitor-restart-failed" "monitor-restart-failed" "${codes}"
assert_contains "docs/errors.md has the section" "## WARN-0019 — monitor-restart-failed" "$(cat docs/errors.md)"

# ---------------------------------------------------------------------------
section "C. Nothing names the binary"
# ---------------------------------------------------------------------------

# Code, not comments: a string literal naming the binary is what would break
# on the rename.
literals=$(grep -n '"[^"]*endless-go' internal/liveview/restart.go internal/liveview/liveview.go || true)
assert_eq "no string literal in the watch names endless-go" "" "${literals}"

summary
