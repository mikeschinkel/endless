# Decode BLOB-stored values at the DB read boundary

## Root cause

`endless task show <id> --text` crashes with `TypeError: write() argument must
be str, not bytes` when a task's text is stored with SQLite storage class BLOB
rather than TEXT. The chain: the row value arrives from sqlite3 as `bytes`,
`click.echo` takes its binary branch, and `_ColorProxy` — the stdout stand-in
that fixes `isatty()` so colorization follows an explicit decision — forwards
straight to a text stream in its `write`. The whole command dies with a raw
traceback naming no task and no field.

Exactly one value in the ledger is affected: `tasks.text` for E-449. Every
other value in every text-bearing column across tasks and decisions is TEXT or
NULL. Confirm this with a `typeof()` survey before starting rather than trusting
this statement.

The write path that produced it no longer exists. Every file read in the Python
package uses `read_text()`; there is no binary read. The row dates from an early
plan importer. Do not hunt for a live writer to fix — there isn't one.

## Prior art this extends

`get_conn` in the db module already solves the identical failure shape for a
different storage class. Read its comment first: E-1914 set a lenient
`text_factory` because sqlite3's default raises on invalid UTF-8 for the whole
QUERY, so one damaged byte took down commands that never asked for that column.
Its reasoning — apply at the connection rather than per query, because the
failure belongs to reading at all, and per-column fixes are whack-a-mole — is
the same reasoning that applies here, and the fix belongs beside it.

The gap E-1914 left is that `text_factory` governs only values SQLite hands
back as TEXT. A value stored as BLOB bypasses it entirely and arrives as
`bytes`. This task closes that gap.

## The fix

Extend the `row_factory` assignment in `get_conn` so bytes values are decoded
with the same lenient policy `text_factory` already uses:

    _conn.row_factory = lambda cur, row: sqlite3.Row(
        cur,
        tuple(
            v.decode("utf-8", "replace") if isinstance(v, bytes) else v
            for v in row
        ),
    )

This preserves the `sqlite3.Row` interface — key indexing and `keys()` both
continue to work — so no call site changes. Verified against a scratch DB
holding a BLOB in a TEXT-declared column.

Extend the existing E-1914 comment rather than adding a second block beside it;
the two halves are one policy about reading damaged values leniently, and
should read as one.

## Why decoding at this layer is safe

The schema declares no BLOB columns in any table. Any bytes value reaching a
caller is therefore an anomaly by definition, never intentional binary, so
there is nothing this can corrupt. Re-verify by searching the schema for BLOB
before landing; if that ever stops being true this fix needs revisiting.

## Do not fix in the renderer

`_ColorProxy.write` is where the crash surfaces, and hardening it there is the
tempting small change. Resist it: the proxy is installed only on some paths, so
the same bad value would still reach a real stdout on the others, and the next
consumer of a bytes-valued field breaks somewhere new. One decode at the read
boundary covers every command and every field at once.

## The fossil row

E-449's stored text can be repaired with a CAST to TEXT. This is optional — the
read fix renders it correctly either way, and the task is obsolete — but it
costs nothing and removes the only instance. Decide at implementation time; if
repaired, confirm the surveyed count of non-TEXT values drops to zero.

## Verification

- `endless task show 449 --text` renders the plan body instead of a traceback,
  both with and without `--paged`, and under `--no-color`.
- A regression test covering a BLOB-valued column read through `get_conn`,
  asserting a `str` comes back and the Row interface still works.
- The typeof survey across text-bearing columns reports no non-TEXT values.
- Full Python test suite passes.
