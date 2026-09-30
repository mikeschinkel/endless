E-1450 (landed) routes hook-fired DB writes to the real DB unconditionally via monitor.ForceRealDB() at hook process entry — correct for production hook events fired by Claude Code, but leaves no opt-in for a developer who wants to exercise 'endless-go hook' directly against a sandbox DB to validate hook behavior without polluting the real ledger.

Until this lands, hook tests must inject DB handles via touchSessionDB and equivalents.
