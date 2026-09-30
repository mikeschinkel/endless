Endless writes every timestamp as UTC (`datetime.now(timezone.utc).strftime(...)`, ~20 call sites) and never converts on display — no `astimezone`, `localtime` or `fromtimestamp` anywhere in src/endless.

Every date the CLI shows is therefore UTC rendered as if local: verified, E-1978's stored created_at is 2026-08-15T09:43:08 while the wall clock read 05:43 EDT, and `task show` printed '9:43 am'.

This is not merely a footgun when correlating endless output with `git reflog` (which prints local) — it is wrong on its own terms for every user outside UTC, on every task, session, note and decision displayed, and it silently makes effects look like they precede causes.
