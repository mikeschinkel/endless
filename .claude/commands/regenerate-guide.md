---
description: Regenerate the agent-facing guide cross-reference (command/topic → guide section map) and the index.md table.
allowed-tools: Bash(just guide-scaffold), Bash(just guide-index), Bash(just guide-check), Read, Write, Edit, Glob
---

# Regenerate the guide map

You are maintaining the cross-reference that maps every `endless` command (and a
set of topics) to the guide section that explains it. This map drives two things:

- the **command/topic → section table** in `docs/guide/index.md`, and
- the **agent `--help` directive** prepended to every command's `--help`.

Deciding *which section explains a command* is your job — the deterministic parts
(enumerating commands, listing section headers, validating, assembling the table)
are already done by `src/endless/guide_map.py` and the `just guide-*` recipes. Do
**not** invent the command list or section names from memory; read them from the
scaffold.

## How the map works

- Map files live in `docs/guide/help/<command-path>.md`, where the command path is
  hyphenated: `endless task spawn` → `docs/guide/help/task-spawn.md`.
- **Inheritance:** a command resolves to the nearest file walking up its path
  (`task clear tier` → `task-clear` → `task`). So one `task.md` covers every task
  subcommand. Add a leaf file (e.g. `task-spawn.md`) **only** when a subcommand
  belongs to a *different* section than its group.
- Each file is either a **mapped** file:

  ```
  section: orchestration
  covers: Short phrase shown in the table and the --help directive.

  Optional one- or two-line note: a command-specific trap NOT already in the guide.
  ```

  (`section:` may be comma-separated for multiple sections.) …or a **gap** file:

  ```
  gap: one sentence on why no current section fits (drives guide improvement).
  ```

- Topics that aren't commands live in `docs/guide/help/_topics.md`, one record per
  blank-line-separated block:

  ```
  topic: who am I / current session
  section: sessions
  covers: Discovering your session id and the task it's bound to.
  ```

Principle: the map file **points**; the guide section **explains**. Never copy
guide prose into a map file — keep `covers:` to a short phrase so nothing drifts.

## Steps

1. **Read the skeleton.** Run `just guide-scaffold`. It prints every guide section
   with its headers, then every command marked `own` / `<- <ancestor>` / `MISSING`.
2. **Read the sections.** For each section slug, read `docs/guide/<slug>.md` so you
   know what it actually covers before mapping commands to it.
3. **Map / fix.** For each `MISSING` command, and for any existing mapping that no
   longer fits (wrong section, stale `covers:`, a gap a new section now covers),
   write or edit the appropriate `docs/guide/help/*.md` file. Prefer mapping at the
   group level; add leaf overrides only for genuine exceptions. Use a `gap:` file
   when no section honestly fits — do not force a bad mapping.
4. **Rebuild the table.** Run `just guide-index` (rewrites only the generated block
   in `docs/guide/index.md`).
5. **Validate.** Run `just guide-check`. It must exit 0. Fix any FAIL lines
   (missing file, missing fields, bad section, orphan file, stale index).
6. **Report the gaps.** `guide-check` lists acknowledged coverage gaps. Summarize
   them for the user as candidates for *improving the guide itself* — each gap is a
   command the guide doesn't yet explain.

Do not edit `docs/guide/index.md`'s generated block by hand — it is rewritten from
the map files by `just guide-index`.
