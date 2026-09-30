tests/tasks/e-1905-verify.sh line 420 asserts `SELECT count(*) FROM sqlite_master WHERE type='trigger' AND name LIKE 'sessions_null_process%'` returns 2 after applying E-1905's change file to the pre-E-1905 fixture DB.

E-1898 REMOVED both `sessions_null_process_on_end_*` triggers (internal/schema/schema.sql records the removal; internal/schema/changes/e-1898-processes-identity.go drops them), so the count is 0 and the check has been red since E-1898 landed.

Nothing is actually broken — the assertion outlived its subject.

Found while running the suite during E-1968, which touched a different section of the same file (its layer B) and is otherwise unrelated.
