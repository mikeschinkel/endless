# Evidence

Writes: `datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%S")` at ~20 call
sites across src/endless (register.py, notes_cmd.py, reconcile.py, scan.py,
task_cmd.py, ...). Stored form carries NO offset.

Reads: there is no `astimezone`, `localtime`, or `fromtimestamp` anywhere in
src/endless. Nothing converts.

Observed 2026-08-15:

    wall clock                08:13:29 EDT  (12:13:29 UTC)
    E-1978 stored created_at  2026-08-15T09:43:08
    `endless task show 1978`  "2026-08-15 9:43 am"

The task was created at 05:43 local and is displayed as 9:43 am — four hours in
the future, silently.

# Why this is more than cosmetic

`git reflog` prints LOCAL time. Correlating endless output against git makes
effects appear to precede causes: E-1919's "Landed: 5:43 am" and the reflog's
`rebase (finish)` at `01:43:56 -0400` are the same event. Half an hour was spent
in this session reconciling those two clocks before realising they were the same
moment. Any user doing forensics across the two hits this.

# REQUIRED AUDIT — this task must establish scope before fixing

The Python side above is confirmed. The following are NOT yet checked and the
task must check them rather than assume:

1. **Go side.** internal/* writes to the same DB (ledger rows, session rows,
   activity, jobs). Does it stamp UTC identically? Any Go-side display path
   (session status, monitor, tmux status line) has the same conversion question.
2. **Web dashboard.** `endless serve` / internal/web renders timestamps
   independently and may already convert, may convert differently, or may not.
3. **Storage consistency.** If any writer stamps local while others stamp UTC,
   the DB already holds mixed-zone values and a display-boundary fix alone is
   insufficient — that would need a data audit, not just a formatter.
4. **Relative-time helpers.** task_cmd.py:1302 computes a delta against
   `datetime.now(timezone.utc)`; anything doing "3h ago" arithmetic against a
   naive-parsed string is a second, independent failure mode.

# Fix shape (subject to the audit)

Fix at the display boundary, not in storage: keep writing UTC, convert to the
operator's zone when formatting, and parse the stored form as UTC EXPLICITLY
rather than relying on naive-datetime defaults. Decide whether to print the zone
abbreviation so displayed values are self-describing.

PRODUCT: this is wrong for every user outside UTC, on every task, session, note
and decision the tool displays — not a dev-machine quirk. A user in UTC+13 sees
tomorrow's dates.
