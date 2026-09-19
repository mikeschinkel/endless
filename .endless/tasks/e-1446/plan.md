# E-1446: Sandbox dir name == worktree dir basename

## Context

The per-worktree sandbox dir is named `worktree-e-{task_id}` today — built from task_id digits only. The worktree dir itself may carry a slug suffix (`e-{task_id}-{slug}` from a manual `git worktree add`), but that slug is collapsed away when deriving the sandbox name. Consequence: multiple worktrees for the same task (e.g. a `testing` worktree alongside the main one) share one sandbox and conflate their DB writes.

This task makes the sandbox dir's name equal the worktree dir's basename, so each worktree gets its own sandbox 1-to-1.

## Decision (Mike, 2026-05-26)

**E-1446 produces the final name in one change**: `e-{task_id}[-{slug}]` — slug included AND `worktree-` prefix dropped at the same time. E-1447 (drop the prefix as a separate task) is therefore superseded and marked obsolete.

End-state rule: **sandbox dir basename == worktree dir basename**.

Examples after landing:
- `.endless/worktrees/e-1446` → `~/.cache/endless/sandboxes/e-1446`
- `.endless/worktrees/e-1446-testing` → `~/.cache/endless/sandboxes/e-1446-testing`

## Architecture

The sandbox name is computed/consumed in four production sites and must stay in lockstep — they're independently hardcoded today. Two roles:

- **Producers** create the sandbox dir with this name when a worktree is set up.
- **Resolvers** compute the same name from cwd to find the existing sandbox for `--db worktree` reads/writes (the E-1429 routing path).

A mismatch means writes land in one dir and reads look in another.

## Sites to change

### Producers

1. **`src/endless/worktree_cmd.py:955`** (`_maybe_auto_sandbox_bind`):
   - Change `name = f"worktree-e-{task_id}"` → `name = worktree_path.name`.
   - `worktree_path` is already a `Path` parameter; `.name` is the final dir component.
   - Leave the `task_id` parameter alone (still passed to other call sites in this function).

2. **`Justfile:314-319`** (`dev-sandbox-init` recipe). Replace the task_id-digits parse with a worktree-basename derivation:
   - Drop the `task_id="$(... sed ...)"` line.
   - Set `name="$(basename "$(pwd)")"`.
   - Validate `name` matches `e-NNN[-suffix]` shape; refuse with an actionable error otherwise (same diagnostic role the old digits check played).

3. **`cmd/endless-sandbox/bind.go:96-100`** (`defaultSandboxName` — fallback when no name arg is passed):
   - `"worktree-" + filepath.Base(worktree)` → `filepath.Base(worktree)`.
   - Update the `e.g.` comment.

### Resolvers (the `--db worktree` read path — entirely absent from the prior plan)

4. **`src/endless/config.py:164`** — extend `_WORKTREE_PATH_RE` to capture the full dir name in addition to the task_id digits:
   ```python
   _WORKTREE_PATH_RE = re.compile(
       r"/\.endless/worktrees/(e-(\d+)(?:-[a-z0-9-]+)?)(?:/|$)"
   )
   # group 1 = full dir name (e-NNN[-slug])
   # group 2 = task_id digits (NNN)
   ```

5. **`src/endless/config.py:189-198`** — `sandbox_config_dir(task_id)` becomes `sandbox_config_dir(worktree_dir_name)`. Body: `sandbox = _cache_root() / "endless" / "sandboxes" / worktree_dir_name`. Update docstring.

6. **`src/endless/config.py`** — add a new pure extractor mirroring `worktree_task_id()`:
   ```python
   def worktree_dir_name(cwd: Path | None = None) -> str | None:
       """Worktree dir basename (e-NNN or e-NNN-slug) if cwd is inside a
       .endless/worktrees/e-NNN[-slug] worktree, else None."""
   ```

7. **`src/endless/config.py:201` `worktree_task_id()`** — adjust to `m.group(2)` (group numbering shifts after step 4). Leave the function in place; it's still a useful task-id-from-cwd helper even if production callers move to the dir-name extractor.

8. **`src/endless/config.py:240,246` `set_db_context()`** — for sandbox-name resolution call `worktree_dir_name()` and feed it to `sandbox_config_dir()`. (Leave `worktree_task_id()` here only if still needed for something else; otherwise remove this call site.)

9. **`src/endless/cli.py:1933,1939` `db path --db=worktree`** — same swap.

### Tests

10. **Python tests** — search `tests/` for `worktree-e-` literals and `sandbox_config_dir` callers; update expected names. Add a slug-bearing fixture that proves a slugged worktree resolves to its own sandbox dir, distinct from the un-slugged one.

11. **Go tests** — `internal/monitor/sandbox_test.go:28,68`, `internal/monitor/db_gate_test.go:166`. Drop the `worktree-` prefix from fixture names. Add at least one slug-bearing case.

### Docs / comments

- `config.py:192-195` docstring example.
- `bind.go:96-97` comment example.
- `CLAUDE.md` "Worktree DB sandbox" section: two `~/.cache/endless/sandboxes/worktree-e-NNN/` references → `~/.cache/endless/sandboxes/e-NNN[-slug]/`.

## Existing sandboxes

Not migrated. Live Claude sessions captured `XDG_CONFIG_HOME` pointing at the old `worktree-e-{id}` path at process spawn; renaming under them would break those processes (fail-on-open, or SQLite silently creating an empty DB at the new path). Existing `worktree-e-{id}/` dirs persist until their worktree is dropped, after which they can be reclaimed manually (`endless-sandbox destroy worktree-e-{id}`). New worktrees and any newly-bound sandbox use the new naming.

## Verification

1. `just build` from this worktree.
2. From this `e-1446` worktree:
   - `endless db path --db=worktree` prints `…/sandboxes/e-1446/endless/endless.db` (no `worktree-` prefix).
   - `endless --db worktree task add "probe"` writes into that path; confirm with `endless --db worktree sql "SELECT count(*) FROM tasks WHERE title='probe'"`.
3. **Multi-worktree slug case — the scenario this task fixes**:
   ```sh
   git worktree add .endless/worktrees/e-1446-testing main
   cd .endless/worktrees/e-1446-testing
   just go-work-init && just build && just dev-sandbox-init
   ls ~/.cache/endless/sandboxes/
   # Expect: e-1446/ AND e-1446-testing/ as two distinct sandboxes.
   ```
4. Cross-sandbox isolation: a row inserted via `endless --db worktree` from `e-1446-testing` is NOT visible from `e-1446`'s sandbox.
5. `just test` — Python tests pass.
6. `go test ./internal/monitor/... ./cmd/endless-sandbox/...` — Go tests pass.

## Coordination

- **E-1447** — superseded by Option B; marked obsolete.
- **E-1429** (assumed) — its stored plan text references the old `worktree-e-{task_id}` convention as the sandbox-name resolution rule. That section becomes historical; not editing in-place.
- **E-1281** — original per-worktree sandbox feature; this completes its "1-to-1" promise.
