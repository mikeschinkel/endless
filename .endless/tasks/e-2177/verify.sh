#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2177 and records what was true when E-2177
# landed. Edit it only if you ARE E-2177. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2177 verification — the Claude hook tells a command that RUNS from one that
# is only MENTIONED.
#
# THE CLAIMS
#   C1  The hook infers no state from Bash command text: a heredoc naming a
#       claim binds nothing, a `--unattended` claim binds nothing, and a commit
#       message naming a confirm confirms nothing. (internal/hookcmd
#       mention_not_run_test.go, driven through the real PostToolUse handler.)
#   C2  `task claim` does the whole job without the hook: the CLI + executor
#       bind the session and promote the task, the session is `working` after
#       its next hook event, and the claim prints its own handoff — to an agent
#       only, never for `--unattended`. (tests/test_claim_binds_without_the_hook.py
#       and internal/claimhandoffcmd.)
#   C3  Text-matched gates ignore quoted, commit-message and heredoc mentions,
#       and still catch real invocations. (mention_gates_test.go)
#   C4  The commit-on-main gate judges the directory the commit runs in.
#       (commit_dir_test.go)
#   C5  Nothing is left of the hook's state writes or the Go matchers package.
#
# Trigger strings are assembled at runtime in the tests this folds in, and none
# appear literally here: the gates under test fire on the command that writes
# this file.
#
# ISOLATION: unit tests (temp DBs, temp git repos), pytest under its own
# isolated config, and greps. Nothing reads or writes a real database.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

ROOT=$(git rev-parse --show-toplevel 2>/dev/null) || setup_error "not in a git tree"
cd "${ROOT}" || setup_error "cannot cd to ${ROOT}"

section "A. Fail-fast: the touched packages' unit tests"
if out=$(go build -o bin/endless-go ./cmd/endless-go 2>&1); then
    report_pass "go build endless-go"
else
    report_fail "go build endless-go" "${out}"
    summary
fi
if out=$(go test -count=1 ./internal/hookcmd/ ./internal/claimhandoffcmd/ \
        ./internal/monitor/ ./internal/sessionstate/ 2>&1); then
    report_pass "go test hookcmd, claimhandoffcmd, monitor, sessionstate"
else
    report_fail "go test hookcmd, claimhandoffcmd, monitor, sessionstate" "${out}"
    summary
fi
if out=$(uv run --quiet pytest -q tests/test_claim_binds_without_the_hook.py \
        tests/test_claim_output_format.py tests/test_refusal_inventory_anchors.py 2>&1); then
    report_pass "pytest claim-without-hook, claim output, refusal inventory anchors"
else
    report_fail "pytest claim-without-hook, claim output, refusal inventory anchors" "${out}"
    summary
fi

# Each claim's own tests, run by name so a claim with no test fails here
# ("no tests to run" is not a pass).
run_named() {
    local desc="$1" pkg="$2" pattern="$3" out
    out=$(go test -count=1 -v -run "${pattern}" "${pkg}" 2>&1)
    if [[ $? -eq 0 ]] && grep -q -- '^--- PASS' <<<"${out}"; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "${out}"
    fi
}

section "B. C1 — a mention changes no state"
run_named "heredoc claim binds nothing; --unattended binds nothing; commit-message confirm confirms nothing" \
    ./internal/hookcmd/ '^TestPostToolUse_'

section "C. C2 — the claim does the whole job"
out=$(uv run --quiet pytest -q tests/test_claim_binds_without_the_hook.py 2>&1); rc=$?
assert_eq "bind + underway + working after next hook, and the handoff rules" "0" "${rc}"
run_named "the claim handoff renders from the claim's own process" \
    ./internal/claimhandoffcmd/ '^(TestRender_|TestRun_|TestClaimHandoff_)'
out=$(./bin/endless-go claim-handoff 2>&1); rc=$?
assert_contains "endless-go claim-handoff is a registered subcommand" "usage: endless-go claim-handoff" "${out}"

section "D. C3 — gates ignore mentions, catch invocations"
run_named "mention-vs-invocation table for every text-matched gate" \
    ./internal/hookcmd/ '^(TestTextGates_|TestStripHeredocs)$'

section "E. C4 — the commit-on-main gate judges the commit's directory"
run_named "cd/-C directory table against a real main + worktree" \
    ./internal/hookcmd/ '^(TestCommitRunsOnMain_|TestCommitDir)'

section "F. C5 — no residue"
residue=$(grep -rn -e 'StartWorkSession' -e 'CompleteTask(' -e 'ActionRegex' \
    -e 'internal/matchers' -e 'handlePostToolUseSession' internal/ cmd/ 2>/dev/null)
assert_eq "no hook state writes or Go matchers remain" "" "${residue}"
assert_eq "internal/matchers is deleted" "absent" "$([[ -e internal/matchers ]] && echo present || echo absent)"

summary
