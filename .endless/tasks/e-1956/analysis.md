No new status needed.

Three parts: (1) render the relation inline with terminal statuses everywhere status is shown — `assumed (replaced by E-1953)` — in `task show`, `task list` and `session status`; (2) guard `obsolete` on any task that reached unverified/confirmed/assumed/completed, refusing or warning and pointing at `replaced_by` (confirmed absent: setting an `assumed` task to `obsolete` succeeds silently); (3) fix `task update --help`, whose --status list omits `submitted` and `completed` though both are accepted.
