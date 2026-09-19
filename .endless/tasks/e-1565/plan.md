# E-1565 — Migrate spawn handoff templates from Python string.Template to Go text/template renderer

Foundation for E-1566 (per-type variants). Lands first.

## Decisions

### Renderer surface
- **Renderer lives in Go.** Add `endless-go template render <name>` subcommand. Reads JSON vars on stdin, writes rendered template on stdout.
- **Templates embedded for shipping.** `//go:embed templates/*.tmpl` at the package level. Templates ship inside the binary; this is the **distribution mechanism**, not the load mechanism.
- **Two project-scoped on-disk paths:**
  - `<project_root>/.endless/templates/<name>.tmpl` — **committed**, team-wide override. Materializes from embed on first render.
  - `<project_root>/.endless/templates/<name>.local.tmpl` — **per-developer**, never written by the renderer (user creates manually). Takes precedence over the committed `.tmpl`.

### Hydrate-on-demand mechanism
At render time, `endless-go template render <name>`:

1. **Resolve `<project_root>`:**
   - If `--project <name>` is supplied → `SELECT path FROM projects WHERE name = ?` (against the active DB per `--db main|sandbox` / `ConsumeDBContextFlag`). Missing row → error. Empty/missing `path` → error.
   - Else → resolve from cwd via the existing endless project-root helper (locate at impl: `grep -n "project_root\|find_project_root\|ResolveProjectRoot" internal/`).
   - Either way: `<project_root>` is the absolute path of the project's working directory.
2. **Project context is required.** If neither path resolves, error: `"template render requires a project context — cd into a project or pass --project <name>"`. Exit non-zero.
3. **Per-file materialize check** (not per-directory): for the requested template name only, check if `<project_root>/.endless/templates/<name>.tmpl` exists. If missing, write the embedded content to that path (creating parent dirs as needed).
4. Read the resolved file (see lookup order below) → render → stdout.

**Per-file, not directory-level**, so a user can delete any single template to restore that one embedded version (e.g. `rm .endless/templates/handoff/epic.md.tmpl` → next render of `handoff/epic` rewrites just that file). Touching one file does not affect siblings. No bulk-restore command needed; deletion is the restore mechanism.

User edits to the materialized file persist (they're now ahead of embed). Embed updates from new endless releases do not auto-propagate — user owns their copy once written. Documented behavior, not a wart.

### Lookup order at render time
For each render of `<name>`:

1. `<project_root>/.endless/templates/<name>.local.tmpl` — per-developer override, gitignored
2. `<project_root>/.endless/templates/<name>.tmpl` — committed, team-wide
3. embedded

Materialize step (above) only writes the committed `.tmpl` variant. `.local.tmpl` is purely user-created; renderer never writes there.

### `.endless/templates/` is committed; `.local.tmpl` is user-managed
- `<project_root>/.endless/templates/<name>.tmpl` goes into git, same as `.endless/db-ledger/`. Team-wide customizations ride with the project.
- `<project_root>/.endless/templates/<name>.local.tmpl` is per-developer. The renderer **never writes** there — it's created manually by users who want a personal override. Since materialize only writes `.tmpl` (committed), a stray `.local.tmpl` only enters git if the user explicitly stages it.
- **No automatic `.gitignore` modification.** The renderer does not touch `.gitignore`. Documenting the recommendation in **E-1573** (guide docs) is the right surface: users who want belt-and-suspenders add `.endless/templates/*.local.tmpl` to their `.gitignore` themselves. No "have we asked them yet" tracking, no interactive prompts in a subprocess.

### Variables and missing-key policy
- Variable names unchanged: `spawned_id`, `title`, `spawner_task`, `return_anchor`, `worktree_path`, `branch`. Go `text/template` resolves `{{.spawned_id}}` against a `map[string]any` decoded from stdin JSON.
- Use Go's default `<no value>` substitution for missing vars (matches `string.Template.safe_substitute()` graceful degradation). No `template.Option("missingkey=error")`.
- Python wrapper supplies the same defaults the current code uses (`"?"`, `"%<spawning-pane>"`, `"<task worktree>"`, `"<task branch>"`) before invoking Go, so `test_render_handoff_degrades_without_runtime_context` continues to assert visible placeholders.

### Python wrapper preserved
`render_handoff(...)` in `src/endless/task_cmd.py` stays — body becomes `subprocess.run(["endless-go", "template", "render", "handoff"], input=json.dumps(vars), capture_output=True, ...)`. Spawn callers and `tests/test_handoff.py` are unchanged. Binary resolution reuses `event_bridge._resolve_endless_go_binary()` (already handles sandbox/main routing).

### Hidden Python CLI surface
New hidden click group `endless internal` with subcommand `template render <name>` that shells through to `endless-go template render`. Lets us debug-render any template from the CLI without touching spawn flow.

### What does NOT go in this child
- Per-type variants (task/bug/research/epic) — E-1566.
- Children-state breakdown var — E-1567.
- Coordinator template set under `templates/coordinator/` — E-1567.
- Override-template lookup at any path OTHER than `<project_root>/.endless/templates/` — out of scope.

## Files

### New
- `internal/templatecmd/template.go` — `Run(args []string)` dispatcher with `render` verb; embeds `templates/*.tmpl`; resolves project root + materializes-if-missing; parses stdin JSON; renders to stdout. Pure functions split: `materializeIfMissing(projectRoot, name) error`, `loadTemplate(projectRoot, name) (string, error)` (returns embedded content when projectRoot is empty), `render(content, vars) (string, error)`.
- `internal/templatecmd/template_test.go` — Go-level tests:
  - Renders `handoff` with full vars.
  - Renders with missing vars → `<no value>` for the missing field.
  - Rejects unknown template name (no embed match, no on-disk match) with a non-zero exit and a clear error.
  - Materialize-on-demand: temp project root + missing file → file appears with embedded content; `.gitignore` is untouched.
  - User-edit persistence: pre-write a modified file in temp project root → render uses the modified content (no overwrite).
  - Delete-to-restore: pre-write modified, delete, render again → file reappears with embedded content.
  - `.local.tmpl` precedence: pre-write both `handoff.tmpl` and `handoff.local.tmpl` with distinct content → render uses `.local.tmpl`.
  - No project context → exits non-zero with the "requires project context" message; no file written anywhere.
  - `--project <name>` resolves to `projects.path` via the active DB: seed a test project row with a temp path, render with `--project test-proj`, assert the temp path is used as `<project_root>`.
  - `--project unknown` → exits non-zero with a clear "project not found" message.
- `templates/handoff.md.tmpl` — verbatim port of `docs/templates/handoff.md` with `$var` → `{{.var}}`.

### Changed
- `cmd/endless-go/main.go` — add `"template"` to switch, route to `templatecmd.Run`. Add `template` to usage block. `template` reads DB (for `--project` → `projects.path`); the global `monitor.ConsumeDBContextFlag()` at the top of `main()` already covers `--db main|sandbox` / `--config-dir` for the subcommand.
- `src/endless/task_cmd.py` — `render_handoff` body: build vars dict (with the same defaults the current code applies), shell out to endless-go, return stdout. Drop `from string import Template` import and `_HANDOFF_TEMPLATE_PATH` constant.
- `src/endless/cli.py` — register hidden `internal` group with `template` subgroup and `render <name>` command.

### Deleted
- `docs/templates/handoff.md` — replaced by `templates/handoff.md.tmpl` (and the materialized `.endless/templates/handoff.md.tmpl` after first render in any project).

## Tests

- `internal/templatecmd/template_test.go` (new) — see above.
- `tests/test_handoff.py` (existing) — keeps passing unchanged.
- `tests/test_handoff_internal_cli.py` (new): invoke `endless internal template render handoff` via CliRunner with stdin JSON; assert non-empty output containing `E-NNNN`.
- `tests/test_template_materialize.py` (new): end-to-end via subprocess against a temp project root — assert `.endless/templates/handoff.md.tmpl` materializes on first render, persists user edits on subsequent renders, restores on deletion.

## Sequence note

Foundation. **E-1566** lands immediately after and adds 4 per-type templates + the broadened embed pattern (`templates/**/*.tmpl`). **E-1567** later adds the `children_state` var and the `templates/coordinator/` set.

## Verification

1. `just build` succeeds.
2. `just test` passes (Python + Go).
3. In a worktree, `endless internal template render handoff < /tmp/vars.json` → `.endless/templates/handoff.md.tmpl` appears, populated handoff prints, `.gitignore` is unchanged.
4. Edit `.endless/templates/handoff.md.tmpl` (e.g. prepend "XYZ"), render again → output contains "XYZ".
5. `rm .endless/templates/handoff.md.tmpl`, render again → file reappears with embedded content (no "XYZ").
6. Create `.endless/templates/handoff.md.local.tmpl` with "ABC" prepended, render → output contains "ABC" (overrides the committed `.tmpl`); the committed file is untouched.
7. From `/tmp`, `endless internal template render handoff --project endless < /tmp/vars.json` → resolves via `projects.path`, materializes into the endless project's `.endless/templates/`.
8. `endless task spawn <ready-task>` still works end-to-end.
9. From `/tmp` (no project context, no `--project`), `endless internal template render handoff < /tmp/vars.json` exits non-zero with the expected message; no file written.
