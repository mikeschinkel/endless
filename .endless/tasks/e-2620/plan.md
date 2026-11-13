# E-2620 plan: commit lefthook.yml before init's other commits

`register_project` in `src/endless/register.py` runs `_place_suite_rules`
(whose `shipped_file.written` ledger event makes init's first commit),
then the guide pointer and output style, then `_setup_git_hooks`, then
`_commit_bring_up`, which is the first commit to include `lefthook.yml`.

Change the order:

1. Run `_setup_git_hooks` before `_place_suite_rules`.
2. Right after it, commit the lefthook config files this run created or
   changed, as their own commit with the subject
   `Endless: set up lefthook for the Endless-Task trailer`. Use the same
   rule `_commit_bring_up` applies to USER_OWNED files: a file the user had
   uncommitted changes in before the run is left out and named. Factor
   `_commit_bring_up` so both commits share that rule: it takes the
   candidate paths and the subject.
3. The rest of init runs as today. `_commit_bring_up` no longer finds
   `lefthook.yml` changed, because it is already committed.

When the commit in step 2 is skipped (nothing changed, the project is not
at its repository's root, or the user's own changes are in the file), init
behaves as it does today.

## Tests

In `tests/test_register.py`: run project init in a fresh repo with lefthook
chosen and check that the first commit init makes contains `lefthook.yml`.
Then rebase init's commits onto an unrelated root commit, with a
`prepare-commit-msg` hook that records whether `lefthook.yml` exists, and
check that it existed for every replayed commit. The test does not need
lefthook installed.
