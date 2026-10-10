# Plan: rename "unindexed" to "log only"

About 70 lines across `internal/faults`, `internal/errorscmd`,
`internal/faultrow` and `docs/errors.md`.

1. `errors.jsonl` writer: emit `"log_only": true` instead of `"unindexed": true`,
   and `db_error` instead of `index_error`.
2. Reader: accept both the new and the old key names. Existing log files already
   hold `unindexed`/`index_error` lines, and they must still be listed and
   cleared. The reader keeps that compatibility; only the writer changes.
3. `eeh` / `errors list` header: "N occurrence(s) written to log only; DB write failed:".
   Per-line reason prefix: "not in the database:" (replacing "not indexed:").
4. Every other user-facing string that says indexed/unindexed (the `errors clear
   --log` help and footer, `errors show`'s pointer, the faultrow notice,
   `docs/errors.md`): say "log only" or "not written to the database".
5. Code names follow the user-facing term: `unindexed.go` -> `logonly.go`,
   `UnindexedCount` -> `LogOnlyCount`, and the rest to match. Test names too.

## Verification

- Unit: a fault whose DB write fails writes `"log_only": true` and `db_error`.
- Unit: a log holding an old-format `"unindexed": true` line is listed and
  cleared exactly as a new-format one.
- `eeh` output shows the new header and reason prefix.
- Grep guard: no user-facing string under `internal/` or `docs/` says
  "indexed"/"unindexed" about faults (the back-compat key in the reader is the
  only allowed hit).
