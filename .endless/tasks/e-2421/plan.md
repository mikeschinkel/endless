# Update an unedited .endless/tasks/CLAUDE.md on project init

Handed over from E-2349 (Mike asked its session to send it). Filed from E-2257
on Mike's go-ahead, 2026-10-22.

## Problem

`endless project init` is meant to be re-run to bring a project up to date
(E-2349's EQ-82). But `scaffold_suite_rules` never overwrites
`.endless/tasks/CLAUDE.md`, so go-cfgstore and homelab keep stale copies of
`SUITE_RULES` (src/endless/suite_rules.py). E-2419 is about to change that
text again.

## Behaviour

Init replaces the file with the current `SUITE_RULES` text only when nobody
edited it locally:

- If the file matches the hash of what init last wrote into this project
  (a project fact), replace it.
- If there's no project fact (the copy was written before this feature) and the
  file matches the hash of any shipped `SUITE_RULES` version (a release
  fact), replace it.
- Otherwise leave it, and report it as locally edited.

## Storage (from E-2257's machine/project classification)

- **Project facts:** a project-scoped table, written through the event pipeline
  (`endless-go event emit`) and rebuilt from the ledger, so it travels with
  the project to a clone. It fits the E-2215 split as decided and doesn't
  wait for it.
- **Release facts:** rows seeded from a Go constant on connect, the same
  pattern as `task_types` (ED-1506). Seeds are free in both files.
- If both share one table, it has a `kind` column with an open-ended set of
  kinds (Mike), not two hard-coded ones.
- Storage and hashing live on the Go side; everything is being ported to Go
  (Mike).

## Content

- A human-readable change list may live in the file's own content, not hidden
  in an HTML comment (Mike).

## self_dev

- In the endless repo, `.endless/tasks/CLAUDE.md` is a committed copy of
  `SUITE_RULES`. Add a test that fails when the two differ, so editing the copy
  instead of the source is caught.
- Running init from a worktree writes to the sandbox DB, not main.

## Blocked by

- E-2419, which changes the `SUITE_RULES` text.
