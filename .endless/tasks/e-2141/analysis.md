## The eight commands

Verified against the CLI, not estimated. Of 30 commands exposing an output flag,
16 have `--json` and no agent view. Eight of those are list-shaped — arrays of
uniform rows — and are this brainstorm's subject:

`session status`, `session list`, `session search`, `session history`,
`project status`, `worktree list`, `verb list`, `phrase list`

The detail-shaped ones among the 16 go to E-2136; three action-result commands
(`session order`, `task import`, `task next revise`) are not readers and are
unclaimed.

## Why TOON, and why it is not settled

TOON's win is structural for this shape: declare the field names once as a
header, then one compact row per record, instead of repeating every key on every
row. That is a real reduction under ANY tokenizer, which is what distinguishes it
from the detail case where the saving is small enough that Claude's tokenizer
could flip it.

Open, and the reason this is a brainstorm rather than a todo:

1. **Tokenizer.** Published TOON savings are benchmarked on GPT-style
   tokenizers. The consumer is Claude. Measure before treating the saving as
   established — the answer could be that a plainer row format wins.
2. **Whether one list format fits all eight.** `session history` is a
   conversation transcript; `worktree list` is paths and states; `session status`
   is a board with derived columns. Uniform rows may not describe all of them.
3. **Interaction with the Agent Folio Format.** A list may appear as a part
   inside a folio stream rather than as standalone output, in which case the
   question is what `Type` that part declares, not what the command prints.
4. **Truncation.** The human views truncate to terminal width. Agent views must
   not — a cut title costs a second call to recover. Confirm nothing in the list
   path truncates.

## The requirement that must not be lost

`session status` encodes **who owns each row in a glyph and nowhere else**:
`classify()` tests `IsFocal` before `InFlight`, so a row renders as the focal
marker when it is this session's own task and as the in-flight marker when
another session is working it. The JSON carries it structurally as `is_focal`,
`in_flight` and `relation`; any agent view must carry an explicit equivalent.

This is not theoretical. A `/whats-left` report in session ES-1196 told Mike to
confirm E-2128 and to spawn E-1881, both of which belonged to another session,
because the agent read the board and could not tell whose work was whose. The
immediate cause was patched in the whats-left command's own rules, so this is
deferred knowingly rather than urgently — but an agent view of `session status`
without an ownership field would reintroduce it.

## Why this is deferred rather than in scope now

It is the long half of the agent-facing output work. The short half — the folio
container, agent-view detail output, caller detection — fixes the observed
failure of sessions skipping analyses, and lands as E-2136. The shape of a list
part will be much clearer once the container exists and one real emitter has
been written against it. Filing a spec for it now would be guessing.
