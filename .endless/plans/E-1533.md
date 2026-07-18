# Plan — endless decision update

Add an `update` verb to `endless decision`, mirroring the existing `add`/`accept`/`reject`/`link`
sibling verbs.

## Contract

- `endless decision update <id>` with at least `--title` and `--description` (and their
  `--description-file` loader if the sibling `add` has one, for parity).
- Edits the existing decision in place — no new ID, no rejected row (the whole point: it replaces
  the reject+re-add workaround that leaves misleading rejected entries).

## Implementation notes

- **Rewrite the `.md` mirror.** The DB is the source of truth; `.endless/decisions/<id>.md` is a
  one-way mirror written at `add` time. `update` MUST re-emit that file from the new DB content,
  or the mirror silently desyncs. (Confirmed empirically 2026-07-18: editing the mirror alone does
  not change what `decision show` returns.)
- **Editable in any status.** A decision's title/description is metadata; allow correcting it
  whether `proposed`, `accepted`, or `rejected` (wording fixes shouldn't require a status dance).
  Default to permissive unless a reason to guard surfaces.
- Route the description through the same inline-content path gate as `add` (subject to E-1794's fix
  for relative-token false positives).

## Recurrence context

The reject+re-add pain this closes recurred 2026-07-18 while filing E-1789's decisions
(ED-1535/1536/1537 carry mangled notation that can't be corrected in place until this lands).
