Task branches can be left based on Endless: record ledger entry commits that later get superseded on main via the auto-amend flow (canAmend in internal/events/commit.go rewrites a ledger commit's SHA whenever a new event is appended).

Hit on E-1333's land 2026-05-14 (orphan was 0514e4a); resolved manually via git rebase --onto main 0514e4a HEAD.
