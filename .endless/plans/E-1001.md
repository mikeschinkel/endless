# Plan — consistent --xxx / --xxx-file content flags

Design reasoning is in this task's analysis field (`endless task show E-1001 --analysis`).

## Change

For long-form content flags **text, analysis, outcome, description**: bare `--xxx` = inline,
new `--xxx-file` = path. Leave **justification** and **reason** inline-only. Remove the
`@file` magic on `--analysis`. Hard cutover, no alias.

## Surface area

- **`src/endless/cli.py`** — add `--text-file`, `--analysis-file`, `--outcome-file`,
  `--description-file`; make the bare flags inline. Affected commands include task add
  (~1205 description, ~1207 text, ~1228 justification), task update (~1276 description,
  ~1278 text, ~1290 analysis, ~1294 outcome, ~1296 justification), and `--outcome` on
  complete / decline / assume (~1351, ~1364, ~1386, ~1590). Remove `@` handling at ~1305.
- **Handlers** — `task_cmd.py` / `plan_cmd.py`: the file-read logic moves to the `--xxx-file`
  branch; bare flags pass through as literals.
- **Handoff templates** (`internal/templatecmd/templates/handoff/*.tmpl`) — change
  `research.md.tmpl`'s `--outcome <file>` → `--outcome-file <path>` (broken today). Audit the
  other three. NOTE: the `endless task show … --text` lines are the boolean *display* flag, not
  the content flag — leave those.
- **Guides / docs** — the 6 `docs/guide/*.md` (tasks, index, reference, sessions, orchestration,
  decisions), README, and any in-repo docs with `--text <file>` / `--analysis @` examples.
- **Tests** — update existing usages; add coverage that both forms work and that `--analysis @`
  no longer file-loads.

## Verification

- `endless task add/update --help` shows the split for text/analysis/outcome/description;
  justification/reason remain inline-only.
- `endless task update <t> --text 'inline'` stores the literal; `--text-file path.md` stores file
  contents; `--analysis @path` no longer file-loads.
- `grep` for `--text <path>`, `--analysis @`, `--outcome <file>` across src/, templates/, docs/,
  README finds no leftovers.
- `just test` green; spawn a task and confirm the research handoff's `--outcome-file` works.
