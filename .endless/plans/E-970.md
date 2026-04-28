# W2 — Phrases Table

> Part of E-968 (parallel-session support). See `.endless/plans/E-968.md` for full context. Obsoletes E-757.

## Why

Several places in endless hardcode lists of strings: title-verb whitelist in `task_cmd.py` (existing E-757 proposal), action regexes in `cmd/endless-hook/claude.go`, etc. The behavioral gate in W3 (E-971) needs another such list (pivot triggers). One general table beats N specific tables.

## Schema

```sql
CREATE TABLE phrases (
  id INTEGER PRIMARY KEY,
  kind TEXT NOT NULL,                 -- 'pivot', 'verb', 'action', ...
  phrase TEXT NOT NULL,
  match_type TEXT NOT NULL DEFAULT 'substring',  -- 'substring' | 'word' | 'regex'
  case_sensitive INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%S','now')),
  UNIQUE(kind, phrase)
);
```

`match_type` is orthogonal to `kind`: different kinds typically default to different match types, but rows within a kind can mix as needed.
- `substring` — input contains the phrase. Default; right for most pivot phrases.
- `word` — word-boundary match. Right for verbs ("Add" matches "Add a feature" but not "Adder").
- `regex` — full regex pattern. For action patterns and any future complex matches.

Project-scoped. Global fallback if a `kind` has no project rows.

## Naming convention (general principle)

- **snake_case** for data and code identifiers in non-Go contexts (DB column names, Python/Ruby/Rust variable names, env var names; also DB string-column *values* that are NOT user-typed).
- **kebab-case** for user-facing strings (CLI flags, CLI argument values that the user types as labels including `kind` values in this table, file names, URL slugs, git branch names).
- **Go exception**: Go identifiers use PascalCase (exported) or camelCase (unexported), NEVER snake_case or kebab-case.

So `kind='action'` (kebab; user types it on CLI), `--case-sensitive` (kebab), `task/757-move-title-verbs` (kebab), `CreateWorktree` (Go PascalCase), `def parse_phrase` (Python snake).

## CLI

- `endless phrase add <kind> "<phrase>" [--match substring|word|regex] [--case-sensitive]`
- `endless phrase list [--kind <k>]`
- `endless phrase disable <id>` / `endless phrase enable <id>`
- `endless phrase remove <id>`

No `endless verb` alias; not enough use to warrant the surface.

## Seed data

`kind='pivot'`, default `match_type='substring'`:
- case-insensitive: "actually", "wait", "also can you", "by the way", "btw", "different", "switch to", "while you're at it", "instead", "and can you"
- case-sensitive: "PIVOT" (Mike's deliberate override)

`kind='verb'`, `match_type='word'`, case-insensitive: seeded from current `_TITLE_VERBS` set in `src/endless/task_cmd.py`.

`kind='action'`, `match_type='regex'`: lifted from hardcoded regexes in `cmd/endless-hook/claude.go`.

## Critical files

- `internal/schema/schema.sql` — new `phrases` table.
- `internal/monitor/db.go` — migration.
- new: `src/endless/phrase_cmd.py` — CLI verbs.
- `src/endless/task_cmd.py` — read `verb` phrases from DB instead of `_TITLE_VERBS`.
- `cmd/endless-hook/claude.go` — read `action` phrases from DB at startup, cache in memory.

## Verification

- Add a `pivot` phrase via CLI; confirm it appears in `endless phrase list`.
- Disable; confirm `enabled=0`.
- Replace a hardcoded title-verb check with a DB lookup; confirm task creation still validates.
- Replace a hardcoded action regex with a DB row; confirm hook still detects task starts/completes.
