#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2260 and records what was true when E-2260
# landed. Edit it only if you ARE E-2260. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2260: a background-job failure the job marks transient (main-sync's DNS
# failure, refused connection, unreachable network, timeout) records nothing
# until it repeats TransientThreshold times in a row, then WARN-0032, which
# clears itself on the next successful run. Every other failure is still
# WARN-0001 at once.
#
# The runner checks use an in-memory database; the main-sync checks run real
# git against throwaway repositories with the fetch's stderr faked.
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
run_go "jobs, mainsyncjob and faults (catalog/docs sync) build and pass" \
    ./internal/jobs/ ./internal/mainsyncjob/ ./internal/faults/
[[ "${FAIL_COUNT}" -eq 0 ]] || summary

section "Plan 1: the marker keeps the cause reachable"
run_go "Transient survives errors.Is/As and wraps; an unmarked error and nil are not transient" \
    ./internal/jobs/ -run '^TestTransient_KeepsTheCauseReachable$'

section "Plan 2: main-sync marks only network failures"
run_go "each listed stderr phrase is transient; a rejected push and auth failures are not" \
    ./internal/mainsyncjob/ -run '^TestUnreachable_MarksOnlyNetworkFailures$'
run_go "a run is transient only when every project's failure is" \
    ./internal/mainsyncjob/ -run '^TestRunProjects_TransientOnlyWhenEveryFailureIs$'

section "Plan 3 + 4: the threshold"
run_go "below the threshold: no incident, fail_count counts, backoff escalates" \
    ./internal/jobs/ -run '^TestTransient_BelowThresholdRecordsNothingButBacksOff$'
run_go "at the threshold: one WARN-0032 naming the job, the count and the cause; no WARN-0001" \
    ./internal/jobs/ -run '^TestTransient_ThresholdRecordsUnreachableNotJobFailed$'
run_go "a non-transient failure is WARN-0001 on its first run, after transient ones too" \
    ./internal/jobs/ -run '^TestTransient_NonTransientReportsAtOnce$'
run_go "WARN-0032 is in the catalog, documented, and its number recorded as spent" \
    ./internal/faults/ -run '^TestCatalog_'

section "Plan 5: the warning clears itself"
run_go "a successful run clears the open WARN-0032" \
    ./internal/jobs/ -run '^TestTransient_SuccessClearsTheWarning$'
run_go "a failed clear is not the run's error" \
    ./internal/jobs/ -run '^TestTransient_FailedClearIsNotTheRunsError$'

section "Plan 4: the code is listed by the CLI"
out="$(go run ./cmd/endless-go errors codes 2>&1)"
assert_contains "endless errors codes lists WARN-0032" \
    "WARN-0032  warning   job-unreachable             A background job could not reach the network" "${out}"

summary
