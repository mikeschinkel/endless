# Add a content-identity precondition to canAmend

Implements ED-1553. Root cause, evidence and rejected alternatives are in
E-1943's outcome. The reproduction harness is in this task's analysis.

## What to change

`canAmend` in `internal/events/commit.go` currently refuses to amend on three
preconditions. Add a fourth, checked only when the tip subject is
`LedgerCommitSubject`:

> Refuse to amend if any `refs/heads/task/*` tip holds a `.endless/db-ledger`
> tree byte-identical to the one at HEAD.

Rationale: a tree hash is content-addressed, so it is unchanged by any history
rewrite. `git pull --rebase` (or interactive rebase, or a hand `commit --amend`
on main) reassigns every local SHA, which makes precondition 2's
`for-each-ref --contains HEAD` test answer a question the rewrite made
meaningless. The content test asks the same question — "does a task branch
depend on the commit I am about to amend?" — in terms a rewrite cannot
invalidate.

## What NOT to change

Precondition 2 stays. The two are OR'd; either refusing means append. The
content test scans only `refs/heads/task/*`, so it is structurally blind to a
ledger commit already pushed to `origin/main` or carrying a tag — exactly the
cases precondition 2 gets right. Do not replace it. The verify script asserts
both survive.

## Implementation notes

Use a single batched process, not one `rev-parse` per branch:

    git for-each-ref --format='%(refname:short):.endless/db-ledger' refs/heads/task \
      | git cat-file --batch-check

Compare each resulting tree OID against `HEAD:.endless/db-ledger`. Measured on
the endless repo (145 task branches, 130 worktrees) at 16ms for the batched
form vs ~1s for 146 spawns. A `git patch-id` scan answers the same question
but costs 71s per ledger event and is struck — its cost scales with the size
of the rewrite it is looking for, so it is slowest exactly when it matters.

Follow existing house error handling in the file: a subprocess failure is an
error; a "no" answer to the precondition returns `(false, nil)`. Treat a
missing `.endless/db-ledger` tree at HEAD (empty OID) as "no match, do not
suppress" rather than an error.

Mirror the existing comment style in `canAmend` — the surrounding
preconditions each carry a short rationale comment, and this one needs to say
why a tree hash rather than a SHA.

## Verification

Two layers.

**1. Go unit tests** in `internal/events`, following the existing
`commit_test.go` fixtures — cover `canAmend`'s decision directly, including
the empty-ledger-tree and missing-branch edge cases.

**2. `tests/tasks/e-1955-verify.sh`**, following the house convention (see
`tests/tasks/e-1648-verify.sh` as the reference shape):

- Run from anywhere inside the worktree: `esu && ./tests/tasks/e-1955-verify.sh`
- Isolation: throwaway git repo as project root under a temp dir, temp
  `XDG_CONFIG_HOME` and `XDG_CACHE_HOME` so no real DB/ledger/cache is
  touched. Exercise the *candidate* binary by prepending `<worktree>/bin/` to
  `PATH` so the Python CLI's event bridge resolves `endless-go` there.
- Output pass/fail per check then a summary; exit 0 all-passed, 1 any
  failure, 2 setup problem.

It must drive real endless ledger writes — not a re-implementation of the
guard. A simulation proves the idea, which the harness in the analysis already
did; the script has to prove the shipped code.

Checks it must make:

1. **Baseline amend.** Fresh repo, no task branch, several ledger events →
   main's ledger commit count stays at 1 (amend is working; flood suppression
   intact).
2. **Reachability refusal still fires.** With a task branch sitting on main's
   ledger tip, the next event appends. Assert main gained a commit.
3. **The regression.** Rewrite main's history (advance a remote, then
   `git pull --rebase`), then record a ledger event. It must **append**.
   Before the fix it amends. Then `git rebase main` in the task worktree must
   exit 0 — this is the conflict the whole task exists to prevent.
4. **Self-release.** After that one append, record 5 more events: main's
   ledger commit count must stay flat (amending resumed), and the land must
   still be clean. This is the check that proves the fix did not cost the
   flood suppression.
5. **No false suppression.** Repeat check 3 with `pull.rebase=false`
   (merge-style pull). Behaviour must be identical to today.
6. **Precondition 2 retained.** A ledger tip reachable from a tag, and one
   reachable from a remote-tracking ref, must each still refuse to amend —
   neither is a `refs/heads/task/*` branch, so only the old test can catch
   them. This is the regression guard against someone later "simplifying" the
   two tests into one.
