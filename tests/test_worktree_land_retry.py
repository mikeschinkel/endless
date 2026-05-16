"""Tests for E-1351: land retry predicate covers diverging-branches.

_is_retryable_ff_merge_error gates Step 5's retry loop in
land_worktree(). The loop must catch both worktree-side races
(uncommitted/would be overwritten) and main-side races (diverging
branches / not possible to fast-forward, E-1351). Hard errors
unrelated to either race must fall through to ClickException.
"""

import pytest

from endless.worktree_cmd import _is_retryable_ff_merge_error


@pytest.mark.parametrize(
    "err_text",
    [
        "fatal: would be overwritten by merge",
        "error: Your local changes to the following files would be overwritten by merge",
        "fatal: Updating the following directories would lose uncommitted changes",
        "uncommitted changes",
        "UNCOMMITTED CHANGES IN AUTO FILES",
    ],
)
def test_worktree_side_race_is_retryable(err_text):
    assert _is_retryable_ff_merge_error(err_text)


@pytest.mark.parametrize(
    "err_text",
    [
        "hint: Diverging branches can't be fast-forwarded",
        "fatal: Not possible to fast-forward, aborting.",
        # Real text from E-1347 land failure 2026-05-15:
        "hint: Diverging branches can't be fast-forwarded, you need to either:\n"
        "hint:\n"
        "hint:     git merge --no-ff\n"
        "fatal: Not possible to fast-forward, aborting.\n",
    ],
)
def test_main_side_race_is_retryable(err_text):
    assert _is_retryable_ff_merge_error(err_text)


@pytest.mark.parametrize(
    "err_text",
    [
        "fatal: refusing to merge unrelated histories",
        "error: pathspec 'foo' did not match any file(s) known to git",
        "fatal: bad object HEAD",
        "",
        "fatal: not a git repository",
    ],
)
def test_unrelated_errors_are_not_retryable(err_text):
    assert not _is_retryable_ff_merge_error(err_text)
