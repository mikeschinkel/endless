#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2174 and records what was true when E-2174
# landed. Edit it only if you ARE E-2174. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2174: a held index.lock must not fail a land.
#
# Observed 2026-09-23 landing E-2137, from a clean worktree with no conflict:
#
#     error: Unable to create '.git/worktrees/e-2137/index.lock': File exists.
#     Another git process seems to be running in this repository...
#     error: could not detach HEAD
#
# The holder — PID 43112, running `git -C <worktree> status --porcelain` — was
# still alive at diagnosis, exited on its own moments later, and the identical
# land then succeeded with nothing changed. `land_worktree` had run its whole
# body inside a LAND_MAX_RETRIES loop since E-987, but Step 4's rebase raised
# directly, walking out of the retry loop it was standing inside.
#
# What is verified here:
#   A. Fail-fast: this task's own tests, plus the predicate tests it must not
#      have broken.
#   B. The claims only visible from inside, named one by one.
#   C. The condition is REAL, not a mocked string: a genuine `git rebase` in a
#      genuine linked worktree whose index.lock is genuinely held, and the
#      predicate matching the text GIT ITSELF produced. A test that only ever
#      sees a hand-typed error message cannot tell you that.
#   D. Waiting is the whole recovery. Nothing here removes a lock file — the
#      holder is a live process, and deleting its lock is how an index gets
#      corrupted.
#
# See E-2174's plan (endless task show E-2174 --all-fields).
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

WT="$(git rev-parse --show-toplevel)"
cd "${WT}" || setup_error "cannot cd to worktree root ${WT}"

# ---------------------------------------------------------------------------
section "A. This task's own tests (fail-fast)"
# ---------------------------------------------------------------------------
# The new suite is the change. test_worktree_land_retry.py is E-1351's, re-run
# because this task added a SECOND retry predicate beside the one it covers:
# if the two ever stopped being disjoint, that is where it would show.
# test_worktree_land_rebase_failure.py and _conflict_msg.py are E-2122's — the
# conflict-reporting path the new guard now sits in front of.

py_file() { # py_file <path> <label>
    if out=$(uv run pytest -q "$1" 2>&1); then
        report_pass "pytest: $2"
    else
        report_fail "pytest: $2" "exit 0" "$(printf '%s' "${out}" | tail -30)"
        summary
    fi
}

py_file tests/test_worktree_land_lock_contention.py "lock contention, end to end"
py_file tests/test_worktree_land_retry.py           "the ff-merge predicate, unchanged"
py_file tests/test_worktree_land_rebase_failure.py  "rebase failure reporting, unchanged"
py_file tests/test_worktree_land_conflict_msg.py    "conflict reporting, unchanged"

# ---------------------------------------------------------------------------
section "B. The claims only visible from inside, named"
# ---------------------------------------------------------------------------
# Section A already ran these. They are re-run by name so this report states
# each claim rather than collapsing all of them into four green files.

py_claim() { # py_claim <file::test> <claim>
    if out=$(uv run pytest -q "$1" 2>&1); then
        report_pass "$2"
    else
        report_fail "$2" "exit 0" "$(printf '%s' "${out}" | tail -20)"
    fi
}

T=tests/test_worktree_land_lock_contention.py

py_claim "${T}::test_e2e_lock_that_clears_lands_with_no_operator_action" \
    "a land whose rebase loses the lock once, then clears, LANDS — no operator action"
py_claim "${T}::test_e2e_lock_that_never_clears_terminates_naming_contention" \
    "a lock that never clears still terminates, naming contention and not a conflict"
py_claim "${T}::test_e2e_real_conflict_is_unaffected" \
    "a REAL conflict is untouched: no retry, no backoff, still the conflict report"
py_claim "${T}::test_rebase_checks_contention_before_building_a_conflict_report" \
    "the check precedes classification — a rebase that never started has no conflict"
py_claim "${T}::test_every_git_call_in_the_loop_is_guarded" \
    "all seven git calls inside the retry loop are guarded, not only Step 4's rebase"
py_claim "${T}::test_guard_propagates_contention_rather_than_classifying_it" \
    "Step 3.8's guard propagates contention rather than calling the worktree modified"
py_claim "${T}::test_ff_merge_race_is_not_contention" \
    "the two predicates stay disjoint — a diverged branch is not a busy repository"
py_claim "${T}::test_waiting_is_the_recovery_never_removing_the_lock" \
    "nothing in worktree_cmd removes a lock file"

# ---------------------------------------------------------------------------
section "C. The condition is real: live git, a real worktree, a real held lock"
# ---------------------------------------------------------------------------
# Everything above matches TEXT. This section makes git produce that text, so
# the predicate is checked against git's own words rather than against a string
# this suite typed out. Without it, a git that reworded the message would leave
# every assertion above green and the bug back in production.

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

lock_stderr() { # make a real rebase fail on a real held index.lock; echo stderr
    local r="${TMP}/repro"
    rm -rf "${r}"; mkdir -p "${r}"
    git init -q -b main "${r}/main" >/dev/null 2>&1 || return 1
    git -C "${r}/main" config user.email t@t.t
    git -C "${r}/main" config user.name t
    git -C "${r}/main" config commit.gpgsign false
    echo a > "${r}/main/a.txt"
    git -C "${r}/main" add -A && git -C "${r}/main" commit -qm base
    git -C "${r}/main" worktree add -q -b feat "${r}/wt" >/dev/null 2>&1 || return 1
    echo b > "${r}/main/b.txt"
    git -C "${r}/main" add -A && git -C "${r}/main" commit -qm "main moves"
    echo c > "${r}/wt/c.txt"
    git -C "${r}/wt" add -A && git -C "${r}/wt" commit -qm "branch work"
    # Hold the lock exactly where a live `git status` in that worktree would.
    : > "$(git -C "${r}/wt" rev-parse --absolute-git-dir)/index.lock"
    git -C "${r}/wt" rebase main 2>&1
}

LIVE="$(lock_stderr)" || true
[[ -n "${LIVE}" ]] || setup_error "could not provoke a real lock failure from git"

assert_contains "git itself refuses to create the worktree's index.lock" \
    "index.lock" "${LIVE}"
assert_contains "and the symptom the operator reported is the one git prints" \
    "could not detach HEAD" "${LIVE}"

# Feed git's OWN text to the predicates.
verdict() { # verdict <python-expression-on-text>
    LIVE="${LIVE}" uv run python -c "
import os
from endless.worktree_cmd import _is_lock_contention, _is_retryable_ff_merge_error
t = os.environ['LIVE']
print($1)
" 2>/dev/null
}

assert_eq "the contention predicate matches the text git actually produced" \
    "True" "$(verdict '_is_lock_contention(t)')"
assert_eq "the ff-merge predicate does NOT — a busy repository is not a diverged one" \
    "False" "$(verdict '_is_retryable_ff_merge_error(t)')"

# ---------------------------------------------------------------------------
section "D. The exhaustion message sends the reader somewhere true"
# ---------------------------------------------------------------------------
# The operator's whole cost in E-2174 was the diagnosis: "could not detach HEAD"
# reads like repository damage, so the report on a lock that never clears has to
# say what it actually is, and has to say do not delete the lock.

MSG="$(uv run python - <<'PY' 2>/dev/null
import inspect
from endless import worktree_cmd
src = inspect.getsource(worktree_cmd.land_worktree)
print(src[src.index("if last_was_contention:"):])
PY
)"

assert_contains "exhaustion names the lock, not a conflict" "index lock" "${MSG}"
assert_contains "and says so in as many words" "NOT a conflict" "${MSG}"
assert_contains "and names the usual holders, so the reader stops hunting" \
    "session monitor" "${MSG}"
assert_contains "and forbids the recovery that corrupts an index" \
    "Do NOT delete the lock file" "${MSG}"

summary
