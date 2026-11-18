## What E-2221 reads from this marker

E-2221 (submitted, blocked by this task) keys board suppression on it. On a
board, an unspawned task's row:

1. On its planning session's board: shown, without ◫.
2. When another session is planning it: suppressed on every other board,
   the steward's included, unless that board's session touched it, where it
   shows with ◫.

So when the marker clears (on submit, or however a stale one clears), the
task comes back on the boards that suppressed it. E-2221 needs, from this
task, a way to ask "which session, if any, is planning task T".

Mike chose planning over focus for this (Q6) because focus moves on any
write: suppressing on focus would take a task off its steward's board after a
session only added a note.

## Where /needs-plan lives

`/needs-plan` is Mike's user-level command,
a `needs-plan.md` in his Claude user commands directory, not part of this repo. Running
`endless task plan start` as its first step is an edit to that file, which is
Mike's to make or approve; the repo cannot ship it. On someone else's machine
(PRODUCT), nothing runs `plan start` unless the guide tells a planning session
to, so the guide's planning workflow should say so too.
