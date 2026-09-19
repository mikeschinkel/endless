# E-1589 — Make handoff template materialization self_dev-aware

## Symptom

`just land` refuses with "main has uncommitted user changes" naming
`.endless/templates/`. Hit on three consecutive task landings. The files are
untracked copies of the handoff templates that appear in the **main checkout**.

## Root cause

`render_spawn_handoff` (`src/endless/task_cmd.py:3194`) shells out to
`endless-go template render handoff/<type>` **without `--project` and without
setting `cwd`**. So `resolveProjectRoot("")` (`internal/templatecmd/template.go:107`)
falls to `projectRootFromCwd()`, which walks up from the **invoking process's
cwd** to the nearest `.endless`. When `task spawn` / `task show --handoff` runs
from the main checkout (the normal case), that resolves to **main**, and
`materializeIfMissing` (`template.go:80`/`188`) writes the embedded template into
`<main>/.endless/templates/handoff/<type>.md.tmpl` as a side effect of a render
(a read). Those files are neither gitignored nor committed → untracked → block
`just land`.

Surfaced now because E-1566 split the single handoff template into four per-type
variants (`task/bug/research/epic`); none had been materialized, so the first
render of each type from main drops a new untracked file.

## Why not the obvious fixes

- **Commit / auto-commit the files in endless's own repo** re-creates a quieter
  bug: lookup order is `.local.tmpl` → `.tmpl` (committed) → embedded, so a
  committed on-disk copy **shadows the embedded source** that endless development
  edits. `materializeIfMissing` never overwrites, so future edits to
  `internal/templatecmd/templates/` would have no effect — the renderer keeps
  using the stale committed copy.
- **Gitignore `.endless/templates/`** has the same shadow problem (present-but-
  ignored still shadows embedded) and contradicts the "committed copy" design for
  consumer projects.

## Fix: gate materialization on `self_dev`

`self_dev` is exactly the flag that means "this repo *is* endless" vs "this repo
*uses* endless." Endless's own templates are the embedded source; consumer
templates live on disk.

| Project | render behavior |
|---|---|
| `self_dev: true` (endless itself) | **Skip materialize — render from embedded.** No file written to main, no shadow, land never blocks. |
| `self_dev: false` (consumer projects) | **Write-on-read + auto-commit** the materialized file. Zero-thought UX: the on-disk copy is the customizable template, committed so it is tracked/discoverable. |

The write-on-read convenience is preserved for end users (the decided, desirable
behavior); the burden of editing embedded templates correctly falls on the
endless developer, which is acceptable.

## Implementation

In `internal/templatecmd/template.go`:

1. Export a self_dev check from `internal/monitor` (thin wrapper over the
   existing unexported `projectIsSelfDev` at `internal/monitor/db.go:208`), e.g.
   `monitor.ProjectIsSelfDev(root string) bool`.
2. In `runRender`, after `resolveProjectRoot`:
   - if `monitor.ProjectIsSelfDev(projectRoot)` → **skip** `materializeIfMissing`
     (rendering still works: `loadTemplate` already falls back to embedded).
   - else → `materializeIfMissing`, then **auto-commit** the written file.
3. Auto-commit (consumer projects only), keep it narrow and safe:
   - commit **only the materialized file's pathspec** (`git -C <projectRoot> add
     <relpath>` then `git commit -m "<msg>" -- <relpath>`), never a blanket
     `git add -A`, so unrelated working-tree changes are untouched.
   - guard: only when `<projectRoot>` is a git work tree; on any git failure,
     log and continue (a failed auto-commit must NOT fail the render).
   - message e.g. `Endless: materialize handoff template <name>`.

No internal `E-NNNN` IDs in any user-facing string.

## Verification

- **self_dev (endless):** from the main checkout, `endless task show <id>
  --handoff` (or a spawn) produces output and creates **no** `.endless/templates/`
  files; deleting any embedded edit shows the change takes effect (no shadow).
- **consumer (self_dev unset) fixture:** first render writes
  `.endless/templates/handoff/<type>.md.tmpl` AND commits exactly that file
  (clean `git status` afterward, one new commit touching only that path); second
  render is a no-op (file exists). In a non-git dir, render still succeeds and
  leaves the file untracked without erroring.
- Existing `internal/templatecmd` tests still pass; add cases for the self_dev
  skip and the consumer auto-commit.

## Cleanup

Remove the stray `.endless/templates/` left in main by prior occurrences and by
the spawn of this task itself (`rm -rf .endless/templates` from the main
checkout) before landing.
