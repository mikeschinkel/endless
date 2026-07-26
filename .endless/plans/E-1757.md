# E-1757 — Build the unified `project init` command; make `register` an alias

## Context

The `project` command group (E-1756) now hosts the project-management verbs.
`register_project()` in `src/endless/register.py` **already** unifies the two halves
this task cares about: it writes the DB `projects` row **and** scaffolds on-disk files
(`.endless/config.json`, `.gitignore`, `.endless/tmp/`) in one idempotent pass. So E-1757
is not building unification from scratch — it is (1) surfacing that unified behavior under
the canonical verb `project init` with `project register` as an alias, and (2) freezing the
canonical `.gitignore` set now that the sibling tasks (E-1755 pruned `sessions/`, ED-1530
confirmed `worktree.json`/`worktree.lock` as durable-kept) have settled.

## Changes

### 0. `src/endless/register.py` — make the config write merge-preserving
Replace the fixed-dict `config.project_config_write(...)` at lines 176–185 with a
read-merge-write so re-running `project init` never clobbers unmanaged keys:
```python
cfg = config.project_config_read(project_path) or {}
cfg.update({
    "name": name, "label": label, "description": description,
    "language": language, "status": status,
})
cfg.setdefault("dependencies", [])
cfg.setdefault("documents", {"rules": []})
config.project_config_write(project_path, cfg)
```
Preserves `self_dev`, `matchers`, and any other keys; preserves existing `dependencies`/
`documents` on update while still defaulting them on a fresh init. Honors "re-running is
always safe; don't clobber."

### 1. `src/endless/register.py` — freeze the canonical `.gitignore` set
Expand `GITIGNORE_ENTRIES` (currently 2) to the canonical **4**:
```python
GITIGNORE_ENTRIES = [
    ".endless/worktrees/",
    ".endless/tmp/",
    ".endless/worktree.json",   # durable identity — kept (ED-1530)
    ".endless/worktree.lock",   # ownership lock — kept (ED-1530)
]
```
- **Not** `.endless/sessions/` — pruned by E-1755; never scaffold it into new projects.
- Update `GITIGNORE_BLOCK_HEADER` string: `register` → `init`.
- Rewrite the lines 27–34 comment: worktree.json/lock are now **included** (ED-1530 settled
  their status as durable), sessions/ intentionally excluded.
- `scaffold_gitignore()` logic is unchanged — it's already set-membership idempotent, so the
  two new lines append once and never duplicate.

### 2. `src/endless/cli.py` — `init` primary, `register` alias
- Rename the `@project_cmd.command("register")` callback (lines 320–336) to `init`; keep the
  identical arguments/options and `register_project(...)` delegation. Docstring: "Initialize
  (register) a directory as a project — idempotent; safe to re-run."
- Add the alias by registering the **same command object** under `register`:
  `project_cmd.add_command(init, name="register")`. Both verbs run one callback → provably
  identical behavior, no duplicated option list.
- Add `"init"` to the moved-stub loop (line 432) alongside `register`, so bare top-level
  `endless init` hard-errors with the same redirect to `endless project init` (consistency
  with the E-1756 stub contract). `register` stays in that loop unchanged.

### 3. `tests/tasks/e-1757-verify.sh` — the verify suite
Model on `tests/tasks/e-1756-verify.sh` (identical skeleton: `set -u`, color/`section`/
`report_pass`/`report_fail`/`summary`, `endless() { uv run endless "$@" --db sandbox; }`,
`main()` git-toplevel + `uv` check + banner). This task DOES need scratch isolation (it writes
real files + a DB row), so add a `mktemp -d` scratch dir per project and a `trap` that both
`rm -rf`s the dirs and unregisters the test rows from the sandbox DB. Unique per-run names
(e.g. `e1757init$$`, `e1757reg$$`) keep the persistent sandbox DB clean across re-runs.

Checks:
- `project init --help` and `project register --help` both succeed (alias reachable).
- **Fresh `project init <scratch> --infer --name … --status active`**: exactly 1 `projects`
  row for that path (`endless sql --db sandbox "SELECT count(*) …"`); `.endless/config.json`
  exists and carries the name; `.endless/tmp/` created; `.gitignore` contains all **4**
  canonical entries and **not** `.endless/sessions/`.
- **Idempotent re-run**: still exactly 1 row (no dup); each gitignore entry appears exactly
  once (grep -c == 1 per line).
- **No-clobber**: after the first init, inject an unmanaged key into config.json (via
  `uv run python` JSON edit, e.g. `self_dev: true`); re-run init; assert the key survives.
- **Alias parity**: `project register` on a second scratch dir yields the identical row +
  config.json + 4 gitignore entries.
- Handoff line on completion: `esu && ./tests/tasks/e-1757-verify.sh`.

## Verification
- Run `./tests/tasks/e-1757-verify.sh` → all checks pass (folded as the fail-fast suite).
- Project-wide regression: `just test` (Python), lint, `just build` — all green.

