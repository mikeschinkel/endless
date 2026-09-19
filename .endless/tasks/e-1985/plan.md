# Implementation spec — rune-safe truncation + repair existing damage

## The bug

Three sites truncate UTF-8 text by slicing a Go string at a **byte** offset.
Indexing a Go string yields a byte, so the cut lands mid-codepoint whenever the
boundary falls inside a multi-byte character, writing invalid UTF-8 to the DB.

| Site | Code | Damage in the real ledger |
|---|---|---|
| `internal/monitor/transcript.go:223` | `inputStr = inputStr[:500] + "..."` | **51 rows** |
| `internal/monitor/transcript.go:279` | `summary = summary[:cutoff]` | 1 row |
| `internal/jobs/run.go:466` | `out = out[:157] + "..."` | not yet measured |

Measured 2026-08-16 against the real DB: `session_messages` holds 92,545 rows, of
which **56 contain invalid UTF-8** — 51 `tool_use`, 3 `user`, 2 `assistant`; by
tool, `Edit` 20, `Bash` 15, `Write` 8, `ExitPlanMode` 4, `Agent` 2. Plus one
`sessions.summary` row. This is actively produced, not historical: `Edit` and
`Bash` inputs routinely exceed 500 bytes and routinely contain non-ASCII.

At `transcript.go:279` the sentence-boundary scan is safe by accident — a single
byte can only equal `.`/`!`/`?` on an ASCII character — but the fallback
`cutoff = 200` is a raw byte cut and is what corrupts.

Why it went unnoticed: Python's sqlite3 raises `OperationalError` for the WHOLE
query on an undecodable column, so this surfaced as `session list --limit 100`
crashing rather than as bad text. E-1914 added a lenient `text_factory`
(`src/endless/db.py`), which stops the crash. That stays — it is the right defense
for damage already stored — but it treats the symptom.

## Fix

1. **Add one rune-safe helper** and route all three sites through it:

   ```go
   // TruncateRunes returns s limited to max BYTES, cut on a rune boundary so the
   // result is always valid UTF-8. Returns s unchanged when it already fits.
   func TruncateRunes(s string, max int) string
   ```

   Byte-bounded rather than rune-bounded on purpose: every caller's limit exists
   to bound storage, and a rune budget would let a CJK or emoji-heavy string blow
   past it. Back the cut off with `utf8.RuneStart` until the boundary is valid.

   Place it where all three can reach it without a new dependency edge —
   `internal/monitor` is imported by `internal/jobs`, so a small `strutil.go`
   there works; a standalone `internal/strutil` is equally fine.

2. **Fix all three sites**, including `summary[:cutoff]`. This task owns
   truncation correctness end to end so the invariant lives in one place. E-1925
   renames `setSummaryIfEmpty` → `seedRecapIfEmpty` and must NOT also carry a
   truncation fix; whichever lands second rebases over the other.

3. **Guard against a fourth site.** Add a test that greps the tree for raw
   byte-slice truncation of string values (`[:<int>]` / `[:cutoff]` applied to a
   string) and fails on any occurrence outside the helper. The safe cases in the
   current tree — slicing slices, hex digests, ASCII prefix compares like
   `ref[:2] == "E-"` — must be distinguishable, so match on assignment-back-to-
   string-plus-literal rather than on `[:` alone, or maintain a short allowlist
   with a comment per entry.

## Repair the existing damage

One-time repair of rows already written, as a `.go` change file
(`internal/schema/changes/e-NNNN-repair-invalid-utf8.go`) using
`internal/schema/changes/runner`, which already handles the transaction, the
`_schema_version` marker, and the exit status. SQL alone cannot do this; Go can.

- Scan `session_messages.content` and `sessions.summary` (or `sessions.recap`,
  if E-1925 lands first — the change file must handle whichever name is present,
  or declare its ordering).
- For each value failing `utf8.ValidString`, **trim the trailing partial rune**
  rather than substituting U+FFFD. The damage is a truncation artifact at the
  end of the value, so trimming restores exactly what a correct truncation would
  have produced. Only if invalid bytes appear mid-value (none observed) fall back
  to dropping the invalid sequences.
- Report the count repaired on stdout so the run is auditable.

The lost bytes are unrecoverable — the codepoint was cut before storage — so this
makes the stored text valid, not complete. Full fidelity is E-1984's question.

## Verification

`tests/tasks/e-NNNN-verify.sh`, isolated exactly as `e-1914-verify.sh` is.

- `TruncateRunes` unit tests: a multi-byte character straddling the limit; a
  string already under the limit (returned unchanged); limit landing exactly on a
  boundary; an all-ASCII string; a string shorter than one rune's width.
- A first assistant response with a multi-byte char at byte 200 and no `.`/`!`/`?`
  in bytes 101-200 produces valid UTF-8 (drives the `transcript.go:279` path).
- A tool_use input over 500 bytes with a multi-byte char at the boundary produces
  valid UTF-8 (drives `transcript.go:223`).
- Every row written by a seeded transcript parse satisfies `utf8.ValidString`.
- The change file repairs a seeded invalid row, leaves valid rows byte-identical,
  and a re-apply is a recorded no-op (`"status":"skipped"`).
- The anti-regression grep finds no raw byte-slice truncation outside the helper.
- The error/login auto-hide still fires (it shares the `transcript.go:279`
  function and must not be disturbed).

## Non-goals

- **Do not remove or change any truncation LIMIT.** Whether 500 chars is the right
  bound, and whether tool results should be captured at all, is E-1984's call.
  This task makes the existing truncation correct; it does not relitigate it.
- **Do not remove the lenient `text_factory`.** It defends against damage this
  fix cannot reach — other machines, older backups, and any value written before
  this lands.
