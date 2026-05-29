# E-1510 — Route schema-mutating endless ops through the worktree's `endless-go`

## Context

`--db sandbox` (E-1429) routes the DB *data path* into the worktree's per-worktree sandbox, but the `endless-go` binary it invokes is still resolved via `shutil.which("endless-go")` — which on every dev machine is the global symlink at `/usr/local/bin/endless-go` → main's `bin/endless-go`. Main's binary embeds main's `schema.sql`; any additive schema the worktree's branch declares is silently absent. Downstream ops that reference the new tables then fail with `no such table`.

The same gap exists in the `just land` recipe: it calls `endless db backup` and `endless db apply-change "$wt/$f"` to apply a branch's schema-change files before the ff-merge, and those calls also reach for the global binary — applying changes against main's schema baseline instead of the worktree's.

This was surfaced 2026-05-29 verifying E-1378 (the per-worktree dev cycle now requires `export PATH="$PWD/bin:$PATH"` as a manual workaround; E-1510 removes the need for it).

Two narrowly-scoped changes:

1. **Python wrapper** (`event_bridge.py`): when `--db sandbox` is the resolved DB context and cwd is inside a self-dev worktree, exec `<worktree>/bin/endless-go` instead of the PATH-resolved global. Apply at `emit_event`, `apply_change`, and `backup_db` (the three schema-mutating shell-outs). Fail loudly with a build hint if the worktree binary is missing — that's a broken state, not a fallback case.
2. **Justfile `land` recipe**: prepend `$wt/bin` to `PATH` on the two `endless db ...` calls so the Python wrapper's internal `shutil.which("endless-go")` resolves to the worktree binary during the schema-application step.

Out of scope: other Python `endless-go` callsites (`tmux_cmd`, `setup`, `cli serve`, `session_cmd`'s session-query). Those are read or orchestration paths; schema baseline doesn't bite them today. If a follow-up shows otherwise, file separately.

## Changes

### 1. `src/endless/config.py` — add resolver helper

After `go_db_context_args()`, add:

```python
def resolved_worktree_endless_go(cwd: Path | None = None) -> Path | None:
    """Path to <worktree>/bin/endless-go when --db sandbox is the active DB
    context AND cwd is inside a self-dev worktree, else None.

    Used by event_bridge to prefer the worktree-built binary (which embeds
    the worktree's schema.sql) over the PATH-resolved global. Does not check
    existence — callers handle the missing-binary case explicitly so the
    error message can prescribe `just build`.
    """
    if RESOLVED_CONFIG_DIR is None:
        return None
    task_id = worktree_task_id(cwd)
    if task_id is None:
        return None
    # Only fire when RESOLVED_CONFIG_DIR is EXACTLY the sandbox path for this
    # worktree. Anything else (--db main, conftest's tmp config dir, a stray
    # external override) routes through PATH as before.
    if RESOLVED_CONFIG_DIR != sandbox_config_dir(task_id):
        return None
    root = gated_worktree_root(cwd)
    if root is None:
        return None
    return root / ".endless" / "worktrees" / f"e-{task_id}" / "bin" / "endless-go"
```

Detection condition matches the plan text "when --db sandbox is set" exactly: `RESOLVED_CONFIG_DIR == sandbox_config_dir(task_id)` is the post-condition of `apply_db_choice("sandbox")`. `--db main`, no `--db`, and conftest's tmp config dir all bypass.

### 2. `src/endless/event_bridge.py` — factor binary resolution

Add at module scope (near `_ATTRIBUTION_REQUIRED`):

```python
import os  # add to existing imports

def _resolve_endless_go() -> str:
    """Resolve which endless-go binary to exec for a schema-mutating call.

    Under --db sandbox in a self-dev worktree, prefer <worktree>/bin/endless-go
    so the embedded schema.sql matches the sandbox DB. Otherwise fall back to
    the PATH-resolved global. Fails loudly if --db sandbox is active but the
    worktree binary is missing — silently using main's binary would re-introduce
    the schema-baseline mismatch this routing exists to prevent.
    """
    wt_bin = config.resolved_worktree_endless_go()
    if wt_bin is not None:
        if not wt_bin.is_file() or not os.access(wt_bin, os.X_OK):
            raise click.ClickException(
                f"--db sandbox is active but the worktree's endless-go "
                f"binary is missing or not executable:\n  {wt_bin}\n"
                f"Run `just build` from the worktree."
            )
        return str(wt_bin)
    found = shutil.which("endless-go")
    if not found:
        raise click.ClickException("endless-go binary not found on PATH.")
    return found
```

Then replace the three `shutil.which("endless-go")` blocks (lines 126–130, 171–175, 199–203) with a single `event_bin = _resolve_endless_go()` call. Drop the duplicated "not found on PATH" `ClickException`s — the helper raises.

### 3. `justfile` — `land` recipe PATH-prepend

In the `if [ -n "$changes" ]; then` block (around lines 131–141), prefix both invocations with `PATH="$wt/bin:$PATH"`:

```sh
echo "→ Backing up DB before applying schema changes"
PATH="$wt/bin:$PATH" endless db backup
for f in $changes; do
    case "$f" in
        *.sql|*.go)
            echo "→ Applying schema change: $f"
            PATH="$wt/bin:$PATH" endless db apply-change "$wt/$f"
            ;;
    esac
done
```

Per-call prefix (not a recipe-wide `export PATH`) so subsequent `endless worktree land` continues using main's binary — that one's orchestration code, not schema mutation.

### 4. Tests

Add `tests/test_event_bridge_worktree_binary.py` covering `_resolve_endless_go`:

- `RESOLVED_CONFIG_DIR` set to a synthetic sandbox dir + cwd inside a `.endless/worktrees/e-NNN/` path under a project that opts into `worktree_sandbox` + worktree binary exists → emit/apply/backup `subprocess.run` cmd[0] == the worktree binary path
- `RESOLVED_CONFIG_DIR == main_config_dir()` → cmd[0] from `shutil.which` (PATH fallback)
- `RESOLVED_CONFIG_DIR is None` → cmd[0] from `shutil.which`
- `RESOLVED_CONFIG_DIR` set to sandbox + cwd in worktree + binary **missing** → `ClickException` with "Run `just build`" hint
- cwd NOT in a self-dev worktree (even with `RESOLVED_CONFIG_DIR` set) → PATH fallback

Use `tmp_path` to build a fake worktree layout: `<tmp>/proj/.endless/config.json` with `worktree_sandbox: true`, `<tmp>/proj/.endless/worktrees/e-9999/bin/endless-go` (chmod +x). `monkeypatch.chdir(...)` into the synthetic worktree, `monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", ...)`, stub `subprocess.run`, call `event_bridge.apply_change("ignored")`.

Note on existing-test compatibility: `conftest.py` sets `RESOLVED_CONFIG_DIR` to a tmp config dir (`tmp_path/.config/endless`). With the stricter sandbox-equality check, that dir != `sandbox_config_dir(1510)`, so `resolved_worktree_endless_go` returns None and existing tests continue to hit the PATH-fallback branch (where they stub `shutil.which`). No conftest changes needed. The new tests build their own RESOLVED_CONFIG_DIR by `monkeypatch.setattr(config, "RESOLVED_CONFIG_DIR", config.sandbox_config_dir("9999"))` and a synthetic worktree at `<tmp>/proj/.endless/worktrees/e-9999/bin/endless-go`.

## Critical files

- `src/endless/config.py` (add helper)
- `src/endless/event_bridge.py` (3 callsites + helper)
- `justfile` (land recipe, 2 lines)
- `tests/test_event_bridge_worktree_binary.py` (new)

## Verification

From the worktree, after `just build`:

```bash
# 1. Confirm existing tests pass (PATH fallback branch unaffected):
just test  # includes tests/test_event_bridge.py
just test tests/test_event_bridge_worktree_binary.py

# 2. End-to-end: show the worktree binary is exec'd under --db sandbox.
#    Reuse the E-1378 verify seed (E-1378 is now assumed; its change file
#    is in this branch's main-relative diff range, but the file itself
#    exists in main already since E-1378 landed — so apply-change is a
#    no-op replay safe for this test). Without our fix, the path would
#    be /usr/local/bin/endless-go; with it, <wt>/bin/endless-go.
ENDLESS_DEBUG=1 endless --db sandbox db apply-change \
    internal/schema/changes/e-1378-extract-decisions.sql 2>&1 \
    | grep -i endless-go

# Expected: the path printed (or otherwise visible) is the worktree's
# bin/endless-go, not /usr/local/bin/endless-go.
# If ENDLESS_DEBUG isn't wired for this, fall back to lsof/ps during the call,
# or temporarily print event_bin in event_bridge.

# 3. Justfile: dry-run the land recipe to see the unrolled commands.
just -n land E-1510 2>&1 | grep -E "endless db (backup|apply-change)"
# Expected: each line is prefixed with PATH="$wt/bin:$PATH" (or the
# variable-expanded equivalent).

# 4. Negative path: simulate missing worktree binary.
mv bin/endless-go bin/endless-go.bak
endless --db sandbox sql "SELECT 1" 2>&1 | head -5
# Expected: ClickException with "Run `just build` from the worktree."
mv bin/endless-go.bak bin/endless-go

# 5. --db main is unaffected (always uses global):
endless --db main task show E-1510 >/dev/null && echo "ok"
```

Manual `just land` against a real synthetic task is out of scope here (Mike runs land for E-1510 itself, which exercises the recipe path naturally).
