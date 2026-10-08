ERR-0030 (error 1688) escalated on 2026-10-07/08: main-sync reported main and origin/main diverged, and stopped pushing, so "ahead" grew 1 → 9 → 42 → 44 while every new commit queued behind it.

main's reflog shows why. The ledger auto-commit made "Endless: record ledger entry" at 23:27:03 and amended it four times within three seconds, as designed. main-sync pushed one of those amends (159420500) to origin. The auto-commit then amended it twice more (23:27:11, 23:30:11 → e564c0668), rewriting a commit origin already held.

Clearing it took a manual `git merge origin/main`, which conflicts in the db-ledger segment — the write-ahead log — and has to be resolved by hand by keeping main's version (origin's was an exact in-order prefix). Every recurrence repeats that.
