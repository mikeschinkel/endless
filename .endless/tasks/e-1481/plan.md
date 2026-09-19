# E-894 Phase 1 — Go read subcommands for task-display queries

Umbrella context: `endless task show E-894 --text`.

## Goal

A working, tested Go read surface: `endless-event` subcommands returning
task-display data as JSON. **No Python changes in this phase** — Python keeps
reading SQLite until Phase 2 cuts over.

## First deliverable: the inventory, derived not inherited

Do not work from a list in this plan. Derive it, and record what you found in
the task's analysis before writing code:

1. Every Python read that feeds a task-display command. Find them; do not trust
   any enumeration, including this one.
2. For each, the exact field set its formatters consume — across the default
   view, `--json` and `--llm`, which do not agree with each other.

The field set is the part that silently rots. Two examples that already caught
a stale list: a task's supersessions (`replaced_by`, E-1956) and its duplicates
(`duplicates`, E-1185) both render inline beside a terminal status and both
postdate the original inventory. Assume there are others by the time you read
this. The inventory you derive is the contract for Phase 2 — if a field is
missing here, Phase 2 cuts over and silently deletes it from every surface.

## Invariants

- **Reads go through the live-task view, not the base table.** Removed tasks are
  retained rows; reading the base table resurfaces them everywhere. The schema
  documents which surface is which, and why id-allocation must never use the
  view.
- `task show` is the deliberate exception: it renders a removed task, marked. So
  the single-task read and the list reads do not share a source.
- **Every field the current formatters read must appear in the JSON.** This is
  the whole point of the phase; a missing field is not a cosmetic gap.
- Output shape: single-row → object or null; list → array or empty array. Keys
  that can be empty are always present, never omitted — an absent key must never
  have to be read as a negative fact.
- Display rules stay in Python. The subcommands return raw values; anything
  gated on status, terminal-ness, or verbosity is the formatter's business.
- DB context threading must match the existing write bridge, or reads bypass the
  worktree DB gate. Whatever that bridge does, mirror it — do not reinvent it.
- Filters must cover what the Click commands already expose. Derive that set the
  same way as the field set.

## Style

Raw stdlib flag parsing and the existing dispatch shape — this is an internal
bridge binary, not a user-facing CLI. New Go code follows the house rules
(ClearPath, doterr, go-dt for paths, go-sqlparams for dynamic clauses).

Put the read helpers alongside the existing Go task reads. Where Go already has
an equivalent query, share it rather than writing a second spelling.

## Verification

- Table-driven Go tests per helper against a fixture DB.
- For each subcommand, the JSON contains every field the derived inventory named.
- A removed task is absent from list reads and present-but-marked in the
  single-task read.
- Exit codes: 0 on success or empty, 1 on DB error, 2 on a missing required flag.
- DB validation still passes; `just build` clean.
