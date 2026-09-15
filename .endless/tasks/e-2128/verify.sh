#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2128 and records what was true when E-2128
# landed. Edit it only if you ARE E-2128. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2128 / ED-1589: the exact unlanded verdict stops being computed by its
# consumers and becomes derived state owned by one background job.
#
# "Does this branch hold work the base branch lacks?" is answered exactly by
# `git range-diff`, at a measured 584ms per worktree. Every consumer recomputed
# it on every tick: the reaper on five Claude hook branches including PreToolUse
# and PostToolUse, and every `session monitor` pane on every rendered row every
# two seconds. So half a second became a continuous multi-core load that
# multiplied with each pane opened — and on the tool-call path the same probe had
# already made a sweep cost ninety seconds, which did not degrade the product so
# much as stop it.
#
# The fix is one change, not two, because the reaper's sweep cost and the
# monitor's fan-out were the same defect: the verdict was recomputed from scratch
# by every consumer, with no shared derived state.
#
# What is verified here:
#   A. Fail-fast: this task's own tests pass — internal/monitor (the cache, the
#      pool, the probe, the reaper), internal/unlandedjob (the schedule),
#      internal/sessionstatuscmd (the fourth glyph and the legend),
#      internal/faults (the new code is documented), and the Go/Python
#      default-branch parity suite, whose contract this change had to keep.
#   B. The claims only visible from inside, named one by one.
#   C. The cache is real, and every worktree of a repo shares exactly one —
#      through the real binary, against a real repository and a real linked
#      worktree.
#   D. There is no file format: the only file with contents holds the lines a
#      consumer needs, and "fully landed" is an EMPTY file whose presence is the
#      verdict — which is also what lets a land show settled immediately, since a
#      settled marker is keyed on the branch tip alone and never goes through the
#      watermark's base tip.
#   E. The invariant the whole design rests on: a settled verdict survives the
#      base branch advancing, and a non-zero one does not.
#   F. A branch amend self-invalidates, by path, with no logic and no stored
#      field.
#   G. The reaper is off the hook path entirely, and the leak that cost closes.
#   H. Where the decisions live: one cancellable runGit with no context-free
#      door, a display path that cannot compute, and `~` tested first.
#
# See E-2128's plan (endless task show E-2128 --all-fields).
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# internal/monitor holds the cache, the validity protocol, the bounded pool, the
# split probe and the reaper's rewired condition 4. internal/unlandedjob is the
# schedule and the registration without which the cache is never written.
# internal/sessionstatuscmd is the fourth glyph and the legend it has to stay
# unique within. internal/faults is the new ERR-0012 and its documentation. The
# parity suite is here rather than as a courtesy: DefaultBranch changed shape and
# its four-step order is mirrored case for case on the Python side.

go_pkg() { # go_pkg <package> <label>
    if out=$(go test "$1" 2>&1); then
        report_pass "go test: $2"
    else
        report_fail "go test: $2" "exit 0" "$(printf '%s' "${out}" | tail -30)"
        summary
    fi
}

go_pkg ./internal/monitor/ "internal/monitor"
go_pkg ./internal/unlandedjob/ "internal/unlandedjob"
go_pkg ./internal/sessionstatuscmd/ "internal/sessionstatuscmd"
go_pkg ./internal/faults/ "internal/faults"

if out=$(uv run pytest -q tests/test_default_branch_parity.py 2>&1); then
    report_pass "pytest: the Go/Python resolver parity suite"
else
    report_fail "pytest: the Go/Python resolver parity suite" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# ---------------------------------------------------------------------------
section "B. The claims only visible from inside, named"
# ---------------------------------------------------------------------------
# Section A already ran these. They are re-run by name so this report states each
# claim rather than collapsing all of them into two green packages. Every cache
# test builds a REAL repository: each rule here is a claim about what git does to
# commits, and a stubbed one would only assert that the author and the
# implementation share a belief about that.

go_claim() { # go_claim <package> <test-name> <claim>
    if out=$(go test "$1" -run "^$2\$" -count=1 2>&1); then
        report_pass "$3"
    else
        report_fail "$3" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    fi
}

mon() { go_claim ./internal/monitor/ "$1" "$2"; }

mon TestDisplayPathNeverRunsTheComparison \
    "the display path reads the cache and runs no part of the comparison"
mon TestVerdictPathReadsTheCacheAndNeverComputes \
    "a cold cache answers 'not yet determined', never a derived verdict"
mon TestSettledVerdictSurvivesBaseAppend \
    "a settled verdict survives the base advancing, and is not recomputed"
mon TestUnsettledVerdictInvalidatesOnBaseAppend \
    "a non-zero verdict does not — base movement can shrink it"
mon TestBaseHistoryRewriteFlushesEverySettledEntry \
    "an amended base is detected and flushes every settled marker"
mon TestBranchAmendInvalidatesOnlyItsOwnEntry \
    "a branch amend invalidates that one entry and nothing else"
mon TestChangingTheConfiguredBaseNameInvalidatesEverything \
    "renaming the project's default_branch invalidates the whole cache"
mon TestMissingWatermarkIsACompleteMiss \
    "with no watermark every lookup misses — fail-closed by construction"
mon TestAmbiguousWatermarkIsACompleteMiss \
    "an ambiguous watermark is refused, not resolved by a rule"
mon TestCachedCountIsExactBeyondTheDisplayCap \
    "the cached count stays exact past the display cap"
mon TestTruncatedUnsettledEntryReadsAsAMiss \
    "a truncated entry reads as a miss, never as the all-clear"
mon TestPruneDropsMarkersForVanishedWorktrees \
    "markers for branch tips nothing points at are pruned"
mon TestBaseNameEscapingRoundTrips \
    "a base branch with a slash is one path component, reversibly"
mon TestUnusableCacheDirIsAPermanentMissAndOneFault \
    "a read-only cache is a permanent miss and exactly ONE incident"
mon TestPoolBoundsConcurrencyOverAColdBatch \
    "a cold batch completes in full without exceeding the pool cap"
mon TestCancelledProbeIsInterruptedAndRecordsNoFault \
    "a cancelled lease is interrupted, not OOM-killed, and records no fault"
mon TestCancellingAPassStopsItWithoutFaults \
    "and cancelling a whole pass leaves no incident behind"
mon TestMaybeReapWorktree_UnrecordedRebaseLandingIsReaped \
    "the reaper reclaims a rebase-landed worktree with no landing row"
mon TestMaybeReapWorktree_ComputesConditionFourOnACacheMiss \
    "and computes on a miss — a delete never acts on 'not yet determined'"
mon TestMasterProjectMarksUnlandedWorkAndClearsAfterLanding \
    "job-writes-then-display-reads, end to end on a master-branch repo"
mon TestPostLandWarmIsVisibleWithoutWaitingForTheJob \
    "landing your own task shows settled at once, with no job pass in between"
mon TestFreshWorktreeIsSettledWithoutAComparison \
    "and so does claiming one — a branch cut at the base needs no comparison"
mon TestWorktreesAtOneTipShareOneAnswer \
    "two worktrees at one branch tip share one answer, paying no git at all"
mon TestComputeOnMissReadsTheCacheFirst \
    "compute-on-miss MISSES first: a warm cache costs the reaper no git at all"
mon TestRefreshSkipsADirectoryThatIsNotARepository \
    "a registered project that is not a git repo is skipped, not a job failure"
mon TestRefreshRecordsAnUnresolvableBaseWithoutFailingTheSweep \
    "one project's unresolvable base records ERR-0011 and does not fail the sweep"
mon TestRefreshSkipsAProjectWithNoTaskWorktrees \
    "a project with no task worktrees is skipped before any git call runs"

go_claim ./internal/sessionstatuscmd/ TestUnsettledMark \
    "all four column states, with ~ outranking ◆ on an uncomputed row"
go_claim ./internal/sessionstatuscmd/ TestUnsettledMarkGlyphWidths \
    "every state is display width 1, so the 13-col prefix still aligns"
go_claim ./internal/sessionstatuscmd/ TestRenderFourStateColumn \
    "one frame holding all four, distinguishable and column-aligned"
go_claim ./internal/sessionstatuscmd/ TestLegendGlyphsAreUnique \
    "no two legend states share a glyph — the check that ruled out ·"
go_claim ./internal/sessionstatuscmd/ TestNotStartedDoesNotVetoDim \
    "~ joins ⊙ in not un-dimming a row"
go_claim ./internal/unlandedjob/ TestRegistered \
    "the job is registered, so something actually writes the cache"
go_claim ./internal/faults/ TestCatalog_EveryCodeIsDocumented \
    "ERR-0012 has a documented remedy in docs/errors.md"

# ---------------------------------------------------------------------------
# A real repository, a real linked worktree, the real binary.
# ---------------------------------------------------------------------------
# Built from this tree rather than taken from bin/, so the suite proves what is
# committed here. GIT_CONFIG_NOSYSTEM keeps a machine-wide setting out of the
# measurement; the runner has already given this suite a temp HOME.
export GIT_CONFIG_NOSYSTEM=1

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go \
    || setup_error "cannot build cmd/endless-go from this tree"

REPO="${TMP}/repo"
git init -q --initial-branch=main "${REPO}" || setup_error "git init failed"
git -C "${REPO}" config user.name t
git -C "${REPO}" config user.email t@example.com
printf 'x\n' >"${REPO}/f.txt"
git -C "${REPO}" add f.txt >/dev/null 2>&1 || setup_error "git add failed"
git -C "${REPO}" commit -qm initial || setup_error "git commit failed"

# A LINKED worktree, at the convention's path. Its own .git is a file pointing
# into the main checkout's administrative area, which is exactly the shape the
# cache's location has to get right.
WTDIR="${REPO}/.endless/worktrees/e-4242"
git -C "${REPO}" worktree add -q -b task/4242 "${WTDIR}" \
    || setup_error "git worktree add failed"
printf 'work\n' >"${WTDIR}/work.txt"
git -C "${WTDIR}" add work.txt >/dev/null 2>&1 || setup_error "git add in worktree failed"
git -C "${WTDIR}" commit -qm "E-4242: the work" || setup_error "git commit in worktree failed"

# The base moves independently, so the comparison has two non-empty ranges —
# the ordinary state of a repo other people are landing into.
printf 'other\n' >"${REPO}/other.txt"
git -C "${REPO}" add other.txt >/dev/null 2>&1 || setup_error "git add on base failed"
git -C "${REPO}" commit -qm "someone else's commit" || setup_error "git commit on base failed"

CACHE="${REPO}/.git/info/endless/unlanded"
probe() { "${BIN}" session-query worktree-unsettled "${WTDIR}" 2>&1; }
head_oid() { git -C "${WTDIR}" rev-parse HEAD; }
base_oid() { git -C "${REPO}" rev-parse main; }

first="$(probe)" || setup_error "the probe command failed: ${first}"

# ---------------------------------------------------------------------------
section "C. The cache is real, and one per repository"
# ---------------------------------------------------------------------------
# `git rev-parse --path-format=absolute --git-common-dir` resolves to the main
# checkout's .git from inside ANY linked worktree, so every worktree of a repo
# shares one cache directory for free. That is not a convenience: the whole point
# is that the verdict is a property of a branch tip and a base branch, not of the
# directory the probe happened to run in, so two worktrees at one OID must share
# one entry rather than compute two.
#
# .git/info/ is untracked, never pushed, per-clone, and survives `git gc` — the
# right home for derived state describing this clone, and the reason no database
# is involved and no sandbox routing applies.

assert_contains "the on-demand probe establishes the verdict" \
    '"unlanded_known": true' "${first}"
assert_contains "and answers the question exactly" '"unlanded_count": 1' "${first}"
assert_contains "naming the commit it found" "E-4242: the work" "${first}"

if [[ -d "${CACHE}" ]]; then
    report_pass "the cache lives under the git COMMON dir, not the worktree's own"
else
    report_fail "the cache lives under the git COMMON dir, not the worktree's own" \
        "a directory at ${CACHE}" "$(ls -d "${REPO}"/.git/info/* 2>&1 || echo 'nothing')"
fi
if compgen -G "${REPO}/.git/worktrees/*/info/endless" >/dev/null; then
    report_fail "no per-worktree copy of the cache" \
        "nothing under .git/worktrees/*/info/endless" \
        "$(ls -d "${REPO}"/.git/worktrees/*/info/endless)"
else
    report_pass "no per-worktree copy of the cache — they share exactly one"
fi

# Only the background job writes the watermark, so a cache populated purely by
# on-demand callers has none yet. That is why a fresh clone shows ~ for one
# interval: readers address the cache THROUGH the watermark.
if compgen -G "${CACHE}/base-*" >/dev/null; then
    report_fail "an on-demand caller does not advance the watermark" \
        "no base-* file yet" "$(ls "${CACHE}"/base-* 2>&1)"
else
    report_pass "an on-demand caller does not advance the watermark — one writer"
fi

# ---------------------------------------------------------------------------
section "D. Every key is a path, and there is no file format"
# ---------------------------------------------------------------------------
# Deliberately absent from the layout, each removable without breaking a rule: a
# format version (this is rebuildable derived state, so an unreadable file is
# simply a miss and no migration can ever be needed), the worktree path, a
# per-entry base name, a stored probe error, and a computed-at timestamp. Nothing
# here can drift out of step with git, because every key IS a git object id.

ENTRY="${CACHE}/unsettled/$(base_oid)/$(head_oid)"
if [[ -f "${ENTRY}" ]]; then
    report_pass "the unsettled entry is keyed <base-tip>/<branch-tip>"
else
    report_fail "the unsettled entry is keyed <base-tip>/<branch-tip>" \
        "a file at ${ENTRY}" "$(find "${CACHE}" -type f 2>&1)"
fi
assert_eq "and holds exactly the lines a consumer needs — one per unlanded commit" \
    "1" "$(wc -l <"${ENTRY}" | tr -d ' ')"
assert_contains "which is the commit itself, not a record about it" \
    "E-4242: the work" "$(cat "${ENTRY}")"

# Now land it the way `worktree land` does — rebase the branch onto the base,
# fast-forward the base to it — which leaves the branch tip equal to the base.
git -C "${WTDIR}" rebase -q main || setup_error "rebase failed"
git -C "${REPO}" merge -q --ff-only task/4242 || setup_error "ff-merge failed"
landed="$(probe)" || setup_error "the probe command failed: ${landed}"

assert_contains "after a land the verdict is settled" '"reason": "settled"' "${landed}"
assert_contains "with nothing outstanding" '"unlanded_count": 0' "${landed}"
assert_contains "and the verdict is established, not merely absent" \
    '"unlanded_known": true' "${landed}"

MARKER="${CACHE}/settled/$(head_oid)"
if [[ -f "${MARKER}" ]]; then
    report_pass "'fully landed' is recorded as settled/<branch-tip>"
else
    report_fail "'fully landed' is recorded as settled/<branch-tip>" \
        "a file at ${MARKER}" "$(find "${CACHE}" -type f 2>&1)"
fi
assert_eq "and the file is EMPTY — its presence is the whole verdict" \
    "0" "$(wc -c <"${MARKER}" | tr -d ' ')"

# ---------------------------------------------------------------------------
section "E. The invariant: settled survives the base advancing"
# ---------------------------------------------------------------------------
# The unlanded set can only SHRINK as the base gains commits: the probe asks, for
# each branch commit since the fork, whether a matching commit exists on the base
# since the fork, and adding commits to the base only adds candidates to match
# against. Nothing that matched becomes unmatched.
#
# So a verdict of ZERO stays true while the BRANCH tip does not move, however far
# the base advances — which is why settled/ is keyed on the branch tip ALONE. A
# verdict of N>0 is valid only while both tips are unchanged, which is why
# unsettled/ nests under the base tip and invalidation is a path miss needing no
# logic and no stored field.

before_tip="$(base_oid)"
printf 'later\n' >"${REPO}/later.txt"
git -C "${REPO}" add later.txt >/dev/null 2>&1 || setup_error "git add failed"
git -C "${REPO}" commit -qm "the base moves on" || setup_error "git commit failed"

if [[ "$(base_oid)" == "${before_tip}" ]]; then
    setup_error "the fixture did not move the base branch"
fi
if [[ -f "${MARKER}" ]]; then
    report_pass "the settled marker survives an append-only base move"
else
    report_fail "the settled marker survives an append-only base move" \
        "${MARKER} still present" "gone"
fi
moved="$(probe)" || setup_error "the probe command failed: ${moved}"
assert_contains "and the verdict is unchanged" '"reason": "settled"' "${moved}"

# ---------------------------------------------------------------------------
section "F. A branch amend self-invalidates, by path"
# ---------------------------------------------------------------------------
# No invalidation logic runs here and no field is compared. The cache is keyed on
# the branch tip, so moving the tip makes every stored answer about the old one
# unreachable — and the old answer stays correct about the OID it describes,
# which is why two worktrees at one tip can share it.

old_head="$(head_oid)"
git -C "${WTDIR}" commit -q --amend -m "E-4242: the work, amended" \
    || setup_error "amend failed"
new_head="$(head_oid)"
[[ "${new_head}" != "${old_head}" ]] || setup_error "the amend did not move the tip"

if [[ -f "${CACHE}/settled/${old_head}" ]]; then
    report_pass "the old tip's answer is untouched — it is still true of that OID"
else
    report_fail "the old tip's answer is untouched" "settled/${old_head} present" "gone"
fi
if [[ -e "${CACHE}/settled/${new_head}" ]] || compgen -G "${CACHE}/unsettled/*/${new_head}" >/dev/null; then
    report_fail "the amended tip has no cached answer" \
        "nothing keyed on ${new_head}" "$(find "${CACHE}" -name "${new_head}" 2>&1)"
else
    report_pass "the amended tip has no cached answer — the miss IS the invalidation"
fi

amended="$(probe)" || setup_error "the probe command failed: ${amended}"
assert_contains "and the on-demand probe computes it afresh" \
    '"unlanded_known": true' "${amended}"
if [[ -f "${CACHE}/settled/${new_head}" ]] || compgen -G "${CACHE}/unsettled/*/${new_head}" >/dev/null; then
    report_pass "storing what it computed under the new tip"
else
    report_fail "storing what it computed under the new tip" \
        "an entry keyed on ${new_head}" "$(find "${CACHE}" -type f 2>&1)"
fi

# ---------------------------------------------------------------------------
section "G. The reaper is off the hook path"
# ---------------------------------------------------------------------------
# This half is subtraction only. The five ReapWorktreesForProject calls in
# internal/hookcmd/claude.go — SessionStart, PreToolUse, PostToolUse, Stop,
# SessionEnd — are deleted and nothing replaces them. Reaping was already
# happening at land; the hook copies were duplicating it before and after every
# tool call, and that cost is what forced condition 4 onto an inexact test.

hook="$(cat "${WT}/internal/hookcmd/claude.go")"
assert_eq "no hook branch reaps worktrees any more" \
    "0" "$(printf '%s' "${hook}" | grep -c 'ReapWorktreesForProject' || true)"
assert_contains "and the removal says where reaping lives instead" \
    "worktree land" "$(printf '%s' "${hook}" | sed -n '/stale-worktree reaper/,/+20p/p' | head -20)"

reap="$(cat "${WT}/internal/monitor/reap_worktrees.go")"
assert_eq "the cheap SHA-containment test it needed is gone" \
    "0" "$(printf '%s' "${reap}" | grep -c 'reapNothingToLand' || true)"
assert_eq "and so is the landed-SHA credit that propped it up" \
    "0" "$(printf '%s' "${reap}" | grep -c 'landedSHAs' || true)"
assert_contains "condition 4 reads the shared cache, computing on a miss" \
    "computeUnlandedAndCache(ctx, dir, base)" "${reap}"
assert_contains "while the filesystem checks stay live and uncached" \
    'runGit(ctx, dir, "status", "--porcelain")' "${reap}"

# ---------------------------------------------------------------------------
section "H. Where the decisions live"
# ---------------------------------------------------------------------------
# None of these is visible from the outside, and each is the design rather than
# an implementation detail.

assert_contains "runGit takes a context as its first parameter" \
    "var runGit = func(ctx context.Context, dir string, args ...string) (string, error) {" "${reap}"
assert_contains "and builds a cancellable child" \
    'exec.CommandContext(ctx, "git", full...)' "${reap}"
assert_eq "there is no context-free door beside it" \
    "0" "$(printf '%s' "${reap}" | grep -c 'exec.Command("git"' || true)"
# A cancelled context is classified WITH SIGINT rather than by widening the
# signal test: exec.CommandContext kills with SIGKILL, and a SIGKILL is usually
# the OOM killer — a fact worth an incident. Recognising the cancellation by the
# CONTEXT keeps that signal.
assert_contains "a cancelled context is the second benign death, beside SIGINT" \
    "case err != nil && ctx.Err() != nil:" "${reap}"
assert_contains "and SIGKILL stays faultable" \
    "ws.Signal() == syscall.SIGINT" "${reap}"

probe_src="$(cat "${WT}/internal/monitor/worktree_unsettled.go")"
assert_contains "the ◆ path is cache-only, by the mode it passes" \
    "return worktreeUnsettledAt(ctx, worktreePath, unlandedCacheOnly, false)" "${probe_src}"
assert_contains "the on-demand path computes on a miss" \
    "return worktreeUnsettledAt(ctx, worktreePath, unlandedComputeOnMiss, true)" "${probe_src}"
assert_contains "and the cache-only branch returns before the resolver is reached" \
    "if mode == unlandedCacheOnly {" "${probe_src}"

cache_src="$(cat "${WT}/internal/monitor/unlanded_cache.go")"
assert_eq "the cache marshals nothing — there is no record type" \
    "0" "$(printf '%s' "${cache_src}" | grep -c 'encoding/json' || true)"
assert_contains "an unsettled entry is written temp-then-rename" \
    "func writeFileAtomic(path string, data []byte) error {" "${cache_src}"
assert_contains "a settled marker is create-or-nothing" \
    "os.O_CREATE|os.O_WRONLY" "${cache_src}"

mark_src="$(cat "${WT}/internal/sessionstatuscmd/session_status.go")"
assert_contains "~ is tested FIRST in the column" \
    "case !r.UnsettledKnown:" "${mark_src}"
assert_contains "and the legend documents it like the other states" \
    'undeterminedGlyph+" not yet determined"' "${mark_src}"

assert_contains "the job is wired by blank import, like the other three" \
    '_ "github.com/mikeschinkel/endless/internal/unlandedjob"' \
    "$(cat "${WT}/cmd/endless-go/main.go")"
assert_contains "and is fired by the existing liveview trigger — nothing new wired" \
    "jobs.RunDue(context.Background())" "$(cat "${WT}/internal/liveview/liveview.go")"

summary
