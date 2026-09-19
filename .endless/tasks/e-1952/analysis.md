`task report` models a report as a one-shot render with no partial success and
no persisted state, so any rejected or absent field costs a full extra
invocation. One task rather than four: the symptoms below share that cause.

Evidence — one turn of an E-1939 session took three calls to emit one block:

1. ALL-OR-NOTHING VALIDATION. A payload of {verify, notes} had its note bounced
   by the ceremony check, and the command emitted NO block at all — the valid
   `verify` field was discarded alongside the rejected note. Rendering the block
   from the accepted fields, plus a rejection notice naming what was dropped and
   why, would have made this the last call instead of the middle one.

   The check's judgment was CORRECT: the note recounted a rebase and its
   conflict resolution, which a reviewer can read off the commits. The defect is
   the blast radius of a rejection, not the rejection itself.

2. STATELESSNESS IS UNDISCOVERABLE. The command is addressed by task id, takes a
   structured payload, and sits beside `task update` / `task submit`, which
   persist. It does not persist — a payload supplied in one call is gone by the
   next. The session spent a call running it bare to find that out. Either
   persist the payload on the task, or make the one-shot nature evident in the
   help text and the `--json` option description.

3. AN EMPTY BLOCK AT `unverified` IS NEAR-CERTAINLY WRONG. Run bare on a task
   just flipped to `unverified`, it prints `Nothing to report.` — for a task
   whose handoff mandates a verify command. It cannot know that command, but it
   does know the status and that no `verify` entry was passed, so it can say so
   rather than assert a null result the handoff forbids.

4. THE PostToolUse HOOK FIRES ON `--help`. Running `task report --help` to look
   up the `--json` shape injects the full "now append the block, unchanged,
   after the separator" instruction when no block was rendered. The hook matches
   the command name rather than a successful render.

Deliberately out of scope: the ceremony check's judgment criteria. They are
working, and loosening them to reduce round-trips would trade a real quality
gate for a cheap one.
