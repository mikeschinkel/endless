## 2026-10-31: test-fixture sessions in the real database

Found while planning E-2519. The main database's sessions table holds rows
that tests wrote: `sess-A` (ES-1224) and `e940-verify-session` (ES-1266, from
E-940's verify suite). Both are non-ended rows with no pane, so every reader
that counts present sessions counts them. E-1730 covers leaked ledger events;
nothing covers leaked session rows.

What Mike agreed to add here:

1. A naming standard for test sessions, which Endless reports as an error
   wherever a matching row appears in the main database. Detects a leak after
   it happens.
2. The Go and Python test harnesses mark their process as a test, and the
   database layer refuses a write to the main database from a marked process.
   Turns a leak into a failing test instead of a stray row.
3. A command that removes rows matching the standard from the main database,
   and fixes the tests that wrote the two rows above.
