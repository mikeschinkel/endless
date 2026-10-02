#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1994 and records what was true when E-1994
# landed. Edit it only if you ARE E-1994. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1994 land gate — start task sessions early so they read in and wait.
#
#   1. Fail-fast: the task's own unit tests, Go and Python, against this
#      worktree's source.
#   2. The candidate endless-go, built into a temp dir, asserted at its seams:
#      the `primed` vocabulary, `session-prime`'s refusal outside a Claude
#      session, and the primed handoff clause.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

ROOT="$(cd "${ENDLESS_VERIFY_DIR}/../../.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

section "Unit tests (fail-fast)"
if (cd "${ROOT}" && go test ./internal/sessionstate/ ./internal/sessionstatecmd/ \
        ./internal/plancite/ ./internal/sessionprimecmd/ ./internal/autospawnjob/ \
        ./internal/projectstatuscmd/ ./internal/schema/ >"${TMP}/go1.log" 2>&1); then
    report_pass "go: vocabulary, drift check, session-prime, prime job, project status, schema"
else
    report_fail "go unit tests" "pass" "$(tail -30 "${TMP}/go1.log")"; summary; exit 1
fi
if (cd "${ROOT}" && go test ./internal/monitor/ \
        -run 'Prime|IdleSession|ResumeFromPrimed|NoticeTrigger' >"${TMP}/go2.log" 2>&1 \
     && go test ./internal/hookcmd/ -run RenderPrimedResume >>"${TMP}/go2.log" 2>&1); then
    report_pass "go: primed writers, Stop keeps primed, notices follow the plan, resume note"
else
    report_fail "go monitor/hookcmd tests" "pass" "$(tail -30 "${TMP}/go2.log")"; summary; exit 1
fi
if (cd "${ROOT}" && uv run pytest tests/test_prime.py -q >"${TMP}/py.log" 2>&1); then
    report_pass "python: framing gate, prime route, challenge, handoff clause, task prime, claim"
else
    report_fail "pytest tests/test_prime.py" "pass" "$(tail -30 "${TMP}/py.log")"; summary; exit 1
fi

section "Candidate endless-go"
if ! (cd "${ROOT}" && go build -o "${TMP}/endless-go" ./cmd/endless-go 2>"${TMP}/build.log"); then
    setup_error "go build failed: $(cat "${TMP}/build.log")"
fi
GO="${TMP}/endless-go"

assert_eq "primed is a session state with its own glyph" "◇" "$("${GO}" session-state glyph primed)"
assert_contains "primed is live" "primed" "$("${GO}" session-state get live)"
assert_not_contains "primed may not write" "primed" "$("${GO}" session-state get may-write)"

out="$(env -u CLAUDE_CODE_SESSION_ID "${GO}" session-prime 2>&1)"; code=$?
assert_eq "session-prime refuses outside a Claude session" "1" "${code}"
assert_contains "and names why" "CLAUDE_CODE_SESSION_ID" "${out}"

vars='{"spawned_id":4242,"task_type":"todo","drafting":true,"framing_suffices":false}'
# Rendered from the worktree: a self-dev project renders the embedded template.
clause="$(cd "${ROOT}" && printf '%s' "${vars}" | "${GO}" template render handoff/prime 2>&1)"
assert_contains "the prime clause says to wait" "Read in, then wait" "${clause}"
assert_contains "it ends the read-in with session primed" "endless session primed" "${clause}"
assert_contains "a planless task is drafted" "has no plan. Draft one" "${clause}"
assert_contains "questions go to the task" "endless question ask E-4242" "${clause}"

summary
