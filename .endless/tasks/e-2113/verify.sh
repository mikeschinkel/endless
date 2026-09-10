#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2113 and records what was true when E-2113
# landed. Edit it only if you ARE E-2113. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2113: a git probe KILLED by a signal was recorded as a probe that FAILED.
# Quitting `session monitor` with Ctrl-C — routine right after a land, since the
# land replaces the global binary — sends SIGINT to the pane's whole foreground
# process group. monitor.runGit uses a plain exec.Command, so every in-flight
# git probe is in that group and dies with it. E-1940's fail-closed rule read
# that as "this worktree cannot be verified" and filed ERR-0010 against a task
# that was fine: observed for E-1972 thirteen seconds after a land, detail
# `git range-diff: signal: interrupt`, ONE occurrence — while the probe answered
# cleanly on demand. A real probe failure would have driven the count into the
# thousands within a minute, because the monitor re-probes every row every two
# seconds and the fault dedupes on (worktree, probe).
#
# What is verified here:
#   A. Fail-fast: this task's own tests pass — the whole packages, so the fix
#      is proved against its neighbours rather than in isolation.
#   B. The claims that are only visible from inside, named one by one: the
#      classifier is SIGINT-only, it survives the probe-error wrapper, and an
#      interrupted probe records nothing while an ordinary failure still does.
#   C. The defect itself, end to end through the real binary, against a git
#      child that really is killed by a real SIGINT.
#   D. The verdict is DELIBERATELY unchanged — E-1940's invariant, that a
#      worktree nobody could inspect must never render as verified clean, is
#      intact. Only the incident and the wording are removed.
#   E. Ordinary git failures are untouched: still "failed", still an incident.
#   F. The narrowness is the decision, not an accident — SIGTERM keeps
#      recording, because a probe big enough to be OOM-killed is a real fact.
#   G. The classification lives where the subprocess is created, and the guard
#      lives in the recorders, so a probe added later cannot forget either.
#
# See E-2113's analysis (endless task show E-2113 --analysis).
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# internal/monitor holds the classifier, the probe-error wrapper and the fault
# recorders; sessionquerycmd is the wire the new wording travels on; the pytest
# file is the renderer that puts it in front of a person.

if out=$(go test ./internal/monitor/ ./internal/sessionquerycmd/ 2>&1); then
    report_pass "go test: monitor + sessionquerycmd"
else
    report_fail "go test: monitor + sessionquerycmd" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

if out=$(uv run pytest -q tests/test_task_unsettled.py 2>&1); then
    report_pass "pytest: the task-unsettled renderer"
else
    report_fail "pytest: the task-unsettled renderer" \
        "exit 0" "$(printf '%s' "${out}" | tail -30)"
    summary
fi

# ---------------------------------------------------------------------------
section "B. The claims only visible from inside, named"
# ---------------------------------------------------------------------------
# Section A already ran these. They are re-run by name so this report states
# each claim rather than collapsing all of them into one green package. Signals
# are not fabricable — os.ProcessState cannot be constructed — so every one of
# them signals a REAL child.

go_claim() { # go_claim <test-name> <claim>
    if out=$(go test ./internal/monitor/ -run "^$1\$" -count=1 2>&1); then
        report_pass "$2"
    else
        report_fail "$2" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    fi
}

go_claim TestKilledBySIGINT \
    "the classifier answers SIGINT alone — TERM, KILL, HUP and exit 3 do not"
go_claim TestRunGitClassifiesAtTheSource \
    "runGit attaches the classification where the git child is created"
go_claim TestRunGitLeavesOrdinaryFailuresAlone \
    "and leaves an ordinary git failure unclassified"
go_claim TestInterruptClassificationSurvivesGitProbeError \
    "the classification survives gitProbeError, which used to drop the chain"
go_claim TestInterruptedProbeRecordsNoFault \
    "an interrupted status probe records NO fault"
go_claim TestInterruptedUnlandedProbeRecordsNoFault \
    "nor does the range-diff probe — the one actually observed in the wild"
go_claim TestOrdinaryProbeFailureStillRecords \
    "a genuinely broken worktree still records exactly one incident"

# ---------------------------------------------------------------------------
# A real repository, a real git child, a real SIGINT.
# ---------------------------------------------------------------------------
# The binary is built BEFORE the shim goes anywhere near PATH, and built from
# this tree rather than taken from bin/, so the suite proves what is committed
# here. GIT_CONFIG_NOSYSTEM keeps a machine-wide setting out of the measurement;
# the runner has already given this suite a temp HOME.
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

# shim <name> <body> — a fake `git` whose fate this suite dictates. What is
# under test is how a SIGNALLED child is classified, not git's own behaviour,
# and a real git cannot be made to take a signal at a chosen instant.
shim() {
    local dir="${TMP}/shim-$1"
    mkdir -p "${dir}"
    printf '#!/bin/sh\n%s\n' "$2" >"${dir}/git"
    chmod +x "${dir}/git"
    printf '%s' "${dir}"
}
# The trailing sleep keeps the shell alive long enough for the signal it sent
# itself to be delivered, rather than racing a normal exit.
SHIM_INT="$(shim int 'kill -INT $$; sleep 5')"
SHIM_TERM="$(shim term 'kill -TERM $$; sleep 5')"
SHIM_FAIL="$(shim fail "echo 'fatal: not a git repository' >&2; exit 128")"

# probe <shim-dir> — the breakdown for the scratch repo, as the ◆ and
# `task unsettled` read it, with that shim standing in for git.
probe() { PATH="$1:${PATH}" "${BIN}" session-query worktree-unsettled "${REPO}" 2>&1; }

interrupted="$(probe "${SHIM_INT}")"
terminated="$(probe "${SHIM_TERM}")"
failed="$(probe "${SHIM_FAIL}")"

# ---------------------------------------------------------------------------
section "C. An interrupted probe says interrupted, not failed"
# ---------------------------------------------------------------------------
# `task unsettled <id>` is the one surface whose whole job is explaining the ◆,
# and it was printing "git status failed: signal: interrupt" about a healthy
# worktree. That sentence is the lie, not merely the incident under it.

assert_contains "the reason names the interrupt" \
    '"undetermined_reason": "git status interrupted"' "${interrupted}"
assert_contains "and the list-view summary carries the same words" \
    '"reason": "undetermined (git status interrupted)"' "${interrupted}"
assert_not_contains "nothing about this probe is described as a failure" \
    "git status failed" "${interrupted}"

# ---------------------------------------------------------------------------
section "D. The verdict is unchanged — fail-closed is intact"
# ---------------------------------------------------------------------------
# What E-1940 actually protects is that a worktree nobody could inspect must
# not render identically to a verified-clean one. That survives whole: the row
# still marks. Only the permanent incident, and the false word, are gone.

assert_contains "an interrupted probe established nothing: still undetermined" \
    '"undetermined": true' "${interrupted}"
assert_contains "so the row is still marked" '"unsettled": true' "${interrupted}"
assert_not_contains "and is never reported settled" \
    '"reason": "settled"' "${interrupted}"

# ---------------------------------------------------------------------------
section "E. Ordinary git failures are untouched"
# ---------------------------------------------------------------------------
# The half that would also pass C and D if the fix had simply stopped reporting.

assert_contains "a broken repo still reads as a failure" \
    '"undetermined_reason": "git status failed:' "${failed}"
assert_contains "naming what git said" "not a git repository" "${failed}"
assert_contains "and it is still undetermined" '"undetermined": true' "${failed}"

# ---------------------------------------------------------------------------
section "F. SIGINT alone — the narrowness is the decision"
# ---------------------------------------------------------------------------
# SIGINT is the one signal observed reaching a probe benignly, through the
# terminal's foreground process group. A SIGKILL is usually the OOM killer, and
# a probe big enough to be OOM-killed is a real operational fact; SIGTERM is a
# deliberate kill by something that meant it. Widening the set later is one line
# in killedBySIGINT; narrowing it after silence has hidden something is not.

assert_contains "a SIGTERMed probe is still reported as a failure" \
    '"undetermined_reason": "git status failed:' "${terminated}"
assert_contains "naming the signal that killed it" "signal: terminated" "${terminated}"
assert_not_contains "it is not swept in with the interrupt" \
    "interrupted" "${terminated}"

# ---------------------------------------------------------------------------
section "G. Classified at the source, guarded in the recorders"
# ---------------------------------------------------------------------------
# Both placements are the design, and neither is visible from the outside.
# runGit is the single point every probe's git subprocess is created, so
# classifying there means no caller can forget to ask. The guard sits inside the
# functions that record, rather than at the four places that call them, so a
# probe added later inherits it instead of having to remember it.

reap_src="$(cat "${WT}/internal/monitor/reap_worktrees.go")"
unsettled_src="$(cat "${WT}/internal/monitor/worktree_unsettled.go")"
unlanded_src="$(cat "${WT}/internal/monitor/worktree_unlanded.go")"

assert_contains "runGit classifies its own child" \
    "if killedBySIGINT(err) {" "${reap_src}"
assert_contains "gitProbeError exposes the cause it used to swallow" \
    "func (e gitProbeError) Unwrap() error { return e.Err }" "${unlanded_src}"

guarded="$(printf '%s%s' "${unsettled_src}" "${reap_src}" \
    | grep -c 'errors.Is(err, ErrGitInterrupted)')"
assert_eq "all three git-derived fault recorders carry the guard" \
    "3" "${guarded}"

# The fourth faults.Record in this package is the worktree-LOOKUP failure — a
# database error, not a subprocess. It is deliberately not guarded, and this
# asserts that on purpose rather than by omission.
assert_contains "the lookup fault is a database error and stays unguarded" \
    "monitor.WorktreePathForTask" "${unsettled_src}"

summary
