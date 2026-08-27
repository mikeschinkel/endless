# Finding: the command already exists — `endless task handoff <id>`

No new command is needed. `handoff` is already a registered subcommand of
`endless task`, described in its own help as "Render the spawn handoff for a
task (the text spawn pastes)."

## What it renders

Running it against a task prints the same handoff body `endless task spawn`
pastes into a freshly opened pane: the worktree path and branch, the sandbox
routing rule and when `--db main` is required, the deliverable and end-state
appropriate to the task's type, the numbered startup steps, the discovery-loop
rules for findings made along the way, the worktree-removal prohibition, and
the final-message contract.

## Where it is documented

`docs/guide/orchestration.md` and `docs/guide/tasks.md` both show the
invocation. `docs/guide/index.md` maps `task handoff` to the orchestration
section, glossed as "The generated handoff text for a spawned session."

## A second path, found by claiming this task

Claiming a task into an already-running session emits a handoff of its own. A
PostToolUse hook on the claim recognized the case explicitly — "You just
claimed into an already-running session; you were not spawned for it, so none
of the type handoff a spawned session is born with has reached you" — and then
rendered that handoff inline, adapted for the situation: it leads with a
relocate-first instruction to `/cd` into the worktree, which the spawn text
omits because a spawned session is already there.

## Answer to the ask

The premise that no such command exists does not hold. The handoff-less case is
covered on two paths already: on demand via `endless task handoff <id>`, and
automatically at claim time via the PostToolUse hook. A running session that
never got a handoff can be told to run the command and will receive the
identical orientation text a spawned session is born with. Verified by
rendering the handoff for E-1732, whose live pane is exactly the handoff-less
case that prompted the question.

## Gaps — reported, deliberately not filed (ED-1550)

Nice-to-haves, recorded here as evidence rather than as backlog.

1. **No session-scoped default.** `ITEM_ID` is a required argument; bare
   `endless task handoff` exits with a missing-argument error instead of
   falling back to the current session's claimed task. A session must first
   learn its own id via `endless task id`.

2. **Spawn-time, not resume-time.** The rendering asserts that the reader is
   already claimed and in the worktree and should just do the work. It carries
   no awareness of commits already on the branch, of prior sessions' recorded
   outcomes, or of what remains. It orients a cold start; it does not rehydrate
   a session mid-flight. This is the one genuinely uncovered case: a session
   already claimed and running that wants its bearings again later, without
   re-claiming.

3. **Discoverability, narrowed.** The claim hook covers the moment of claiming,
   so the original worry is smaller than it looked. What remains uncovered is
   the session that is already claimed and running and has since lost the text
   — nothing points it back at the command, and it is not surfaced at
   SessionStart.
