# Plan — the land survives the recorder committing main's managed files first

## The step this is about

Before it rebases, `worktree land` reads `git status` on the main checkout and
splits it into Endless-managed files (ledger segments under
`.endless/db-ledger/`, `.endless/verbs.jsonl`, document mirrors) and the user's
files. When there are managed files, the land stages and commits exactly those
on main ("Endless: auto-record session activity") so the rebase and ff-merge
meet a clean main. In the code this is Step 3 of `land_worktree`
(`src/endless/worktree_cmd.py`), and its refusal prints `auto-commit failed:`.
This plan calls it "the managed-file commit".

## The failure

Endless's background recorder (`internal/events/commit.go`) commits the same
managed paths on main ("Endless: record ledger entry") on its own schedule, and
shares no lock with the land. When it commits between the land's status read
and the land's commit, git has nothing left to commit: exit 1, message on
stdout, stderr empty. The land retries only on index-lock contention, so it
refuses with "git could not auto-commit main's endless-managed files" above an
empty git message. Nothing merges; a re-run normally succeeds. Seen landing
E-2158 on 2026-10-08.

The exit-1/empty-stderr shape is inferred, not observed directly — step 1
proves it before anything changes.

## Decisions (Mike, 2026-10-08)

1. When the managed-file commit fails, check `git diff --cached --quiet`. If
   nothing is staged, the recorder already committed what the land meant to:
   treat the step as done and carry on. Any other failure is classified exactly
   as today. (Not a pre-commit check — that leaves a window between check and
   commit.)
2. No shared lock between land and recorder. The recorder's commits are
   snapshots of the files the land would commit anyway, so once (1) treats the
   race as success it costs nothing.
3. This site's refusal relays git's stderr, falling back to stdout when stderr
   is empty, so it never prints "act on what git said below" above nothing.
   `relay_foreign` itself is unchanged.
4. Tested against a real throwaway repo, deterministically — not a mocked
   `_git_run`, which would assume the very failure shape step 1 must prove.

## Steps

1. **Reproduce.** In `tests/`, a real main repo with a dirty managed file; stub
   the point between the land's `git add` and `git commit` to commit that file
   the way the recorder does. Assert the unfixed land refuses with exit 1 and
   empty stderr. If it does not reproduce that shape, STOP and re-open the
   cause instead of fixing a guess.
2. **Fix (decision 1)** in Step 3 of `land_worktree`: on `CalledProcessError`
   from the commit, after the lock-contention check, run
   `git diff --cached --quiet`; exit 0 means nothing staged → proceed as if the
   commit succeeded.
3. **Relay (decision 3):** pass `e.stderr or e.stdout or e` to the relay.
4. Tests:
   - the step-1 race now lands, and main carries the recorder's commit with no
     duplicate;
   - a partial race (recorder took one of two managed files) commits the other
     and lands;
   - a real commit refusal (a failing pre-commit hook in the throwaway repo)
     still refuses, unchanged;
   - a stdout-only failure shows its stdout in the refusal.

## Verify

`.endless/tasks/e-2275/verify.sh`, folding in the new tests as a fail-fast check
and re-running the step-1 reproduction as the proof the race now lands.
