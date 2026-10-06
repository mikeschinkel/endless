#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2259 and records what was true when E-2259
# landed. Edit it only if you ARE E-2259. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2259: main-sync's WARN-0031 explains the divergence it reports — when each
# branch diverged, whether the rewrite predates the job's previous run, whose
# task each branch is and whether that task is finished — points at --detail
# when the summary truncates, and clears itself once nothing is stranded.
#
# The job checks run against throwaway git repositories the Go tests build
# under their own temp dirs; the database reads are stubbed there, and the two
# new readers are tested against an in-memory database.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || setup_error "not inside the worktree"
cd "${ROOT}" || setup_error "cannot cd to ${ROOT}"

# run_go <description> <go test args...> — one assertion per go test run.
run_go() {
    local desc="$1"; shift
    local out
    if out="$(go test -count=1 "$@" 2>&1)"; then
        report_pass "${desc}"
    else
        report_fail "${desc}" "go test passes" "$(printf '%s' "${out}" | tail -15)"
    fi
}

section "Fail-fast: the task's own unit tests"
run_go "mainsyncjob, faults (docs/remedy sync), jobs and monitor build and pass" \
    ./internal/mainsyncjob/ ./internal/faults/ ./internal/jobs/ ./internal/monitor/
[[ "${FAIL_COUNT}" -eq 0 ]] || summary

section "Plan 1: a truncated summary says where the rest is"
run_go "past four names: 'and N more — endless errors show <id> --detail'; at four, no hint" \
    ./internal/mainsyncjob/ -run '^TestRewritten_TruncatedSummaryPointsAtDetail$'

section "Plan 2: when each branch diverged, and whether that is news"
run_go "first-ever run, and a rewrite older than the previous run, are labelled not new; a newer one is not" \
    ./internal/mainsyncjob/ -run '^TestRewritten_AgeNote$'
run_go "the job's previous successful run is read from its scheduling row" \
    ./internal/jobs/ -run '^TestLastOkAt_ReadsThePreviousSuccessfulRun$'

section "Plan 3 + 4: whose branch it is; finished tasks grouped apart"
run_go "status and live/ended claiming session per branch; finished task's branch in its own group, still named" \
    ./internal/mainsyncjob/ -run '^TestRewritten_FinishedTasksAreGroupedApart$'
run_go "a task's status and newest bound session read in one query" \
    ./internal/monitor/ -run '^TestGetTaskClaim$'
run_go "failing task reads leave their line out and never fail the run" \
    ./internal/mainsyncjob/ -run '^TestRewritten_FailedReadsDoNotFailTheRun$'

section "Plan 5: the incident resolves itself"
run_go "fewer stranded re-records naming only those; none clears it; a failed clear is retried" \
    ./internal/mainsyncjob/ -run '^TestRewritten_ResolvesItself$'
run_go "E-2233's own rewrite behaviour still holds" \
    ./internal/mainsyncjob/ -run '^TestSync_(RewrittenMainNamesStrandedBranches|LinearMainSkipsTheRewriteCheck)$'

section "Docs"
doc="$(sed -n '/^## WARN-0031/,/^## /p' docs/errors.md)"
assert_contains "docs say the warning clears itself" "once none are left it clears itself" "${doc}"
assert_not_contains "docs no longer tell anyone to clear it" "Dismiss with" "${doc}"

summary
