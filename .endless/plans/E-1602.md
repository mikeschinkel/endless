# Plan — verify.toml manifest schema + discovery convention

Shared interface contract is in the epic's analysis (`endless task show E-1596 --analysis`,
section "Interface Contract / A"). This plan implements section A. Do not re-derive the schema —
conform to it.

## Deliver

1. **The `verify.toml` schema** (flat TOML): `schema`, `task`, `runner`, `format`
   (`gotest-json|pytest-json|tap`), optional `tiers`, `seed`, `needs`. Parse with a Go TOML lib
   (`BurntSushi/toml` or `pelletier/go-toml`). Define a Go struct + validation (required fields,
   known `format` values).
2. **The location/layout convention:** `.endless/tasks/<id>/verify.toml` beside suite files and
   optional `fixtures/`.
3. **Discovery:** a function that globs `.endless/tasks/*/verify.toml` and returns parsed
   manifests keyed by task id; reused by `endless verify` (E-1603).

## Notes

- Manifest is a pointer, not a spec (per the contract) — no test-description fields.
- Keep the struct flat; if a future need pushes nesting, re-evaluate TOML rather than nest.
- All-Go: this is Go code (the migration target), even though the current CLI is Python — put it
  where E-1603's `endless verify` will consume it.

## Verify

- A sample `.endless/tasks/E-XXXX/verify.toml` parses into the struct; missing required field or
  unknown `format` fails validation loudly.
- Discovery finds all manifests under `.endless/tasks/*/` and ignores unrelated files.
- Unit tests cover parse + validation + discovery glob.
