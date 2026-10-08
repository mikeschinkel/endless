#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2273 and records what was true when E-2273
# landed. Edit it only if you ARE E-2273. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2273: main-sync never publishes a commit the ledger auto-commit may still
# amend, and pushes the exact tip it inspected rather than the branch by name.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

root="$(git -C "$ENDLESS_VERIFY_DIR" rev-parse --show-toplevel)" || setup_error "not in a git checkout"
cd "$root" || setup_error "cannot cd to $root"

section "This task's own tests (fail fast)"
out="$(go test ./internal/mainsyncjob/ -count=1 \
    -run 'TestSync_AmendableTipStaysLocal|TestSync_PushesTheTipItInspected|TestSync_OnlyAnAmendableTipIsHeldBack' -v 2>&1)"
if [[ $? -ne 0 ]]; then
    report_fail "E-2273 reproduction tests pass" "ok" "$out"
    summary
fi
for t in TestSync_AmendableTipStaysLocal TestSync_PushesTheTipItInspected TestSync_OnlyAnAmendableTipIsHeldBack; do
    assert_contains "$t ran and passed" "--- PASS: $t" "$out"
done

out="$(go test ./internal/events/ -count=1 -run '^TestMayAmend$' -v 2>&1)"
assert_contains "events.MayAmend matches exactly the auto-commit subjects" "--- PASS: TestMayAmend" "$out"

section "main-sync's whole package still passes"
out="$(go test ./internal/mainsyncjob/ -count=1 2>&1)"
assert_contains "go test ./internal/mainsyncjob/" "ok  	github.com/mikeschinkel/endless/internal/mainsyncjob" "$out"

summary
