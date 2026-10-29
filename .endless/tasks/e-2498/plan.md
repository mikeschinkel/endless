# Name the event in each ledger commit's subject

## Problem

Every ledger auto-commit is titled `Endless: record ledger entry`. `git log` on
any project Endless manages is a wall of identical subjects, and nothing in one
says which event it holds.

## Change

Title each ledger commit by its event: the kind and the entity, within 60
characters, e.g. `Endless: task.status_changed E-2421`, or
`Endless: shipped_file.written .endless/tasks/CLAUDE.md`, shortened to fit.

## The coupling that makes this more than a string change

The subject is the key the amend logic matches on:

- Go `events.LedgerCommitSubject` — `canAmend` amends only when HEAD's subject
  equals the subject being committed, so successive ledger commits fold into one.
- Python `worktree_cmd.AMENDABLE_COMMIT_SUBJECTS`, and anything else that
  recognises a ledger commit by its exact subject.

With unique subjects, exact-match amending stops folding ledger commits
together. Decide whether that is wanted: one commit per event, or fold by the
`Endless: ` prefix plus "touches only db-ledger". Then move every
exact-subject reader to a predicate (prefix plus paths).

## Verify

- A ledger commit's subject names its kind and entity, within 60 characters.
- Whatever folding is chosen, two consecutive events produce the expected
  number of commits.
- Land, verify and the worktree checks still recognise ledger commits.
