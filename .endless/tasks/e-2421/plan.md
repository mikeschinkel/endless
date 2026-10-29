# Update an unedited .endless/tasks/CLAUDE.md on project init

Handed over from E-2349 (Mike asked its session to send it). Filed from E-2257
on Mike's go-ahead, 2026-10-22.

## Problem

Every project Endless manages gets a rules file at `.endless/tasks/CLAUDE.md`.
It tells an agent how a task's own folder works and how to write a verify
suite. Endless writes it the first time `endless project init` runs, and never
touches it again.

Re-running `endless project init` is meant to bring a project up to date
(E-2349's EQ-82). For this file it doesn't. So when Endless changes the rules,
as E-2419 did, every existing project keeps the old ones: go-cfgstore and
homelab still teach the old suite form today.

## Behaviour

Re-running init updates the file to the rules this Endless ships, but only if
nobody has edited the project's copy:

- If the file is exactly what init last wrote into this project, replace it.
- If init has no record for this project (the copy predates this feature), and
  the file is exactly some version Endless has shipped, replace it.
- Otherwise leave it alone, and say it was edited locally and not updated.

"Exactly" is judged by a hash of the file's contents, so Endless never has to
keep the old text itself.

## Where the records live

- **What init wrote into a project** belongs to that project, so it is
  recorded in the project's own history (the ledger) and travels with a clone.
- **Which versions Endless has shipped** belongs to Endless itself, so each
  install knows them with no download or setup.
- If both are kept in one place, it can hold other kinds of shipped file later,
  not just this one (Mike).
- Hashing and storage are done in Go; everything is moving there (Mike).

## Content

- A list of what changed may go in the file itself, as text a reader sees
  (Mike).

## In the endless repo itself

- Running init from a task's worktree records into that worktree's sandbox,
  not the main database.

## Blocked by

- E-2419, which changed the rules text. It has landed.
