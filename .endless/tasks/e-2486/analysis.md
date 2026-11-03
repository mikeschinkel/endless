## Decisions made with Mike while planning

- General, not task status: Mike asked for correcting the state of things the ledger tracks. An earlier wording of this task narrowed that to task status.
- Correction entries are separate from change entries and point at them by byte offset (Mike's option), rather than holding after values. Replay can ignore corrections, audits can read only corrections, and one correction can cover several changes. The before values stay on the correction because nothing else keeps them.
- The change entry is always `ledger.row_changed`, plus `like_kind` where an existing kind fits (Mike's option). Using existing kinds directly would bring their effects, and `task.fields_updated` enforces the transition table even when gates are skipped.
- Columns come from a list per entity type, not any column. A wrong value in an unlisted column waits for a release.
- Update, insert and delete are all supported.
- Gates are on by default with a bypass flag, per Mike's suggestion.
- A correction with no change entries is an annotation.
- `ts` is not a key: seven values repeat in Endless's ledger, from a backfill that stamped commit dates, and nothing prevents a repeat because each process starts a fresh clock. Byte offset needs a lock, which a hash chain would need anyway. A later check and repair command can fix any offset a hand edit breaks.
- Corrections target every table the ledger writes, not entity types. The first version targeted entity types, which left task_plan_items uncorrectable, and that table was part of the deadlock that prompted this task.

## Rejected

- A correction holding after values and writing the row itself: replay could not ignore corrections.
- An existing kind plus a reason: many entity types have no update kind.
- Reversing a named earlier event: a wrong state often has no single event behind it, and events with derived effects have no defined reverse.

## Costs accepted

An older `endless-go` fails with `events: unknown kind` on a ledger holding either new kind. Code that reads a specific kind doesn't see corrected changes unless it also reads `like_kind`. A later migration that renames a listed column needs the projector to map the old name for replay.
