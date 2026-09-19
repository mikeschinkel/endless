Landed 2026-04-27 (812e138). Confirmed 2026-08-25 AS SCOPED, with the shortfall recorded here rather than by reopening.

Shipped: the projection engine. ProjectToTempDB replays the whole ledger into a scratch database, endless-go event rebuild-db exists, and validate-db exists alongside it.

NOT met, of this task's own stated criterion 'given same events, any machine produces same DB state': the projector builds task_landings and task_deps, and the copy-back step writes back only tasks, decisions and decision_relations. Two machines replaying one ledger can therefore differ, because task_deps is left holding whatever their SQLite already had. Owned now by E-1728, under the E-1935 epic.

Also outstanding, owned elsewhere: replay-side FK and UNIQUE failures plus fixture ledgers reusing real task ids (E-1041), and the fact that rebuild-db --confirm deliberately refuses to run (E-2062). E-1935's analysis carries the measured detail.