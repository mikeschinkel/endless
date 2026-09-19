# E-863 — Implement schema version migration system

## Context

E-1118 just demonstrated the cost of having no schema version system: Python's `_migrate_v5` ran on every connect with no version guard, flipping every `blocks`/`replaces` row's direction. The patch used `PRAGMA user_version` as an ad-hoc gate. Generalize that pattern across all migrations so future migrations can't repeat the same bug.

Today's state:
- **Two migration runners**: Go (`internal/monitor/db.go:migrate` runs V1-V4) and Python (`src/endless/db.py:_migrate` runs V1-V6) both run on every DB connection. They overlap but aren't synchronized — Python is two versions ahead.
- **No version gate** on most migration steps; idempotency is ad-hoc (some via `IF NOT EXISTS`, some via guards, some absent — the V5 case).
- **No explicit `migrate` command**; migrations run as a side effect of opening the DB.
- **No "rebuild requires opt-in"** rule; V5's table rebuild ran whenever the schema looked off.

## Decision: scope (minimal — framework only)

**E-863 establishes the framework. It does NOT port Python's V5/V6 to Go.** That port lives in E-894 Phase 3, which will be the first user of E-863's framework.

Rationale: tier 2 (quick) sized E-863 for ~half a session. Adding V5/V6 port (with V5's tricky table rebuild) would push it to tier 3+ and entangle two unrelated concerns. After E-863 lands, V5/V6 ports become straightforward registry additions.

## Decision: version store

**Both `PRAGMA user_version` and a `_schema_version` table.** The pragma is the gate (atomic, fast, standard SQLite); the table records audit history.

- **`PRAGMA user_version`** — single integer, used by the orchestrator to decide which migrations to run. Already set to 5 by the E-1118 patch.
- **`_schema_version` table** — one row per migration applied, with version, name, applied_at timestamp, runner ("auto" / "explicit" / "force-rebuild"). Inserted after each successful migration; never updated in place.

The two stay in sync. If they ever diverge (e.g. partial failure), the table is the source of truth for "what ran"; the pragma is the source of truth for "what to skip". Recovery prefers the higher of the two.

Schema:
```sql
CREATE TABLE IF NOT EXISTS _schema_version (
    version    INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S', 'now')),
    runner     TEXT NOT NULL DEFAULT 'auto'
);
```

## Architecture

**Go orchestrator** in `internal/monitor/db.go`:

```go
const CurrentSchemaVersion = 4  // bump when adding a migration

type Migration struct {
    Version       int
    Name          string
    Apply         func(*sql.DB) error
    RequiresRebuild bool  // true → only runs with explicit --force-rebuild
}

var migrations = []Migration{
    {Version: 1, Name: "legacy plan→task + base tables",   Apply: migrateV1},
    {Version: 2, Name: "drop dead tables, rename, tier",   Apply: migrateV2},
    {Version: 3, Name: "session conversation history",     Apply: migrateV3},
    {Version: 4, Name: "task_files, suggestions",          Apply: migrateV4},
}

func migrate(db *sql.DB, opts MigrateOpts) error {
    cur, _ := userVersion(db)
    if cur >= CurrentSchemaVersion {
        return nil  // up to date, no work, no backup
    }
    if !opts.SkipBackup {
        BackupDB()
    }
    for _, m := range migrations {
        if m.Version <= cur {
            continue
        }
        if m.RequiresRebuild && !opts.AllowRebuild {
            return fmt.Errorf("migration v%d requires table rebuild; rerun `endless db migrate --force-rebuild`", m.Version)
        }
        if err := m.Apply(db); err != nil {
            return fmt.Errorf("migration v%d (%s): %w", m.Version, m.Name, err)
        }
        if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", m.Version)); err != nil {
            return err
        }
        if _, err := db.Exec(
            "INSERT INTO _schema_version (version, name, runner) VALUES (?, ?, ?)",
            m.Version, m.Name, opts.RunnerLabel(),
        ); err != nil {
            return err
        }
    }
    return nil
}
```

`_schema_version` is created on first run if missing. `RunnerLabel()` returns `"auto"` (implicit on `monitor.DB()`), `"explicit"` (via `endless db migrate`), or `"force-rebuild"` (with `--force-rebuild`).

`monitor.DB()` calls `migrate(db, MigrateOpts{})` exactly as today — implicit, auto-backup, no rebuilds. Explicit `endless db migrate` accepts flags.

**CLI surface:**
- `endless-event migrate-db [--dry-run] [--force-rebuild] [--target N]` — Go subcommand. JSON output: `{"applied": [{"version": 5, "name": "..."}], "skipped": [...]}`.
- `endless db migrate [--dry-run] [--force-rebuild]` — Python verb shelling out (mirrors event_bridge / query_bridge pattern).

## Implementation phases

Single-session work; one branch / worktree.

**Phase 1 — Registry refactor + version gate**
- Convert `internal/monitor/db.go:migrate` to the registry pattern shown above.
- Add `userVersion(db)` / `setUserVersion(db, n)` helpers.
- Each `migrateVN` function stays as-is (V1-V4 already body-idempotent via IF NOT EXISTS / hasColumn / hasTable). The gate just stops them from re-running unnecessarily.
- One-time backfill: existing DBs have `user_version=0` but already-applied schema. On first run: detect post-V4 schema (e.g. `hasTable("suggestions")`) → fast-path set `user_version=4` and skip. Or: re-run V1-V4 (all idempotent) and let them no-op. Recommend the fast-path so we don't re-trigger backups on every existing install.

**Phase 2 — `endless-event migrate-db` Go subcommand**
- Adds new case to the `endless-event` switch.
- Flags: `--dry-run`, `--force-rebuild`, `--target N`.
- Always calls `migrate(db, opts)`; reports JSON.

**Phase 3 — `endless db migrate` Python verb**
- New `@db_cmd.command("migrate")` in `src/endless/cli.py` (or under `task_cmd` if `db` group doesn't exist yet).
- Shells to `endless-event migrate-db`.
- Surfaces output. Exits non-zero on failure.

**Phase 4 — Short-circuit Python's `_migrate`**
- At the top of `src/endless/db.py:_migrate`, add a fast-return: read `PRAGMA user_version`, return immediately if `>= 6` (the highest version Python knows about). Keeps Python from re-running V1-V6 once Go has owned the DB.
- After this, Python's `_migrate` is a no-op for current DBs. E-894 Phase 5 deletes the function entirely.

**Phase 5 — Document the migration-author contract**
- Short docstring at the top of `db.go:migrations` describing:
  - Increment `CurrentSchemaVersion`.
  - Append to `migrations` slice in order.
  - Body must be idempotent or fail loudly on partial state.
  - Set `RequiresRebuild=true` if any step rebuilds a whole table; user must opt in.
  - No assumptions about prior runs — gate is at framework level.

## Files to create / modify

**Modify:**
- `internal/monitor/db.go` — registry refactor, version helpers, post-V4 detection, `_schema_version` table creation + insert-after-success
- `cmd/endless-event/main.go` — add `migrate-db` case + runner
- `src/endless/cli.py` — add `db migrate` verb (create `db` group if absent)
- `src/endless/db.py` — short-circuit `_migrate` on `user_version >= 6`

**Create:**
- (Optional) `src/endless/db_bridge.py` — if you want to keep migration-shellouts in their own module rather than extending `event_bridge.py`. Default: extend `event_bridge.py` to keep the pattern flat.

## Reusable existing helpers
- `internal/monitor/db.go:BackupDB` — existing 60-second-rate-limited backup. Already integrated into `migrate()`; keep.
- `internal/monitor/db.go:hasTable`, `hasColumn` — for post-V4 detection.
- `src/endless/event_bridge.py:emit_event` pattern — shellout/JSON-parse template for the new bridge function.

## Verification

- **Fresh DB**: user_version=0, schema empty → V1-V4 run → user_version=4.
- **Existing post-V4 DB** (typical install today): user_version=0, schema post-V4 → fast-path sets user_version=4 with no migration run, no backup taken.
- **Stability check** (the bug E-1118 just hit): run `endless task show E-X` 10 times — `user_version` and `task_deps` rows unchanged.
- **Hypothetical V5**: bump `CurrentSchemaVersion` to 5, add `migrateV5` to slice. Run `endless db migrate` → V5 runs, version=5, row appended to `_schema_version`.
- **Rebuild gate**: V5 with `RequiresRebuild=true` blocks auto-migrate; only runs with `endless db migrate --force-rebuild`.
- **Dry run**: reports pending migrations without applying.
- **`_schema_version` table**: `sqlite3 db "SELECT * FROM _schema_version ORDER BY version"` shows the audit trail with timestamps and runner labels.
- **Python short-circuit**: with `user_version=6`, instrument `_migrate` to verify it returns at the fast-path check (no SQL executed past the guard).

## Out of scope

- **Port Python's V5+V6 to Go** — E-894 Phase 3. First user of this framework.
- **Remove Python's `_migrate` entirely** — E-894 Phase 5. Until then, Python's `_migrate` is short-circuited (Phase 5 below) so the function returns immediately on a current DB.
- **Rebuild policy for V1-V4** — none of them rebuild whole tables. Constraint applies forward (V5 onward).
