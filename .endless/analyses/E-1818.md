Discovered during E-1659 (task-type slug rename `task`->`todo`, `bug`->`bugfix`).
Building an experimental `schema.SQL` (a self-heal `UPDATE task_types ...`) into
the worktree's `bin/endless-go` silently rewrote the REAL DB's `task_types` rows
to the unlanded slugs. Every deployed / other-worktree binary then fail-closed
(`VerifyIntegrity`: their enum vs. the drifted rows), so `session status` and most
endless commands broke machine-wide — with no land and no explicit DB write.

MECHANISM. The worktree's `.claude/settings.json` (E-998 claude-settings-init)
runs `<worktree>/bin/endless-go hook claude` on every Claude tool event. `hook`
calls `monitor.ForceRealDB()`, which — because the session's `XDG_CONFIG_HOME`
points at the sandbox (`IsSandboxActive()` true) — sets `dbPathOverride` to
`~/.config/endless/endless.db` so the dev session's real activity lands in the
real ledger (E-1450 / E-1700). `monitor.DB()` then applies the running (worktree,
unlanded) binary's embedded `schema.SQL` + enum integrity to that real DB. So an
unlanded binary migrates a database it does not own. Same exposure via
`PinMainDB()` (endless-channel, endless-tmux), which also sets `dbPathOverride`.

EVIDENCE. After the incident the real DB's `task_types` held the unlanded slugs
but carried NO `_schema_version` marker — a plain on-open `schema.SQL` reconcile,
not an `apply-change`; the sandbox separately held the E-1659 marker from a real
`apply-change`. The real DB was reverted to its correct pre-E-1659 state
(`task`/`bug`) and a backup retained at
`~/.config/endless/endless.db.e1659-incident-backup-*`.

SEVERITY. Any self-dev worktree that edits `schema.SQL` or a mirrored enum
(`tasktype`, `sessionkind`) can corrupt or brick the shared real ledger the
instant its binary is rebuilt and a hook fires — before any land, review, or
explicit action. This is why the standing rule "destructive changes live in a
land-time apply-change file, never in schema.SQL" is load-bearing; this bug lets
on-open `schema.SQL` reach the real DB from an unlanded binary.

FIX APPROACH (mine, Mike to confirm). Gate on-open `schema.SQL` + integrity so a
ForceRealDB / PinMainDB-pinned (candidate) binary opens the real DB
schema-passive — see the attached plan. Related prior art: E-1450 / E-1700 (why
the real-DB pin exists), E-1458 (opt-in hook->sandbox routing), E-995 / E-1045
(prod-leak detection for tests).

USE-CASE PRESERVED (confirmed against E-1450, landed 2026-05-24 cd1281c). The
pin (ForceRealDB / PinMainDB) exists so hook-fired WRITES — session/pane
registration, event emission, task-status transitions — reach the real ledger
even from a sandbox worktree, because the `sessions` table is machine-wide
ground truth for pane registration. This fix keeps all those DATA writes on the
real DB; it removes only the incidental application of the running binary's
`schema.SQL` + integrity gate. So E-1450's use-case is intact.

LAND UNAFFECTED. Schema migration at land runs via `endless db apply-change`
from the MAIN checkout with `--config-dir` → real DB (justfile land recipe). The
`event` command resolves its DB via `ConsumeDBContextFlag`, NOT the pin, so
`dbPathOverride == ""` there and the fix's gate does not catch it — `apply-change`
keeps applying `schema.SQL` + change-files exactly as today. The gate targets
only the pinned hook/channel/tmux/session-status surfaces.

SCOPE BOUNDARY (DECIDED — deferred, wait-and-see). The gate closes the AUTOMATIC
vector (hooks, no user intent). A worktree binary invoked with an explicit
`--config-dir ~/.config/endless` would still apply schema to the real DB — which
is exactly how land migrates, and is deliberate intent. Forbidding even that
(schema reaches the real DB ONLY via apply-change, never incidentally on open)
is a larger change; Mike's call (2026-07-30) is to NOT harden it now and revisit
only if it actually causes a problem. Do not fold it into this fix.
