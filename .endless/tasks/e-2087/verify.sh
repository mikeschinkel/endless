#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2087 and records what was true when E-2087
# landed. Edit it only if you ARE E-2087. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2087: "has this branch's work reached the base branch?" was being answered
# by SHA reachability. `worktree land` rebases before it fast-forwards, so the
# base gets a copy of every commit under a new hash while the branch keeps the
# original — and the probe reported those copies as unlanded forever, with the
# count growing on every later land. Measured on this repo before the fix: 45
# worktrees reporting 118 unlanded commits, 73 of which were real; e-1834 alone
# reported 11, every one of them already on main.
#
# What is verified here:
#   A. Fail-fast: this task's own Go and Python tests all pass.
#   B. The bug itself, end to end on a real repository: a rebase-landed branch
#      that the SHA probe still counts reads SETTLED.
#   C. E-2089's correction — patch-id is not enough. A landing whose diff
#      changed when a conflict was resolved reads settled even though
#      `git cherry` still calls it unlanded.
#   D. The probe did not simply start answering "settled": work the base branch
#      really lacks is still reported, and named.
#   E. Fail-closed is intact — a probe that cannot run is undetermined, never
#      the all-clear.
#   F. The probe reads no database. E-1940's landed-SHA credit was a stand-in
#      for this fix and is gone, along with the `landed_shas` wire field.
#   G. The reaper does NOT read that probe, and the reason is load-bearing:
#      it runs on PreToolUse/PostToolUse. It keeps the cheap containment test,
#      and no longer requires a recorded landing to reclaim a settled worktree.
#
# See E-2087's analysis (endless task show E-2087 --analysis).
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# First and fail-fast, so the one command is a complete proof rather than a
# spot check bolted onto a green build somebody else made. The monitor package
# holds both the content comparison and the reaper; sessionquerycmd holds the
# wire shape Python renders.

if out=$(go test ./internal/monitor/ ./internal/sessionquerycmd/ 2>&1); then
    report_pass "go test: monitor + sessionquerycmd"
else
    report_fail "go test: monitor + sessionquerycmd" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

if out=$(uv run pytest -q tests/test_task_unsettled.py 2>&1); then
    report_pass "pytest: task unsettled renderer"
else
    report_fail "pytest: task unsettled renderer" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# ---------------------------------------------------------------------------
# A scratch repository, and the land that broke the old probe.
# ---------------------------------------------------------------------------
# Built with real git rather than stubbed, because the whole claim is about
# what git does to commits when a rebase replays them. GIT_CONFIG_NOSYSTEM
# keeps a machine-wide setting from changing what is measured; the runner has
# already given this suite a temp HOME.
export GIT_CONFIG_NOSYSTEM=1

REPO="$(mktemp -d)" || setup_error "cannot create a scratch repo"
trap 'rm -rf "${REPO}"' EXIT

git init -q --initial-branch=main "${REPO}" \
    || setup_error "git init failed in ${REPO}"
git -C "${REPO}" config user.name t
git -C "${REPO}" config user.email t@example.com

r() { git -C "${REPO}" "$@" >/dev/null 2>&1 || setup_error "git $* failed"; }
commit() { # commit <branch> <file> <body> <subject>
    r checkout "$1"
    printf '%s\n' "$3" >"${REPO}/$2"
    r add "$2"
    r commit -m "$4"
    r checkout main
}

printf 'x\n' >"${REPO}/f.txt"
r add f.txt
r commit -m initial
r checkout -b task/42-probe
r checkout main

commit task/42-probe feature.txt work "E-42: the work"
# main moves independently, so the land has to REBASE rather than
# fast-forward — which is what rewrites the SHAs.
commit main other.txt elsewhere "someone else's commit"

# The land: replay the branch onto main under new SHAs, fast-forward main to
# the replay, and leave the task branch pointing at its originals.
r checkout -b landing-copy task/42-probe
r rebase main
r checkout main
r merge --ff-only landing-copy
r branch -D landing-copy
r checkout task/42-probe

# probe <dir> — the breakdown for one directory, as the ◆ and `task unsettled`
# read it. `go run` rather than a prebuilt binary so the suite proves THIS
# tree, not whatever bin/ happens to hold.
probe() { go run ./cmd/endless-go session-query worktree-unsettled "$1" 2>&1; }

# ---------------------------------------------------------------------------
section "B. A rebase-landed branch reads settled"
# ---------------------------------------------------------------------------

assert_eq "the fixture reproduces the bug: main..HEAD still counts the copies" \
    "1" "$(git -C "${REPO}" rev-list --count main..HEAD)"

landed_probe="$(probe "${REPO}")"
assert_contains "the verdict is settled" '"reason": "settled"' "${landed_probe}"
assert_contains "and not unsettled" '"unsettled": false' "${landed_probe}"
assert_contains "no commit is counted as unlanded" '"unlanded_count": 0' "${landed_probe}"
assert_not_contains "the verdict was reached, not dodged" '"undetermined": true' "${landed_probe}"

# ---------------------------------------------------------------------------
section "C. A conflict-resolved landing reads settled too"
# ---------------------------------------------------------------------------
# E-2089's correction to the plan. Patch-id survives a plain rebase, but
# `worktree land` rebases onto a MOVING base and resolving a conflict changes
# the diff — after which the branch's copy and the landed copy share neither
# SHA nor patch-id. `git range-diff` pairs them by similarity instead.

CONFLICT="$(mktemp -d)" || setup_error "cannot create the conflict-case repo"
trap 'rm -rf "${REPO}" "${CONFLICT}"' EXIT

git init -q --initial-branch=main "${CONFLICT}" \
    || setup_error "git init failed in ${CONFLICT}"
git -C "${CONFLICT}" config user.name t
git -C "${CONFLICT}" config user.email t@example.com
printf 'x\n' >"${CONFLICT}/f.txt"
git -C "${CONFLICT}" add f.txt >/dev/null 2>&1
git -C "${CONFLICT}" commit -qm initial

git -C "${CONFLICT}" checkout -q -b task/42-probe
printf 'one\ntwo\n' >"${CONFLICT}/shared.txt"
git -C "${CONFLICT}" add shared.txt >/dev/null 2>&1
git -C "${CONFLICT}" commit -qm "E-42: extend shared.txt"

# The landed copy of that same commit, with the diff a conflict resolution
# would have produced: same subject, same file, different content.
git -C "${CONFLICT}" checkout -q main
printf 'one\ntwo\nthree\n' >"${CONFLICT}/shared.txt"
git -C "${CONFLICT}" add shared.txt >/dev/null 2>&1
git -C "${CONFLICT}" commit -qm "E-42: extend shared.txt"
git -C "${CONFLICT}" checkout -q task/42-probe

cherry="$(git -C "${CONFLICT}" cherry main HEAD)"
assert_contains "the fixture reproduces the patch-id miss: git cherry says '+'" \
    "+" "${cherry}"

conflict_probe="$(probe "${CONFLICT}")"
assert_contains "the verdict is still settled" '"reason": "settled"' "${conflict_probe}"
assert_contains "no commit is counted as unlanded" '"unlanded_count": 0' "${conflict_probe}"

# ---------------------------------------------------------------------------
section "D. Work the base branch really lacks is still reported"
# ---------------------------------------------------------------------------
# The half a probe that answered "settled" to everything would also pass B and
# C. This is what stops that.

commit task/42-probe later.txt later "E-42: written after the land"
r checkout task/42-probe

unlanded_probe="$(probe "${REPO}")"
assert_contains "the verdict names the unlanded sub-state" \
    '"unlanded": true' "${unlanded_probe}"
assert_contains "exactly the post-land commit is counted" \
    '"unlanded_count": 1' "${unlanded_probe}"
assert_contains "and it is named, not just counted" \
    "E-42: written after the land" "${unlanded_probe}"
assert_not_contains "the commits that landed are not named" \
    "E-42: the work" "${unlanded_probe}"

# ---------------------------------------------------------------------------
section "E. A probe that cannot run is undetermined, never the all-clear"
# ---------------------------------------------------------------------------

nonrepo_probe="$(probe "$(mktemp -d)")"
assert_contains "an uninspectable directory is undetermined" \
    '"undetermined": true' "${nonrepo_probe}"
assert_contains "and therefore unsettled" '"unsettled": true' "${nonrepo_probe}"
assert_not_contains "it is never reported settled" \
    '"reason": "settled"' "${nonrepo_probe}"

# ---------------------------------------------------------------------------
section "F. The probe reads no database"
# ---------------------------------------------------------------------------
# E-1940 credited task_landings.merge_commit_sha because a rebasing land
# rewrote the SHAs. That was a stand-in for this fix: it could only ever HIDE
# unlanded work, and it made a deliberately DB-free probe (E-1766) read the
# database. Content comparison subsumes it — and neither of the two worst
# offenders measured for this task had a task_landings row at all.

assert_eq "the landed-SHA lookup is gone" \
    "absent" \
    "$([[ -f "${WT}/internal/monitor/task_landings.go" ]] && echo present || echo absent)"
assert_not_contains "no landed_shas field survives on the wire" \
    "landed_shas" "$(cat "${WT}/internal/sessionquerycmd/session_query.go")"
assert_not_contains "and the renderer no longer credits landings" \
    "landed_shas" "$(cat "${WT}/src/endless/task_cmd.py")"
assert_not_contains "the settled verdict carries no landing credit" \
    '"landed_shas"' "${landed_probe}"

# ---------------------------------------------------------------------------
section "G. The reaper keeps the CHEAP check, and stays off the slow one"
# ---------------------------------------------------------------------------
# This section asserts the opposite of what it did when E-2087 first landed,
# and the reversal is the finding. monitor.ReapWorktreesForProject is called
# from five branches of internal/hookcmd/claude.go, including PreToolUse and
# PostToolUse — the reaper sweeps every worktree before and after every tool
# call in every session. Routing that through the content comparison made each
# sweep ~90s and stopped the product. The reaper answers condition 4 with a
# sufficient condition instead: a cheap containment test, wrong only in the
# direction that refuses to reap. E-2111 is where it gets the exact answer back
# affordably; until then this is the guard that keeps it off the hot path.

reap_src="$(cat "${WT}/internal/monitor/reap_worktrees.go")"
assert_not_contains "the reaper does not call the content probe" \
    "worktreeUnsettledAt(" "${reap_src}"
assert_contains "condition 4 is the cheap containment test" \
    "nothing, gerr := reapNothingToLand(dir, base, landedRefs)" "${reap_src}"
assert_contains "which is one rev-list, crediting the recorded landings (E-1940)" \
    'args := []string{"rev-list", "--count", "--ignore-missing", "HEAD", "^" + base}' "${reap_src}"
assert_contains "a missing landing row no longer disqualifies" \
    "hasLanding = false" "${reap_src}"
assert_contains "but a task with no recorded moment at all is skipped" \
    "if latest.IsZero() {" "${reap_src}"

# The hook wiring is the reason for all of the above; assert it rather than
# trusting the comment, so a future move off the hook path is noticed here.
hook_src="$(cat "${WT}/internal/hookcmd/claude.go")"
reap_calls="$(printf '%s' "${hook_src}" | grep -c 'ReapWorktreesForProject')"
assert_eq "the reaper is still on five hook branches (E-2111's premise)" \
    "5" "${reap_calls}"

summary
