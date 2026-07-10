Bootstrap V0 schema.SQL at the top of migrate() so fresh DBs (sandbox or otherwise) get the foundational tables (projects, tasks, notes, activity, project_deps) before V1+ patches run. Removed the now-redundant cur==0 + has_suggestions fast-path that would have skipped V6-V8 bodies on fresh DBs.

To verify:
1. From this worktree: 'go test ./internal/monitor/...' — TestMigrateFreshDB_CreatesBaselineTables and TestMigrateIdempotent both pass.
2. Confirm self-heal: 'sqlite3 ~/.cache/endless/sandboxes/worktree-e-1327/endless/endless.db ".tables"' — should now include projects, tasks, notes, activity, project_deps (already healed on disk via 'XDG_CONFIG_HOME=...worktree-e-1327 bin/endless-event migrate-db').
3. Fresh-sandbox smoke test: 'bin/endless-sandbox init smoke-e1345 && mkdir -p ~/.cache/endless/sandboxes/smoke-e1345/endless && XDG_CONFIG_HOME=~/.cache/endless/sandboxes/smoke-e1345 bin/endless-event migrate-db' — audit rows V1-V4,V6-V8 run with runner=auto on first contact; all baseline + V6-V8 tables present.
4. Main DB undisturbed: 'sqlite3 ~/.config/endless/endless.db "PRAGMA user_version; SELECT version, runner FROM _schema_version ORDER BY version;"' — unchanged (8 rows V1-V8, V1-V5 backfill, V6-V8 auto).
5. Re-spawn E-1327: 'endless task spawn E-1327' — the auto-registering project hook error should not recur.