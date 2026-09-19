# Why this is a real hazard, and why it is not folded into E-1906

## What replay does today

`rebuild-db` and `validate-db` both go through
`events.ProjectToTempDB` -> `ReadAllEvents` -> `replayEvent`.

- `ReadAllEvents` does a plain `json.Unmarshal` per ledger line. It never
  calls `Event.Validate()`.
- `replayEvent` is a `switch evt.Kind` over the task and decision kinds. Its
  `default:` branch is commented "Skip non-task events silently (sessions,
  notes, etc.)" and returns nil.

So every `session.*`, `note.*`, `message.*` and `conversation.*` kind lands in
`default:` and is dropped without a trace — and so does a kind that is
misspelled, emitted by a newer binary, or removed from the vocabulary. The two
cases are indistinguishable at replay time.

## Where ValidKinds actually applies

`ValidKinds` is consulted in exactly one place: `Event.Validate()`
(`internal/events/event.go`), which is called only on the four EMIT paths in
`internal/eventcmd/event.go`. It is a write-side gate, not a read-side one.

Consequence: removing a kind from the closed set cannot fail a rebuild today.
That is not because retirement is handled — it is because replay checks
nothing. The safety is accidental.

## Proposal

1. `events.RetiredKinds` — a set a removed kind moves into rather than
   vanishing. `Validate()` consults it and rejects an emit with a distinct
   "retired" message, so a stale binary still emitting one gets a diagnosis
   rather than a generic "unknown kind".
2. Split `replayEvent`'s `default:` into two branches: a known-and-
   intentionally-skipped set (silent, as today) and everything else (appended
   to `ProjectResult.Errors`, which `rebuild-db` and `validate-db` already
   print as warnings).

Step 2 is the bulk of the work: it requires enumerating every kind that is
legitimately skipped at replay, which is currently implicit in the absence of
a case arm. That enumeration is why this is a separate task.

## Relation to E-1906

E-1906 removed `KindSessionRecapped` and `SessionRecappedPayload`. That removal
is safe under today's behavior and was verified as such:

- no emitter exists anywhere in the tree — the kind was introduced by
  `f99b9ab0` (E-804) as vocabulary and never wired up;
- `grep 'session\.recapped' .endless/db-ledger/*.jsonl` returns zero hits, so
  no ledger line references it;
- replay would skip it silently even if one did.

The hazard above predates E-1906 and is independent of it.
