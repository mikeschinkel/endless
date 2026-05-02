# E-1117: Redesign verb storage — top-level verbs array, decoupled from matchers

## Background

E-1106 introduced `--definition` on `endless phrase add verb` (good: forces
semantic justification). E-1108 backfilled definitions for the existing 79
verbs. Both stored definitions as a sibling `definitions: {value: def}` map on
the verb-type matcher object.

That coupled verb-specific content into the generic matcher schema. The
schema started saying "any matcher might have definitions" when only verbs
ever do — pivots, regex command-patterns, channel matchers carry vestigial
nulls. Discussion in this session converged on a cleaner shape.

## Considered and rejected

**Option C — uniform per-value `meta` array on every matcher's `match`
list:**

```json
"match": [{"value": "broaden", "meta": [{"name": "definition", "value": "..."}]}]
```

Restores schema purity at the cost of CLI ergonomics (`endless phrase add
broaden --type verb --meta "definition:..."`), validation strength (definition
is a stringly-typed bag entry, not a typed field), and discoverability (domain
knowledge that verbs have definitions is hidden). Bad trade for verbs given
how load-bearing definitions are.

**Option B (original) — top-level verbs as a map keyed by value:**

```json
"verbs": {"broaden": {"definition": "..."}}
```

Map-keyed-by-value is a Go pain (lookup easy; ordering nondeterministic;
adding fields like `added_at` later is more verbose to migrate than slice
handling). Rejected in favor of array-of-objects.

## Chosen design

```json
{
  "verbs": [
    {"value": "broaden", "definition": "to widen scope or coverage"},
    {"value": "consider", "definition": "to deliberate over"}
  ],
  "matchers": [
    {"type": "pivot", "method": "substring", "match": ["actually", "wait"]},
    {"type": "start", "scope": "task", "method": "regex", "match": "..."}
  ]
}
```

- Verbs are a top-level `verbs` array of objects with `value` and `definition`
  required, future fields optional.
- Matchers no longer contain `type=verb` entries. Stay generic for pivots,
  regex command-patterns, channel matchers.
- The match engine (exact / substring / regex detection) stays a pure shared
  function. Verb-gate calls it with patterns drawn from `verbs[*].value`;
  phrase consumers call it with patterns from `matchers`. Same engine,
  different data sources.

## Why bespoke for verbs is justified

Verbs have rich domain content (definitions today; aliases, examples,
synonyms plausible later) that is load-bearing for verb-gate correctness.
Other matcher types currently do not. If pivots later grow rich metadata,
extract them the same way — pattern-following, not novel design. YAGNI now.

## CLI surface

```
endless verb add <value> --definition "<def>"
endless verb list [--json]
endless verb remove <value>
```

`endless phrase add verb` is removed (no compat shim — endless is "paving
cowpaths"). Future commands like `endless verb show <value>` or `--alias`
land naturally.

## Implementation

### Files

- New: `src/endless/verb_cmd.py` — CRUD for the `verbs` array.
- New: `src/endless/cli.py` — `verb` command group; remove the `verb` special
  case in the existing `phrase add` handler.
- Modified: `src/endless/matchers.py` — `get_verbs()` reads from `verbs`
  top-level array. Drop `type=verb` from `add_match_value`. One-time
  migration helper that runs at config load: extract any existing
  `type=verb` matchers into `verbs`, drop the `definitions` map.
- Modified: `src/endless/phrase_cmd.py` — remove `definition` parameter and
  the verb-required-definition check (verb code path is gone from phrase).
- Modified: `src/endless/task_cmd.py` — `validate_title` is unchanged in
  shape (still calls `matchers.get_verbs()`); only the storage backend
  underneath changed.

### Tests

- `tests/test_verb_cmd.py` — add/list/remove, definition required, persists
  in `verbs` array, layered config (project + machine).
- `tests/test_verb_gate.py` — update existing tests to reflect new storage
  (verbs come from top-level array, not matcher list).
- Migration test: a config with old-shape `type=verb` matchers (with or
  without `definitions`) is auto-migrated on first load.

### Migration

One-pass, idempotent, runs at config load:

1. If `matchers` contains any `type=verb` entries, extract their `match`
   list values into `verbs[]`. If a `definitions` map is present on the
   verb matcher, hoist values into the per-verb `definition` field.
2. Remove the verb matchers from `matchers`.
3. Save back.

Idempotent because step 1 finds nothing to do once migration is complete.

## Cleanup of prior bad work

- Revert `fb60208` (the E-1108 commit that added the bad `definitions`
  structure to project config).
- Machine config `definitions` field is removed automatically by the new
  migration on first load.

## Out of scope

- Migrating other matcher types (pivot, etc.) into bespoke top-level
  structures. Defer until they have metadata that warrants the work.
- Adding alias / example / synonym fields to verbs. Schema is forward-
  compatible (add fields as needed); no implementation today.
- Backward-compat shim that keeps `endless phrase add verb` working.
  Removing it cleanly is fine — endless is in active development.

## Verification

1. `just build` clean.
2. Full Python test suite passes.
3. `endless verb add ponder --definition "to deliberate"` writes a new
   `{value: "ponder", definition: "to deliberate"}` entry in the project
   `verbs` array.
4. `endless verb list` shows all verbs with definitions.
5. `endless task add "Ponder X"` accepts (verb-gate reads new storage).
6. `endless task add "Maybe X"` rejects with the E-1106 anti-rationalization
   error message.
7. Reading a config that still has old-shape `type=verb` matchers triggers
   migration; the file ends up in the new shape after first read.
