# Plan: remove `endless phrase` and the pattern-matcher config it edits

Decided by Mike, 2026-09-26. The pattern matchers were groundwork for E-502
("Inject context via regex patterns and user keywords"), now obsolete: the
use-cases it targeted are handled by more explicit mechanisms that do not rely
on hooks matching text, which produce false positives. Pre-beta, so no
migration: existing machine configs keep whatever entries they have, and
nothing reads them.

## What is dead, and why

- The only reader of the `matchers` config in the whole codebase is the Claude
  hook's action lookup (claim / confirm / chat) in `handlePostToolUseSession`.
  E-2177 removes the claim and confirm lookups; E-2179 removes chat. After both,
  nothing reads `matchers`.
- The `pivot` phrases seeded into machine configs (`wait`, `btw`, `actually`,
  `PIVOT`, …) were never read by anything.
- Python's `get_action_regex` has no callers today.
- `endless phrase add|remove|enable|disable|list` edits config nothing reads.

## Ownership split — read before editing

- **E-2177** deletes the Go `internal/matchers` package, because removing the
  hook's lookups orphans it. This task does NOT touch Go.
- **E-2179** owns every chat-related line.
- **This task** owns the Python pattern-matcher half and `endless phrase`.

There is no ordering dependency. If this lands first, a fresh install simply
never seeds the claim/confirm patterns, which disables the hook's text inference
early on that machine — the outcome E-2177 is heading for anyway.

## Work

**Keep everything the VERB functions use.** `src/endless/matchers.py` hosts two
unrelated things: verbs (`get_verbs`, `load_all_verbs`, `add_verb`,
`update_verb`, `remove_verb`, `verb_categories`, `project_verbs_path`, and the
path-layer helpers they rely on such as `project_config_path`) and pattern
matchers. Remove only the latter. Trace each helper's callers before deleting it;
a shared path helper stays.

Remove:
- `src/endless/phrase_cmd.py`, and the `phrase` group in `src/endless/cli.py`.
- In `src/endless/matchers.py`: `DEFAULT_MATCHERS`, `_STALE_DEFAULTS`,
  `_ENSURED_DEFAULTS`, `_migrate_stale_defaults`, the default-seeding in
  `load_all_matchers` (and `load_all_matchers` itself once `endless phrase` is
  gone, if nothing else calls it), `add_match_value`, `remove_match_value`,
  `set_enabled`, `get_action_regex`. Update the module docstring, which still
  describes the matcher half.
- `src/endless/set_cmd.py`: `matchers` in the list of known project config
  fields. Remove it; `tests/test_project_set_fields_help.py` parametrizes over it,
  so drop that case.
- The `matchers` key in this project's own `.endless/config.json`.

Tests:
- `tests/test_matchers.py` — delete the pattern-matcher tests; keep any verb
  coverage (move it to a verb test file if the file would otherwise be deleted).
- `tests/test_project_config_path.py` — its `add_match_value` case goes; keep
  the `project_config_path` cases if that helper survives.
- `tests/test_verb_gate.py`, `tests/test_agent_help.py` — adjust any reference
  to `endless phrase` or matcher seeding.

Docs: any guide section documenting `endless phrase`. Run `just guide-index` if
a heading changes, then `just guide-check`. The guide-coverage report currently
lists `endless phrase` as an uncovered command; it should drop off that list.

Tell E-1063 (the Python→Go port umbrella): its description lists `phrase` among
the surfaces to port. With this landed there is nothing to port.

## Verification

- `endless phrase` exits as an unknown command.
- `grep -rn "phrase_cmd\|add_match_value\|remove_match_value\|get_action_regex\|DEFAULT_MATCHERS\|_migrate_stale_defaults"`
  over `src/`, `tests/` and `docs/guide/` returns nothing.
- Verb registration still works end to end: `endless task add` with a new title
  verb still auto-registers it, and `endless verb list` still reads project and
  machine layers.
- `just build`, `just test`, `just test-go`, `just guide-check` green.
