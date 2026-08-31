#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-1940 and records what was true when E-1940
# landed. Edit it only if you ARE E-1940. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-1940 verification script — landed-state detection reads the RECORDED
# landing, and a probe that cannot run says so instead of saying "you're clear".
#
# Three defects, one function (monitor.UnsettledDetail) and its two consumers
# (the ◆ marker and the worktree reaper):
#
#   1. FAIL-OPEN. Any failed probe — worktree lookup, `git status`, `git
#      rev-list` — returned false, the settled verdict, and Reason() literally
#      rendered "settled (git status failed: …)". A worktree nobody could
#      inspect drew exactly like a worktree verified clean. The reaper ran the
#      same two probes and had always failed CLOSED, so the two surfaces that
#      claim to agree on "done and landed" disagreed precisely where it
#      mattered, and the display had picked the unsafe polarity.
#
#   2. HARDCODED `main`. `rev-list main..HEAD` exits 128 on any project whose
#      default branch differs, so combined with (1) those projects showed a
#      PERMANENT false all-clear and ◆ could never appear — while the reaper
#      skipped every candidate forever. Absorbs E-1166, whose Python-side
#      `_default_base_branch` returned the literal "main" for the same reason.
#
#   3. FALSE UNLANDED. `worktree land` REBASES before fast-forwarding, so every
#      landed commit is rewritten under a new SHA while the branch keeps the
#      originals. `<base>..HEAD` therefore counted a correctly-landed branch as
#      unlanded forever, with the count GROWING as the base advanced — and the
#      remedy it printed, `endless worktree land E-NNNN`, would replay hundreds
#      of stale commits. Measured when the bug was found: 51 worktrees with a
#      recorded landing still reporting unlanded, 43 of them by 45-326 commits.
#      Same probe is reap condition 4, which is why landed worktrees accumulated
#      without bound. Absorbs E-1308, a third symptom of the same cause.
#
# The fix crediting the recorded landing anchors on the BRANCH, not on the base:
# a landing SHA is excluded from the branch's own history. That survives a later
# rewrite of the base's history, which the obvious "is the landing SHA reachable
# from the base?" test does not — 80 of this repo's 599 recorded landing SHAs
# are no longer reachable from main, while all 51 affected branches still reach
# their own landing SHA. Section D pins that distinction directly.
#
# Run from anywhere inside the worktree:
#   endless task verify E-1940
#
# Output: pass/fail per check, then a summary. Exit 0 on all-passed, 1 on any
# failure, 2 on a setup problem.
#
# Section A is a FAIL-FAST gate: the Go unit suites for the resolver, the
# verdict polarity, the landing credit and the reaper, plus this task's pytest
# files, run first and the script stops there if they fail. Everything after
# asserts end-to-end behaviour that is meaningless if the unit level is broken.
#
# Isolation: throwaway git repos under a temp dir, each with its own
# XDG_CONFIG_HOME (own DB and ledger) and XDG_CACHE_HOME. No real DB, ledger,
# worktree or cache is touched. The Go probe is invoked directly from
# <worktree>/bin with --config-dir pointed at the temp DB; the Python CLI runs
# from the worktree source with <worktree>/bin prepended to PATH.

# Refuse a direct run, and pick up the shared harness vocabulary. Sourced as the
# FIRST executable statement so the refusal fires before anything in this file
# runs; every definition below overrides the harness's own, so a suite written
# before the harness existed behaves exactly as it did.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

# ─── output ─────────────────────────────────────────────────────────────────

PASS_COUNT=0
FAIL_COUNT=0
FAILED_TESTS=()

if [[ -t 1 ]]; then
    GREEN=$'\033[32m'; RED=$'\033[31m'; DIM=$'\033[2m'; BOLD=$'\033[1m'; RESET=$'\033[0m'
else
    GREEN=""; RED=""; DIM=""; BOLD=""; RESET=""
fi
UNDERLINE="──────────────────────────────────────────────────────────────"

section()     { printf '\n%s%s%s\n%s\n' "${BOLD}" "$1" "${RESET}" "${UNDERLINE}"; }
report_pass() { printf '  %s✓%s %s\n' "${GREEN}" "${RESET}" "$1"; PASS_COUNT=$((PASS_COUNT + 1)); }
report_fail() {
    printf '  %s✗%s %s\n' "${RED}" "${RESET}" "$1"
    printf '      %sexpected:%s %s\n' "${DIM}" "${RESET}" "$2"
    printf '      %sgot:%s      %s\n' "${DIM}" "${RESET}" "$3"
    FAIL_COUNT=$((FAIL_COUNT + 1)); FAILED_TESTS+=("$1")
}
summary() {
    printf '\n%sSummary%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    if [[ "${FAIL_COUNT}" -eq 0 ]]; then
        printf '  %s%d passed%s\n\n  %sALL PASSED%s\n\n' \
            "${GREEN}" "${PASS_COUNT}" "${RESET}" "${GREEN}${BOLD}" "${RESET}"
        return 0
    fi
    printf '  %s%d passed%s, %s%d failed%s\n\n  %sFAILED:%s\n' \
        "${GREEN}" "${PASS_COUNT}" "${RESET}" "${RED}" "${FAIL_COUNT}" "${RESET}" "${RED}${BOLD}" "${RESET}"
    local t; for t in "${FAILED_TESTS[@]}"; do printf '    - %s\n' "${t}"; done
    printf '\n'; return 1
}

# assert_eq DESC EXPECTED ACTUAL
assert_eq() {
    if [[ "$2" == "$3" ]]; then report_pass "$1"; else report_fail "$1" "$2" "$3"; fi
}
# assert_contains DESC NEEDLE HAYSTACK
assert_contains() {
    if [[ "$3" == *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output contains: $2" "$3"; fi
}
# assert_not_contains DESC NEEDLE HAYSTACK
assert_not_contains() {
    if [[ "$3" != *"$2"* ]]; then report_pass "$1"
    else report_fail "$1" "output must NOT contain: $2" "$3"; fi
}

# ─── shared fixture ──────────────────────────────────────────────────────────

WT=""; TMP=""; REPO=""; EGO=""; DBDIR=""; PID=""; WTDIR=""; LANDSHA=""
TASK=9400        # the task whose worktree is landed and re-probed
GONE=9401        # a landed task whose worktree is gone (E-1308)

# E: the worktree's Python CLI against the isolated DB, from the repo dir.
E() { ( cd "$REPO" && uv run --project "$WT" endless "$@" ); }
# Q SQL: one scalar/row out of the isolated DB.
Q() { E sql "$1" --tsv 2>/dev/null; }
# W SQL: a write against the isolated DB (fixture seeding).
W() { E sql "$1" --write >/dev/null 2>&1; }
# PROBE <path>: the candidate Go probe's JSON for one worktree path — the SAME
# code path the ◆ marker renders from.
PROBE() { "$EGO" --config-dir "$DBDIR" session-query worktree-unsettled "$@" 2>&1; }
# FIELD <json> <key>: pull one scalar out of the probe's single-element array.
FIELD() {
    printf '%s' "$1" | python3 -c \
        'import json,sys; print(json.load(sys.stdin)[0].get(sys.argv[1], ""))' "$2" 2>/dev/null
}
# GIT <dir> ...: quiet git in dir.
GIT() { local d="$1"; shift; git -C "$d" "$@" >/dev/null 2>&1; }

# new_repo DIR BRANCH: a git repo with one commit on BRANCH, isolated from the
# developer's own git config so a machine-wide init.defaultBranch cannot leak
# in and change what these checks measure.
new_repo() {
    local dir="$1" branch="$2"
    mkdir -p "$dir"
    env -i PATH="/usr/bin:/bin:/usr/local/bin" HOME="$dir" GIT_CONFIG_NOSYSTEM=1 \
        git init --initial-branch="$branch" "$dir" >/dev/null 2>&1 || return 1
    GIT "$dir" config user.email verify@test
    GIT "$dir" config user.name verify
    printf 'x\n' > "$dir/f.txt"
    GIT "$dir" add f.txt
    GIT "$dir" commit -m "initial"
}

setup_fixture() {
    TMP="$(mktemp -d)"
    REPO="$TMP/repo"
    DBDIR="$TMP/xdg/endless"
    export XDG_CONFIG_HOME="$TMP/xdg"
    export XDG_CACHE_HOME="$TMP/cache"
    export PATH="$WT/bin:$PATH"
    mkdir -p "$DBDIR"

    # The project's default branch is `master`, on purpose: this is the shape
    # the hardcoded probe could never inspect, so every end-to-end check below
    # is simultaneously the non-`main` regression.
    new_repo "$REPO" master || return 1

    printf '{"roots": ["%s"]}\n' "$TMP" > "$DBDIR/config.json"
    E project register "$REPO" --name probe --label Probe --desc d --lang Go --status active >/dev/null 2>&1
    PID="$(Q "SELECT id FROM projects WHERE name='probe'")"
    [[ -n "$PID" ]] || return 1

    W "INSERT INTO tasks (id, project_id, title, status, phase) VALUES
         ($TASK, $PID, 'Landed then re-probed', 'unverified', 'now'),
         ($GONE, $PID, 'Landed and reaped',     'confirmed',  'now')"
    [[ "$(Q "SELECT count(*) FROM tasks WHERE id IN ($TASK,$GONE)")" == "2" ]] || return 1

    # The task's worktree, with one commit of the user's own work on it.
    WTDIR="$REPO/.endless/worktrees/e-$TASK"
    GIT "$REPO" worktree add -b "task/$TASK-probe" "$WTDIR" || return 1
    printf 'work\n' > "$WTDIR/feature.txt"
    GIT "$WTDIR" add feature.txt
    GIT "$WTDIR" commit -m "E-$TASK: the work"
    return 0
}

# land_the_worktree performs what `endless worktree land` performs — rebase the
# branch onto the base, fast-forward the base to it — and records the landing
# from the BASE's HEAD after the merge, exactly as the events bridge does.
land_the_worktree() {
    GIT "$WTDIR" rebase master || return 1
    GIT "$REPO" merge --ff-only "task/$TASK-probe" || return 1
    LANDSHA="$(git -C "$REPO" rev-parse HEAD 2>/dev/null)"
    [[ -n "$LANDSHA" ]] || return 1
    W "INSERT INTO task_landings (task_id, branch, base_branch, merge_commit_sha, landed_at)
         VALUES ($TASK, 'task/$TASK-probe', 'master', '$LANDSHA', '2026-07-01T00:00:00')"
    [[ "$(Q "SELECT count(*) FROM task_landings WHERE task_id=$TASK")" == "1" ]]
}

# ─── A: fail-fast — the unit level this whole change rests on ────────────────

test_unit_gate() {
    section "A. Fail-fast gate: resolver, polarity, landing credit, reaper"
    local out rc

    # The resolver, the verdict polarity, the landing credit, the fault dedup,
    # and the two conditions on the code path that DELETES worktrees.
    out=$(cd "$WT" && go test ./internal/monitor/ \
        -run 'DefaultBranch|Undetermined|Unsettled|Unlanded|NoLandings|MasterProject|Incident|RecordsNothing|Reap' \
        -count=1 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go: monitor probe + resolver + reaper units"
    else report_fail "go: monitor probe + resolver + reaper units" "exit 0" \
        "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -30)"; fi

    # The two new fault codes must be documented; the catalog test is what stops
    # a code shipping with no entry in docs/errors.md for the reader to land on.
    out=$(cd "$WT" && go test ./internal/faults/ -count=1 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "go: fault catalog matches docs/errors.md"
    else report_fail "go: fault catalog matches docs/errors.md" "exit 0" \
        "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -25)"; fi

    out=$(cd "$WT" && uv run pytest \
        tests/test_default_branch_parity.py \
        tests/test_task_unsettled.py \
        tests/test_land_already_landed.py \
        tests/test_worktree_create.py -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "pytest: resolver parity + renderer + already-landed"
    else report_fail "pytest: resolver parity + renderer + already-landed" "exit 0" \
        "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -30)"; fi

    [[ "${FAIL_COUNT}" -eq 0 ]]
}

# ─── B: the marker appears for genuinely unlanded work, on a master repo ─────

test_marks_unlanded_work_on_a_master_project() {
    section "B. Unlanded work is marked — on a project whose base is not \`main\`"

    # The probe the fix replaced cannot even run here. Asserting it pins WHY the
    # hardcoded base was a bug and not a stylistic complaint.
    if git -C "$WTDIR" rev-list main..HEAD --count >/dev/null 2>&1; then
        report_fail "the fixture is genuinely a non-main project" \
            "\`git rev-list main..HEAD\` to fail" "it succeeded"
    else
        report_pass "the fixture is genuinely a non-main project (\`main..HEAD\` exits non-zero)"
    fi

    local out
    out="$(PROBE "$WTDIR")"
    assert_eq "the base resolves to master, not main"    "master" "$(FIELD "$out" base)"
    assert_eq "...and the worktree reads unsettled"      "True"   "$(FIELD "$out" unsettled)"
    assert_eq "...specifically unlanded"                 "True"   "$(FIELD "$out" unlanded)"
    assert_eq "...counting the one real commit"          "1"      "$(FIELD "$out" unlanded_count)"
    assert_eq "...and NOT undetermined — the probe ran"  "False"  "$(FIELD "$out" undetermined)"
}

# ─── C: the marker clears once the work has actually landed ──────────────────

test_clears_after_landing() {
    section "C. A landed worktree reads settled"

    if ! land_the_worktree; then
        report_fail "the fixture lands the worktree" "rebase + ff-merge + landing row" "setup failed"
        return
    fi
    report_pass "the fixture lands the worktree (rebase, ff-merge, recorded landing)"

    local out
    out="$(PROBE "$WTDIR")"
    assert_eq "the landed worktree reads settled"  "False" "$(FIELD "$out" unsettled)"
    assert_eq "...with nothing unlanded"           "0"     "$(FIELD "$out" unlanded_count)"
    assert_contains "...and the landing was credited" "$LANDSHA" "$(FIELD "$out" landed_shas)"
}

# ─── D: the case the obvious fix gets wrong ──────────────────────────────────

test_survives_a_rewritten_base_history() {
    section "D. The verdict survives a rewrite of the base branch's history"

    # This is the state 80 of this repo's 599 recorded landings were found in:
    # the recorded SHA is no longer reachable from the base, but IS still
    # reachable from the branch. A check of the form "is the landing reachable
    # from the base?" regresses here; anchoring on the branch does not.
    GIT "$REPO" commit --amend -m "E-$TASK: the work (rewritten)"

    if git -C "$REPO" merge-base --is-ancestor "$LANDSHA" master >/dev/null 2>&1; then
        report_fail "the fixture detaches the recorded SHA from the base" \
            "the landing SHA unreachable from master" "it is still reachable"
        return
    fi
    report_pass "the fixture detaches the recorded SHA from the base"

    if ! git -C "$WTDIR" merge-base --is-ancestor "$LANDSHA" HEAD >/dev/null 2>&1; then
        report_fail "...while the BRANCH still reaches it" \
            "the landing SHA reachable from the branch" "it is not"
        return
    fi
    report_pass "...while the branch still reaches it — which is what the fix anchors on"

    local out
    out="$(PROBE "$WTDIR")"
    assert_eq "the verdict stays settled"          "False" "$(FIELD "$out" unsettled)"
    assert_eq "...and no stale commit is resurrected" "0"  "$(FIELD "$out" unlanded_count)"
}

# ─── E: work done AFTER the landing is still reported ────────────────────────

test_post_landing_work_is_still_unlanded() {
    section "E. Crediting the landing does not silence real work done since"

    printf 'more\n' > "$WTDIR/after.txt"
    GIT "$WTDIR" add after.txt
    GIT "$WTDIR" commit -m "E-$TASK: work done after landing"

    local out
    out="$(PROBE "$WTDIR")"
    assert_eq "the new commit reads unlanded"     "True" "$(FIELD "$out" unlanded)"
    assert_eq "...and only the new commit counts" "1"    "$(FIELD "$out" unlanded_count)"
}

# ─── F: fail-closed ──────────────────────────────────────────────────────────

test_fails_closed() {
    section "F. A probe that cannot run is undetermined, never the all-clear"

    local out reason
    out="$(PROBE "$TMP/not-a-repo-at-all")"
    assert_eq "an uninspectable worktree is NOT reported settled" "True" "$(FIELD "$out" unsettled)"
    assert_eq "...it is reported undetermined"                    "True" "$(FIELD "$out" undetermined)"
    reason="$(FIELD "$out" reason)"
    assert_contains "...and the verdict text says so" "undetermined" "$reason"
    assert_not_contains "...never the word it used to print" "settled (git" "$reason"
    assert_contains "...naming the probe that failed" "git status" "$(FIELD "$out" undetermined_reason)"

    # A repo with no resolvable default branch: the failure that used to be
    # permanent AND invisible.
    new_repo "$TMP/no-base" develop || {
        report_fail "the fixture builds a repo with no resolvable base" "a repo on develop" "setup failed"
        return
    }
    out="$(PROBE "$TMP/no-base")"
    assert_eq "an unresolvable default branch is undetermined too" "True" "$(FIELD "$out" undetermined)"
    assert_eq "...and no branch is guessed"                        ""     "$(FIELD "$out" base)"
    assert_contains "...the reason names the resolver" \
        "default branch unresolved" "$(FIELD "$out" undetermined_reason)"

    # An explicit project setting is the documented remedy, so it must work.
    mkdir -p "$TMP/no-base/.endless"
    printf '{"name":"nb","default_branch":"develop"}\n' > "$TMP/no-base/.endless/config.json"
    out="$(PROBE "$TMP/no-base")"
    assert_eq "...and \`default_branch\` in .endless/config.json resolves it" \
        "develop" "$(FIELD "$out" base)"
}

# ─── G: the failure is recorded where a user will find it ────────────────────

test_records_a_clearable_fault() {
    section "G. The unanswerable probe is recorded as an error, and dedupes"

    # Fifty ticks over one broken worktree: the monitor re-probes every row every
    # two seconds, so a fault raised per occurrence would bury the list.
    local i
    for i in $(seq 1 50); do PROBE "$TMP/still-not-a-repo" >/dev/null; done

    local out
    out="$(E errors show 2>&1)"
    assert_contains "the probe failure is recorded"         "ERR-0010"   "$out"
    assert_contains "...naming the failing command"         "git status" "$out"
    assert_contains "...and the worktree it could not read" "still-not-a-repo" "$out"

    # Scoped to THIS worktree's incident: section F deliberately broke a second
    # one, and those are two conditions to fix, not one.
    assert_eq "50 ticks leave ONE open incident, not 50" "1" \
        "$(Q "SELECT count(*) FROM errors WHERE code='ERR-0010' AND cleared_at IS NULL AND summary LIKE 'still-not-a-repo%'")"
    assert_eq "...with the occurrences counted in place" "50" \
        "$(Q "SELECT occurrences FROM errors WHERE code='ERR-0010' AND cleared_at IS NULL AND summary LIKE 'still-not-a-repo%'")"
    assert_eq "...and the other broken worktree is its OWN incident" "2" \
        "$(Q "SELECT count(*) FROM errors WHERE code='ERR-0010' AND cleared_at IS NULL")"

    out="$(E errors codes 2>&1)"
    assert_contains "the probe-failure code is in the catalog"   "worktree-probe-failed"     "$out"
    assert_contains "...and so is the resolver-failure code"     "default-branch-unresolved" "$out"
}

# ─── H: the landed-but-gone worktree (absorbed E-1308) ───────────────────────

test_already_landed_message() {
    section "H. \`worktree land\` on a reaped worktree reports the landing"

    W "INSERT INTO task_landings (task_id, branch, base_branch, merge_commit_sha, landed_at)
         VALUES ($GONE, 'task/$GONE-x', 'master', 'feedfacecafebabe1234', '2026-06-01T12:00:00')"

    # Called in-process rather than through `endless worktree land`. Landing is
    # an always-main operation: config.default_db_to_main() pins ~/.config/endless
    # EXPLICITLY, ignoring the injected XDG_CONFIG_HOME (E-1628, so a self-dev
    # session cannot land into its own sandbox). A verify script must never reach
    # the developer's real database, so this sets the DB context first — which
    # makes default_db_to_main a no-op — and calls the function the CLI calls.
    cat > "$TMP/land_probe.py" <<'LANDPROBE'
import os
import sys
from pathlib import Path

from endless import config

config.set_db_context(Path(os.environ["DBDIR"]))

import click  # noqa: E402  (must follow the DB pin)

from endless import worktree_cmd  # noqa: E402

try:
    worktree_cmd.land_worktree(os.environ["GONE_ID"], dry_run=False)
except click.ClickException as exc:
    print(exc.format_message())
    sys.exit(1)
sys.exit(0)
LANDPROBE

    local out rc
    out=$( cd "$REPO" && DBDIR="$DBDIR" GONE_ID="E-$GONE" \
        uv run --project "$WT" python "$TMP/land_probe.py" 2>&1 ); rc=$?
    assert_eq "landing a task with no worktree still refuses" "1" "$rc"
    assert_contains "...but says the work already landed" "already landed" "$out"
    assert_contains "...naming the recorded commit"       "feedfacecafe"   "$out"
    assert_not_contains "...and no longer implies the work is lost" \
        "No endless-managed worktree" "$out"
}

# ─── I: broader suites ───────────────────────────────────────────────────────

test_suites() {
    section "I. Regression suites"
    local out rc

    out=$(cd "$WT" && go test ./internal/... -count=1 2>&1 | grep -v '^ok\|no test files'); rc=$?
    if [[ -z "$out" ]]; then report_pass "go: the full internal suite passes"
    else report_fail "go: the full internal suite" "no failures" "$(printf '%s' "$out" | tail -30)"; fi

    out=$(cd "$WT" && uv run pytest tests/ -q 2>&1); rc=$?
    if [[ $rc -eq 0 ]]; then report_pass "pytest: the full Python suite passes"
    else report_fail "pytest: the full Python suite" "exit 0" \
        "exit=$rc"$'\n'"$(printf '%s' "$out" | tail -30)"; fi
}

# ─── main ─────────────────────────────────────────────────────────────────────

main() {
    WT="$(git rev-parse --show-toplevel 2>/dev/null)"
    [[ -n "${WT}" ]] || { printf 'ERROR: not inside a git worktree\n' >&2; exit 2; }
    EGO="${WT}/bin/endless-go"

    command -v uv >/dev/null      || { printf 'ERROR: uv not on PATH\n' >&2; exit 2; }
    command -v git >/dev/null     || { printf 'ERROR: git not on PATH\n' >&2; exit 2; }
    command -v go >/dev/null      || { printf 'ERROR: go not on PATH\n' >&2; exit 2; }
    command -v python3 >/dev/null || { printf 'ERROR: python3 not on PATH\n' >&2; exit 2; }
    [[ -x "${EGO}" ]] || {
        printf 'ERROR: %s missing — run `just build`\n' "${EGO}" >&2; exit 2; }

    printf '%sE-1940 verification%s\n%s\n' "${BOLD}" "${RESET}" "${UNDERLINE}"
    printf '  worktree: %s\n' "${WT}"

    # The fail-fast gate runs before the fixture: it needs no isolated DB, and a
    # failure here makes every end-to-end assertion below uninterpretable.
    if ! test_unit_gate; then
        printf '\n  %sfail-fast: the unit level is broken; skipping the end-to-end checks%s\n' \
            "${RED}${BOLD}" "${RESET}"
        summary
        exit 1
    fi

    if ! setup_fixture; then
        printf 'ERROR: fixture setup failed (isolated repo/DB seeding)\n' >&2
        [[ -n "$TMP" ]] && rm -rf "$TMP"
        exit 2
    fi

    test_marks_unlanded_work_on_a_master_project
    test_clears_after_landing
    test_survives_a_rewritten_base_history
    test_post_landing_work_is_still_unlanded
    test_fails_closed
    test_records_a_clearable_fault
    test_already_landed_message
    test_suites

    [[ -n "$TMP" ]] && rm -rf "$TMP"
    summary
}

main "$@"
