# E-717 Post-mortem

## What was delivered

### `endless task next` command
- Shows top actionable **leaf nodes only** (tasks with no children)
- Excludes: completed, blocked, and dependency-blocked tasks (via `task_dependencies` table)
- Ranking: status priority (in_progress > verify > ready > needs_plan > revisit), then phase (now > next > later), then sort_order
- `--project <name>` — scope to one project (default: all projects, grouped by project)
- `--limit N` — cap results (default: 10)
- Three output formats:
  - **Default**: human-readable table with headers, separator, auto-sized columns, title truncated to terminal width
  - **`--llm`**: token-efficient, no icons/padding, `# project` heading, space-separated fields
  - **`--json`**: structured JSON array

### `--status` and `--phase` filters on `task show` (E-583)
- `endless task show --status ready` — filter tree by status
- `endless task show --phase now` — filter tree by phase
- Orphan fix: items whose parent is excluded by a filter render as roots instead of disappearing

## Key decisions

1. **Leaf nodes only** — parents are grouping containers, not actionable work. This dramatically reduces the list from 60+ to ~10 relevant items.
2. **`--llm` flag** — inspired by llms.txt web standard. Separates human vs LLM output concerns so neither format compromises the other.
3. **No status icons in table** — the icon column duplicated the Status column. Removed for cleaner output.
4. **Manual formatting over tabulate** — tabulate adds uncontrollable padding per column. Manual `click.echo()` with f-strings gives exact column control. E-720 tracks switching to `rich` library later for proper word wrap and table borders.
5. **Title truncation** — titles are truncated with `…` to fit terminal width rather than wrapping (which `tabulate` can't do). Full text available via `endless task detail <id>`.

## Files modified
- `src/endless/task_cmd.py` — `next_tasks()` function, `show_plan()` gained `status_filter`/`phase_filter` params, orphan fix in tree builder
- `src/endless/cli.py` — `task next` subcommand, `--status`/`--phase` options on `task show`
- `docs/guide-2026-04-15-using-endless-in-sessions.md` — documented new commands, removed raw SQL workaround

## Follow-up tasks created
- E-720: Add `rich` library for improved human CLI output (word wrap, table borders) — phase: later
