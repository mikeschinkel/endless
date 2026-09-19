# E-1456 plan: Rename `worktree_sandbox` config key to `self_dev`

## Context

The `.endless/config.json` key `worktree_sandbox` (true when a project's worktrees should sandbox their DB writes) is accurate but obscures intent. The flag is ON for endless-developing-endless and OFF for the ~40 downstream projects that use endless as a tool. Rename to `self_dev` — that's the actual semantic: "this project is endless self-development, so its worktrees should sandbox writes to avoid polluting the real DB."

The flag is internal/dev-only. A cryptic name is fine; clarity of intent beats consistency with the older terminology.

E-1429 (mandatory `--db main|sandbox` flag inside self-dev worktrees) and E-1476 (flag unification) have landed; this plan uses `--db main`/`--db sandbox` in verification.

## Implementation sites

**Config reader** — `src/endless/config.py:110-121`:

```python
def project_wants_worktree_sandbox(project_path: Path) -> bool:
    """True if the project opts into per-worktree DB sandboxing.

    Set by adding `"worktree_sandbox": true` to <project>/.endless/config.json.
    Endless's own config has this enabled so dev-time worktrees don't pollute
    the user's real DB; downstream projects using endless as a tool leave it
    unset so their tasks land in the real DB.
    """
    cfg = project_config_read(project_path)
    if cfg is None:
        return False
    return bool(cfg.get("worktree_sandbox", False))
```

Rename function to `project_is_self_dev`. Update the docstring to describe the new key. Read `cfg.get("self_dev", False)`.

**E-1429 routing comment** — `src/endless/config.py:156`:

```python
# Inside a self-dev worktree (a .endless/worktrees/e-NNN checkout of a project
# whose config.json sets "worktree_sandbox": true), the implicit XDG-driven DB
# routing is replaced by a mandatory, per-invocation --db main|sandbox flag.
```

Update `"worktree_sandbox": true` → `"self_dev": true` in the comment.

**`gated_worktree_root` docstring** — `src/endless/config.py:211`:

```python
def gated_worktree_root(cwd: Path | None = None) -> Path | None:
    """Project root if cwd is inside a self-dev worktree of a worktree_sandbox
    project (so --db is required), else None."""
```

Update `worktree_sandbox` → `self_dev` in the docstring.

**Caller** — `src/endless/worktree_cmd.py:942-955`:

```python
def _maybe_auto_sandbox_bind(project_root: Path, worktree_path: Path, task_id: int) -> None:
    """...
    Triggered by `worktree_sandbox: true` in the project's .endless/config.json
    (see config.project_wants_worktree_sandbox). Endless's own config has the
    flag set ...
    """
    ...
    if not config.project_wants_worktree_sandbox(project_root):
        return
```

Update the docstring to mention `self_dev`. Update the call to `config.project_is_self_dev(project_root)`.

**Error string** — `src/endless/worktree_cmd.py:961`:

```python
"worktree-sandbox setup skipped. Run 'just build' then "
```

Change to `"sandbox setup skipped. Run 'just build' then "`.

**Documentation** — `CLAUDE.md`:

- Line 26 in worktree-setup steps: comment `# see "Worktree DB sandbox" below` — update section heading reference.
- Line 50 — section heading `## Worktree DB sandbox — E-1281` — rename to `## Self-dev DB sandbox — E-1281`.
- Line 56 — `Endless's own .endless/config.json sets "worktree_sandbox": true. Downstream projects ...` — update to `"self_dev": true`.

**Endless's own config** — `/Users/mikeschinkel/Projects/endless/.endless/config.json`:

```json
{ "worktree_sandbox": true, ... }
```

Rewrite to `{ "self_dev": true, ... }`.

## Verification

1. Build (`just build`).
2. Confirm endless's own `.endless/config.json` now has `self_dev: true` and behaves as before — sandbox routing still activates for new worktrees:
   ```sh
   git worktree add /tmp/test-self-dev-rename main
   cd /tmp/test-self-dev-rename
   just go-work-init
   just dev-sandbox-init
   ls ~/.cache/endless/sandboxes/  # expect a new dir created (sandbox routing fired)
   ```
3. Confirm `--db` gating still triggers inside a self-dev worktree (E-1429 path):
   ```sh
   cd /tmp/test-self-dev-rename
   endless task list  # expect: error requiring --db main|sandbox
   endless --db main task list  # expect: success
   ```
4. Grep the repo: no remaining `worktree_sandbox` references in active code (only historical commit messages / ledger entries).
5. Run `just test` — all tests pass.

## Closing

After verification, file the outcome and flip to `verify`. Do not auto-land — Mike confirms.

## Related

- E-1429 — explicit `--db main|sandbox` routing flag. Landed; this plan touches its `config.py` references too.
- E-1476 — unified the `--db` flag to position-agnostic and renamed `worktree → sandbox` at the flag level. Landed.
- E-1447 — drops the `worktree-` prefix from sandbox dir names. Same renaming family. (Per Mike: E-1446's session absorbs E-1447's scope; E-1447 will be marked obsolete.)
- E-1281 — the original sandbox-routing feature this task renames.
