#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2233 and records what was true when E-2233
# landed. Edit it only if you ARE E-2233. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2233: the main-sync job keeps an opted-in project's main in step with its
# upstream (fetch, fast-forward only, push, never pull, never force), records a
# fault on divergence, and reports a main rewritten under open task branches —
# in its fault and in `endless worktree check`.
#
# Every check runs against throwaway git repositories (a bare origin plus
# clones) that the Go tests build under their own temp dirs with the
# developer's git config shut out, so nothing here touches a real remote.
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
run_go "baserewrite, landgate, config, faults and the job build and pass" \
    ./internal/baserewrite/ ./internal/landgate/ ./internal/config/ ./internal/faults/ ./internal/mainsyncjob/
[[ "${FAIL_COUNT}" -eq 0 ]] || summary

section "Plan 1: fetch, fast-forward, push, diverge — against a real bare origin"
run_go "only origin moved: main fast-forwards, working tree follows" \
    ./internal/mainsyncjob/ -run '^TestSync_FastForwardsWhenOnlyOriginMoved$'
run_go "only main moved: main is pushed, a second run is in sync" \
    ./internal/mainsyncjob/ -run '^TestSync_PushesWhenOnlyMainMoved$'
run_go "both moved: nothing changes, ERR-0030 names both counts and both ways out" \
    ./internal/mainsyncjob/ -run '^TestSync_DivergedChangesNothingAndRecordsAFault$'
run_go "a rejected push is the run's error (backed off), never forced" \
    ./internal/mainsyncjob/ -run '^TestSync_FailedPushIsTheRunsError$'
run_go "an unreachable remote fails the fetch and nothing acts on stale state" \
    ./internal/mainsyncjob/ -run '^TestSync_FailedFetchIsTheRunsError$'
run_go "main checkout on another branch: refused before the fetch" \
    ./internal/mainsyncjob/ -run '^TestSync_RefusesWhenMainCheckoutIsNotOnMain$'
run_go "pull.rebase / pull.ff have no effect, and git pull never runs" \
    ./internal/mainsyncjob/ -run '^TestSync_UserPullConfigHasNoEffect$'

section "Plan 2: opt-in config"
run_go "a project without main_sync.enabled gets no git command at all" \
    ./internal/mainsyncjob/ -run '^TestRunProjects_DisabledDoesNothing$'
run_go "main_sync.enabled is never inherited from the user's config" \
    ./internal/config/ -run '^TestMerge_MainSyncEnabledIsNeverInherited$'

section "Plan 3: a rewritten main is reported before a land fails"
run_go "the job names the stranded branch (not a landed one) until it is rebased" \
    ./internal/mainsyncjob/ -run '^TestSync_RewrittenMainNamesStrandedBranches$'
run_go "main moving forward skips the expensive rewrite check" \
    ./internal/mainsyncjob/ -run '^TestSync_LinearMainSkipsTheRewriteCheck$'
run_go "worktree check (real endless-go) reports base-rewritten, and stops after a rebase" \
    ./internal/monitor/ -run '^TestWorktreeCheckScripts$/^base-rewritten$'
run_go "the land gate still refuses on the shared detector (E-2232's suite)" \
    ./internal/landgate/

section "Wiring"
main_go="$(cat cmd/endless-go/main.go)"
assert_contains "the job is registered in the endless-go binary" \
    '_ "github.com/mikeschinkel/endless/internal/mainsyncjob"' "${main_go}"
assert_not_contains "the job never invokes git pull" \
    '"pull"' "$(cat internal/mainsyncjob/mainsyncjob.go)"

summary
