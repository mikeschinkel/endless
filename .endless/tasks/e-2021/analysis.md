## What E-2019 leaves this task to generate from

The migration set is internal/schema/migrations/, embedded via //go:embed and driven by goose as a library. 00001_baseline.sql was produced by dumping sqlite_master, in creation order, from a database built by schema.sql, then mechanically re-injecting IF NOT EXISTS (SQLite does not store that clause, and the baseline must stay idempotent so the real ledger reaches version 1 by an inert replay). The generator used is a throwaway; this task writes the durable one, in the other direction.

Three things a naive generator would get wrong:

1. FTS5 shadow tables. schema.sql declares one virtual table, session_messages_fts. SQLite then creates session_messages_fts_data/_idx/_docsize/_config, and those appear in sqlite_master like any other table. Declaring them is wrong — the fts5 module owns them. Detect with `SELECT name FROM pragma_table_list WHERE type='shadow'` (SQLite 3.37+), not by name pattern.
2. The enum seeds are NOT in the migration set. They live in internal/schema/seeds.sql and are applied by schema.Seed() after every migration run, so E-1659's self-heal survives. A generated schema.sql that omits them describes a database that fails every integrity gate on first connect; a generated one that includes them has to say where they actually come from.
3. The three leading PRAGMAs (journal_mode, busy_timeout, foreign_keys) are not in the migration set either — they configure a connection, and journal_mode cannot change inside goose's transaction. Every opener sets them itself.

## The cost this task should weigh explicitly

schema.sql is 1,323 lines, and a large fraction of it is commentary that exists nowhere else — the E-1960 index-naming trap, the E-1929 view-vs-index ordering constraint, the ID-append rule on session_task_relations, the reasoning behind the write-once trigger. A generator that emits DDL produces a file that is accurate and says none of that. The description's fallback ("delete schema.sql rather than keep a hand-maintained copy") would delete the commentary with it. Consider a third option: move the prose into the migrations that own each object, so generation has something to carry forward.

While both files exist, internal/schema/migrate_test.go's TestMigrate_MatchesSchemaSQL pins them equal in both directions — a schema.sql edited without a migration fails, and a migration added without mirroring into schema.sql fails. That test is what this task replaces with real generation.

## sqlc, if it is still on the roadmap

Checked 2026-09-16. sqlc reads a goose migration directory natively (schema: <migrations dir>, engine: "sqlite"; sqlc names atlas, dbmate, golang-migrate, goose, sql-migrate and tern) and ignores Down sections. Two constraints worth keeping in mind here, because they shape what the migration set may contain:

- sqlc parses migration files in LEXICOGRAPHIC order, which diverges from goose's numeric order unless filenames are zero-padded. 00001_baseline.sql already is; keep the padding.
- sqlc can only read SQL migrations. A goose GO migration is invisible to it, so any DDL expressed in Go silently disappears from the schema sqlc models. Keep DDL in .sql migrations and put data work in a separate step.
- sqlc gained virtual-table/FTS5 support in 1.31.0 with known rough edges (the fts5 token is case-sensitive — this schema spells it lowercase, which is the working case — and the implicit rowid is not modeled). session_messages_fts is the one object most likely to need attention if sqlc is adopted.
