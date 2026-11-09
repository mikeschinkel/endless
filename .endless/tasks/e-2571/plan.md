Three renames, one mechanism.

## The mechanism

Add `RenamedKinds map[Kind]Kind` to `internal/events/event.go`, beside
`RetiredKinds`, mapping each retired spelling to the live one that replaced it:
`decision.deleted` to `decision.removed`, `task.deleted` to `task.removed`, and
`session_tasks.removed` to `session_tasks.deleted`.

Both dispatchers resolve through it before their switch — `Execute`'s kind
switch in `internal/events/executor.go` and the replay switch in
`internal/events/projector.go`. Resolve the kind into a local, leave `evt.Kind`
alone, and switch on the local. The explicit dual-spelling cases E-2010 added
for decisions (`case KindDecisionRemoved, KindDecisionDeleted:` in both files)
come out, since the map now carries what they carried. The retired-kind no-op
in each default branch stays as it is: with normalization ahead of the switch it
can only be reached by a retired kind that has no replacement, which is what it
was written for.

Document at `RenamedKinds` that a renamed kind belongs in BOTH maps, and why —
`RetiredKinds` is what makes it unemittable, `RenamedKinds` is what keeps replay
removing — and trim E-2010's comment at `KindDecisionDeleted`, which currently
explains the explicit cases that are being removed.

## The kinds

`KindTaskRemoved` (`task.removed`) and `KindSessionTasksDeleted`
(`session_tasks.deleted`) enter `ValidKinds`; `KindTaskDeleted` and
`KindSessionTasksRemoved` move to `RetiredKinds`. Two in, two out, so the tally
in `TestValidKinds_Count` is unchanged and gains an explanatory line.
`TestRetiredKinds`' want-list grows by both newly retired spellings.

Rename the symbols that name the kinds they handle, as E-2010 did for
decisions: `execTaskDeleted` and `replayTaskDeleted` to `…Removed`,
`TaskDeletedPayload` to `TaskRemovedPayload`, and `execSessionTasksRemoved` to
`execSessionTasksDeleted`. `SessionTaskMembershipPayload` is shared across the
membership kinds and does not move.

## The emitters

`event emit` gates on `ValidKinds` (`internal/eventcmd/event.go`), so every
Python emit site must change in the same commit or be refused at the door.
Task removal has two: `task remove` in `src/endless/task_cmd.py` and the
per-task emit in `src/endless/unregister.py`. Membership deletion has one:
`session_task_remove` in `src/endless/session_task_cmd.py`, whose `_VERB_OF`
key moves with it.

## The command

`endless session task remove` becomes `endless session task delete`, in
`cli.py`'s registration, its help body and its example, and in
`session_task_cmd.py`'s entry point and module docstring. No alias, no
deprecation path. Then the prose that names the old command: `docs/guide/
appendix-a.md` and `docs/guide/sessions.md`, and the comments in
`internal/events/session_task_membership.go`,
`internal/events/session_task_visibility.go` and `src/endless/session_cmd.py`.
`just guide-check` is the gate that the command map still resolves.

## The tests

One asserts the map's invariant: every key is in `RetiredKinds` and absent from
`ValidKinds`, every value is in `ValidKinds`. One asserts replay parity for
task removal under both spellings — the retained row and the id floor, as
E-2010's decision test does — which is the assertion that would catch a
dispatch that stopped reaching the handler. One does the same for membership
deletion under both spellings.
