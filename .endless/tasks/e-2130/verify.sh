#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2130 and records what was true when E-2130
# landed. Edit it only if you ARE E-2130. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2130: E-2113's fix, finished. E-2113 classified a signalled git child at the
# source (ErrGitInterrupted) and guarded the three functions that record a
# git-derived fault. Two of those guards — recordDefaultBranchFault and
# recordReapDefaultBranchFault — were correct and UNREACHABLE, because every
# step of resolveDefaultBranch swallowed its runGit error and returned "". The
# classification died inside the resolver, so a Ctrl-C landing during
# default-branch resolution fell through all four steps and arrived at
# ErrDefaultBranchUnresolved: ERR-0011, "the repository's default branch could
# not be resolved", filed against a worktree whose only problem was that someone
# quit `session monitor`. The same misclassification E-2113 removed for
# ERR-0010, surviving under a different code.
#
# And it outlived the signal. defaultBranchCache memoizes errors as well as
# answers, so the interrupted verdict was cached and every later probe of that
# directory read it — the reaper runs on PreToolUse/PostToolUse in a process
# that keeps going, so one Ctrl-C poisoned the rest of it.
#
# What is verified here:
#   A. Fail-fast: this task's own tests pass — the whole monitor package, plus
#      the Go/Python parity suite, because the resolver's step order has a
#      cross-language contract this change had to keep.
#   B. The claims only visible from inside, named one by one.
#   C. The defect end to end through the real binary, against a real git child
#      killed by a real SIGINT: the report says interrupted, not unresolved.
#   D. The memoized failure does not outlive the signal — the SAME process,
#      probing the SAME directory a second time, gets the real answer.
#   E. The verdict is DELIBERATELY unchanged: E-1940's invariant, that a
#      worktree nobody could inspect never renders as verified clean, is intact.
#   F. The guard does not over-fire. A genuinely unresolvable repository still
#      says unresolved, and ordinary git failures still fall through the four
#      steps as E-1166 requires.
#   G. Where the decision lives: the short-circuit is in the resolver and the
#      one-class-only contract is stated in a single place the helpers call,
#      rather than re-derived at each of the seven sites that could bail.
#
# See E-2130's analysis (endless task show E-2130 --analysis).
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# internal/monitor holds the resolver, the memoization and both default-branch
# fault recorders. The parity suite is here rather than as a courtesy: the
# resolver's four-step order is mirrored by the Python side and asserted case
# for case, so changing what each step RETURNS had a contract to keep.

if out=$(go test ./internal/monitor/ 2>&1); then
    report_pass "go test: internal/monitor"
else
    report_fail "go test: internal/monitor" "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

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
# Section A already ran these. They are re-run by name so this report states
# each claim rather than collapsing all of them into one green package. Signals
# are not fabricable — os.ProcessState cannot be constructed — so every one of
# them signals a REAL child through the production runGit.

go_claim() { # go_claim <test-name> <claim>
    if out=$(go test ./internal/monitor/ -run "^$1\$" -count=1 2>&1); then
        report_pass "$2"
    else
        report_fail "$2" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    fi
}

go_claim TestDefaultBranchPropagatesInterrupt \
    "an interrupted resolve reports the interrupt, not an unresolvable repo"
go_claim TestInterruptedResolveIsNotMemoized \
    "and is never cached — the next probe of that directory asks again"
go_claim TestInterruptedConfiguredBranchIsNotCalledATypo \
    "an interrupted check never accuses the project's own default_branch"
go_claim TestInterruptedDefaultBranchRecordsNoFault \
    "the unsettled probe's recorder is now reached, and records nothing"
go_claim TestInterruptedReapDefaultBranchRecordsNoFault \
    "so is the reaper's — and an interrupt still skips, never reaps"
go_claim TestOrdinaryUnresolvableStillRecords \
    "a genuinely unresolvable repository still records exactly one ERR-0011"
go_claim TestOrdinaryFailureStillFallsThrough \
    "and an ordinary step failure still falls through to the next step"
go_claim TestDefaultBranchMemoizes \
    "a resolved answer is still memoized — the hot path is unchanged"

# ---------------------------------------------------------------------------
# A real repository, a real git child, a real SIGINT.
# ---------------------------------------------------------------------------
# The binary is built BEFORE any shim goes near PATH, and built from this tree
# rather than taken from bin/, so the suite proves what is committed here.
# GIT_CONFIG_NOSYSTEM keeps a machine-wide setting out of the measurement; the
# runner has already given this suite a temp HOME.
export GIT_CONFIG_NOSYSTEM=1

REALGIT="$(command -v git)" || setup_error "git is not on PATH"

TMP="$(mktemp -d)" || setup_error "cannot create a scratch directory"
trap 'rm -rf "${TMP}"' EXIT

BIN="${TMP}/endless-go"
go build -o "${BIN}" ./cmd/endless-go \
    || setup_error "cannot build cmd/endless-go from this tree"

# make_repo <path> <branch> — a repo with one commit, no remote, no
# origin/HEAD: steps 2 and 3 of the resolver have nothing to say about it, which
# is the ordinary shape of a task worktree.
make_repo() {
    git init -q --initial-branch="$2" "$1" || setup_error "git init failed"
    git -C "$1" config user.name t
    git -C "$1" config user.email t@example.com
    printf 'x\n' >"$1/f.txt"
    git -C "$1" add f.txt >/dev/null 2>&1 || setup_error "git add failed"
    git -C "$1" commit -qm initial || setup_error "git commit failed"
}

REPO="${TMP}/repo"
make_repo "${REPO}" main

# The shim's git is interrupted during the FIRST probe of the loop and real
# afterwards. `status` — probe 1, which always runs for real — is what delimits
# one tick, so the arming is tied to the caller's loop rather than to a count of
# git invocations. That matters: the count is exactly what this fix changes (a
# short-circuiting resolver issues ONE call where the old one issued three), so
# a counter-armed shim would be measuring the fix with a ruler the fix moves.
TICKS="${TMP}/ticks"
SHIM="${TMP}/shim"
mkdir -p "${SHIM}"
cat >"${SHIM}/git" <<SHIMBODY
#!/bin/sh
case " \$* " in
  *" status "*)
    n=\$(cat "${TICKS}" 2>/dev/null || echo 0)
    printf '%s' \$((n + 1)) >"${TICKS}"
    exec "${REALGIT}" "\$@" ;;
esac
if [ "\$(cat "${TICKS}" 2>/dev/null || echo 0)" -le 1 ]; then
  # The trailing sleep keeps the shell alive long enough for the signal it sent
  # itself to be delivered, rather than racing a normal exit.
  kill -INT \$\$
  sleep 5
fi
exec "${REALGIT}" "\$@"
SHIMBODY
chmod +x "${SHIM}/git" || setup_error "cannot install the git shim"

# Two probes of the SAME directory in ONE process — the shape `session monitor`
# and the reaper both have, and the only shape in which memoization is visible.
printf '0' >"${TICKS}"
probes="$(PATH="${SHIM}:${PATH}" "${BIN}" session-query worktree-unsettled \
    "${REPO}" "${REPO}" 2>&1)" || setup_error "the probe command failed: ${probes}"

# Split the JSON array into the first probe and the second. Done with awk on the
# element boundary rather than a JSON tool so the suite depends on nothing the
# product does not already require.
first="$(printf '%s' "${probes}" | awk '/^  \{/{n++} n==1')"
second="$(printf '%s' "${probes}" | awk '/^  \{/{n++} n==2')"
[[ -n "${first}" && -n "${second}" ]] \
    || setup_error "could not split the two probes out of: ${probes}"

# ---------------------------------------------------------------------------
section "C. An interrupted resolve says interrupted, not unresolved"
# ---------------------------------------------------------------------------
# `task unsettled <id>` is the surface whose whole job is explaining the ◆, and
# the sentence it printed — "default branch unresolved: cannot resolve the
# repository's default branch" — is a positive claim about the repository, made
# out of four probes that never got to run. ERR-0011 is filed on the same claim.

assert_contains "the reason names the interrupt" \
    '"undetermined_reason": "default branch resolution interrupted"' "${first}"
assert_contains "and the list-view summary carries the same words" \
    '"reason": "undetermined (default branch resolution interrupted)"' "${first}"
assert_not_contains "the repository is never called unresolvable" \
    "default branch unresolved" "${first}"
assert_contains "the classification reached the caller intact" \
    '"base_error": "git interrupted' "${first}"

# ---------------------------------------------------------------------------
section "D. The failure does not outlive the signal"
# ---------------------------------------------------------------------------
# Same process, same directory, second probe — with git working perfectly. Here
# the cached interrupt used to answer, so a single Ctrl-C cost every later probe
# in the process rather than one tick. An interrupt is a fact about the process
# that asked, not about the repository, and it stops being true immediately.

assert_contains "the second probe resolves the branch for real" \
    '"base": "main"' "${second}"
assert_contains "so the worktree reads as settled" '"reason": "settled"' "${second}"
assert_contains "with nothing left undetermined" '"undetermined": false' "${second}"
assert_not_contains "and no trace of the earlier interrupt" \
    "interrupted" "${second}"

# ---------------------------------------------------------------------------
section "E. The verdict is unchanged — fail-closed is intact"
# ---------------------------------------------------------------------------
# What E-1940 protects is that a worktree nobody could inspect must not render
# identically to a verified-clean one. That survives whole: the interrupted row
# still marks. Only the false sentence, and the incident under it, are gone.

assert_contains "an interrupted resolve established nothing: still undetermined" \
    '"undetermined": true' "${first}"
assert_contains "so the row is still marked" '"unsettled": true' "${first}"
assert_not_contains "and is never reported settled" '"reason": "settled"' "${first}"
assert_contains "no branch is guessed in its place" '"base": ""' "${first}"

# ---------------------------------------------------------------------------
section "F. The guard does not over-fire"
# ---------------------------------------------------------------------------
# The failure mode of over-applying E-2113 is silence. ERR-0011 exists because
# an unresolvable default branch disables the unsettled probe and the reaper's
# unmerged-commits condition for the whole project, so a repo that genuinely
# cannot name its branch must still say so — with a real, unshimmed git.

ORPHAN="${TMP}/orphan"
make_repo "${ORPHAN}" develop   # not main, not master, nothing to detect from
orphan="$("${BIN}" session-query worktree-unsettled "${ORPHAN}" 2>&1)" \
    || setup_error "the probe command failed: ${orphan}"

assert_contains "a repository that cannot name its branch still says so" \
    '"undetermined_reason": "default branch unresolved:' "${orphan}"
assert_not_contains "and is not excused as an interrupt" "interrupted" "${orphan}"
assert_contains "it is still undetermined" '"undetermined": true' "${orphan}"
assert_not_contains "and no branch is substituted for the one it cannot name" \
    '"base": "main"' "${orphan}"

# The other half of not-over-firing: an ordinary failure must keep FALLING
# THROUGH. `rev-parse --verify` exits non-zero for every candidate that is not a
# branch — the resolver's normal answer of "not this one" — and propagating that
# as an error would stop the chain at step 2 and undo E-1166.
FALLS="${TMP}/falls"
make_repo "${FALLS}" master
git -C "${FALLS}" config init.defaultBranch main   # names no branch here
falls="$("${BIN}" session-query worktree-unsettled "${FALLS}" 2>&1)" \
    || setup_error "the probe command failed: ${falls}"

assert_contains "two failing steps still fall through to the branch that exists" \
    '"base": "master"' "${falls}"

# ---------------------------------------------------------------------------
section "G. Where the decision lives"
# ---------------------------------------------------------------------------
# Neither placement is visible from the outside, and both are the design. The
# helpers return an error for ONE class of failure, stated in one function they
# all call, so no step re-derives the distinction and no step added later can
# absorb an interrupt by accident. The resolver bails on any error the helpers
# hand back, because by that contract there is only one kind.

src="$(cat "${WT}/internal/monitor/default_branch.go")"

assert_contains "the one-class-only contract is a single function" \
    "func interruptOnly(err error) error {" "${src}"
assert_contains "and it is what the helpers return" \
    "return \"\", interruptOnly(err)" "${src}"
assert_eq "all three fall-through helpers use it, none open-code the test" \
    "3" "$(printf '%s' "${src}" | grep -c 'interruptOnly(err)')"
assert_contains "DefaultBranch declines to memoize an interrupt" \
    "if errors.Is(err, ErrGitInterrupted) {" "${src}"
assert_eq "the interrupt test appears exactly twice: the contract, and the cache" \
    "2" "$(printf '%s' "${src}" | grep -c 'errors.Is(err, ErrGitInterrupted)')"
assert_not_contains "and never inside a resolution step, which must not re-derive it" \
    "ErrGitInterrupted" "$(printf '%s' "${src}" | sed -n '/^func resolveDefaultBranch/,/^}/p')"

summary
