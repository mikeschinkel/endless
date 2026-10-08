#!/usr/bin/env bash
# ── DO NOT EDIT ─────────────────────────────────────────────────────
# This suite belongs to E-2275 and records what was true when E-2275
# landed. Edit it only if you ARE E-2275. If your change breaks an
# assertion here, leave it alone — see .endless/tasks/CLAUDE.md.
#
# E-2275: when the background recorder commits main's Endless-managed files
# between land's status read and its own commit, the land treats "nothing left
# staged" as done and lands; any other commit failure still refuses, and the
# refusal carries git's stdout when stderr is empty.
source "$(dirname "${BASH_SOURCE[0]}")/../_harness.sh"

set -u

root="$(git -C "$ENDLESS_VERIFY_DIR" rev-parse --show-toplevel)" || setup_error "not in a git checkout"
cd "$root" || setup_error "cannot cd to $root"

section "This task's own tests (fail fast)"
out="$(uv run pytest tests/test_worktree_land_ledger_race.py -v 2>&1)"
if [[ $? -ne 0 ]]; then
    report_fail "E-2275 tests pass" "exit 0" "$(tail -30 <<<"$out")"
    summary
fi

section "The reproduction: a real repo, the recorder committing mid-land"
assert_contains "git's failure is exit 1, empty stderr, 'nothing to commit' on stdout" \
    "test_race_failure_shape_is_exit_1_with_empty_stderr PASSED" "$out"
assert_contains "that race now lands, main carries the recorder's commit and no duplicate" \
    "test_full_race_lands_with_the_recorders_commit_and_no_duplicate PASSED" "$out"

section "The neighbours of the race"
assert_contains "recorder took one of two files: the land commits the other and lands" \
    "test_partial_race_commits_the_rest_and_lands PASSED" "$out"
assert_contains "a failing pre-commit hook still refuses, with git's words" \
    "test_real_commit_refusal_still_refuses PASSED" "$out"
assert_contains "a stdout-only git failure shows its stdout in the refusal" \
    "test_stdout_only_failure_shows_its_stdout PASSED" "$out"

section "Land's lock-contention handling is unchanged"
out="$(uv run pytest tests/test_worktree_land_lock_contention.py -q 2>&1)"
assert_contains "tests/test_worktree_land_lock_contention.py passes" " passed" "$out"
assert_not_contains "with no failures" "failed" "$out"

summary
