Two changes to the `endless errors list` table.

1. LAST SEEN: render `2026-10-01T13:10:28` as a short local time, e.g. `Oct 1 1:10pm`
   — month abbreviation, day without padding, 12-hour time with am/pm, no seconds and
   no year. Errors should be cleared long before a year passes, and the ID column
   already orders errors recorded within the same minute.
   Note: the stored value is UTC (`strftime('now')`), so the display must convert to
   local time — `13:10:28` UTC is `9:10am` in EDT. Decide whether `errors show` and
   `--json` change too (likely: show keeps the full timestamp, JSON stays ISO).

2. A TASK column: the task of the session whose process raised the error.
   The `errors` row has no session or task column today, and a row is a deduplicated
   INCIDENT (one fingerprint, many occurrences), so one row can span several sessions.
   Open questions for the plan:
   - Which task a multi-occurrence row shows: the latest occurrence's, the first's,
     or a marker when they differ.
   - Where the session comes from: occurrences are captured in errors.jsonl
     (`errors show --detail`); does each capture carry the session/process, or does
     the fault writer need to start recording it (a schema column on the incident
     for the latest occurrence)?
   - What faults raised outside any session show (jobs, the tmux status bar, a
     shell) — blank.
