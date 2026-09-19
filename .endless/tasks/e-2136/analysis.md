## Why this is separate from E-1504, and blocks it

E-1504 sweeps 31 commands to offer an agent-facing text view. If the format is
not settled first, those 31 renderings get written twice — once ad hoc, once in
TOON. The format is the cheap thing to get right early and the expensive thing
to change late, so it lands first and E-1504 writes against it.

Discussed in session 539 alongside E-1504 ("two tasks, peers"); E-1504 was filed
and this one never was.

## Only the Python implementation is on the path

The agent-facing CLI is Python. Go owns database access and its JSON output —
`endless-go session-query worktree-unsettled`, `session-status --json` — is
internal plumbing consumed by Python, not by an agent reading output. So the
stable Python implementation is all this needs, and TOON's in-development Go
implementation is NOT a blocker for any agent-facing surface.

If Go ever emits agent-facing output directly, that is its own task and can use
whichever Go implementation is mature by then.

## Where TOON actually wins, and where it does not

Sorting the 31 commands by output shape splits them cleanly, and the split
determines the rules:

- **List-shaped** — `task list`, `task search`, `task next`, `task recent`,
  `task active`, `task landed`, `task unsettled`, `decision list`, `epic list`,
  `session status`, `session list`, `session search`, `session history`,
  `worktree list`, `verb list`, `phrase list`, `project status`. Arrays of
  uniform objects: TOON declares the fields once and emits one row per record
  instead of repeating every key on every row. This is the whole reason to adopt
  it.
- **Detail-shaped** — `task show`, `decision show`, `epic show`, `session show`,
  `worktree show`, `worktree current`, `worktree diagnose`. One object dominated
  by long free-text fields (description, analysis, plan). TOON's tabular saving
  does not apply; the win here is small and the format must simply not make
  prose worse.

## What the plan has to settle

1. **Dependency or in-house emitter.** Endless has three Python runtime
   dependencies today (click, inquirerpy, tabulate). Adding a fourth for TOON is
   a real posture change; writing a small emitter avoids it but owns the format's
   edge cases. Decide deliberately, not by reflex.
2. **How prose fields render.** A task description is multi-line text inside a
   record. Whatever TOON does with embedded newlines has to be legible and
   unambiguous when a title or description contains delimiters.
3. **Empty and default fields.** The `session status` JSON emits
   `blocked_by_n: 0`, `replaced_by: []`, `duplicates: []`, `hidden: false` on
   every row. Omitting defaults is the single largest saving across 31 commands,
   but in a tabular format the field list is declared once, so "omit" means
   something different than it does per-record. Resolve this explicitly.
4. **Shared key vocabulary.** `status`, `phase`, `owner` should mean the same
   thing in every command's output so the vocabulary is learned once.
5. **No truncation.** The human view cuts titles at terminal width. For an agent
   that is unrecoverable loss costing a second call, so agent output carries full
   values regardless of length.
6. **Glyph-encoded signals become explicit fields.** `session status` encodes
   ownership as `●` versus `⟳` and nothing else; an agent view without an
   explicit ownership field cannot tell this session's work from another
   session's. That failure is recorded on E-1504.

## Not in scope

Auto-selecting agent output when an agent invokes a command, plus the
counterpart flag that forces the human view. Its own task, after E-1504.
