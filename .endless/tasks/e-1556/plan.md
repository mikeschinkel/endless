# Fix dir-name/task-id mismatch in resolved_worktree_endless_go causing global-binary fallback

## Bug

`src/endless/config.py:333` `resolved_worktree_endless_go` is supposed to
return the per-worktree `<worktree>/bin/endless-go` path when `--db sandbox`
is the active context, so `event_bridge` execs the worktree-built binary
(embedding the worktree's `schema.sql`) instead of the global one
(embedding main's). This is the entire point of E-1510 — without it,
additive schema in a branch is silently absent against the sandbox DB
and surfaces as `no such table` errors.

The function never returns the worktree path today. Three call sites
treat `worktree_task_id(cwd)` (digits-only: `"1437"`) and
`worktree_dir_name(cwd)` (full dir name: `"e-1437"`, optionally with a
slug suffix like `"e-1437-add-foo"`) interchangeably:

```python
# config.py:347-358 — resolved_worktree_endless_go
task_id = worktree_task_id(cwd)            # "1437"
if task_id is None:
    return None
if RESOLVED_CONFIG_DIR != sandbox_config_dir(task_id):  # ❌ wrong arg
    return None
root = gated_worktree_root(cwd)
if root is None:
    return None
return root / ".endless" / "worktrees" / f"e-{task_id}" / "bin" / "endless-go"
#                                         ❌ drops the -slug suffix
```

```python
# config.py:381-387 — worktree_python_reexec_target
task_id = worktree_task_id(cwd)            # "1437"
...
worktree = (root / ".endless" / "worktrees" / f"e-{task_id}").resolve()
#                                              ❌ drops the -slug suffix
```

Meanwhile `apply_db_choice("sandbox")` writes the realistic form:

```python
# config.py:258-264
dir_name = worktree_dir_name()             # "e-1437" (or "e-1437-foo")
...
set_db_context(sandbox_config_dir(dir_name))
# RESOLVED_CONFIG_DIR := ~/.cache/endless/sandboxes/e-1437/endless
```

So at line 353 the comparison is `~/.cache/endless/sandboxes/e-1437/endless`
(real) vs `~/.cache/endless/sandboxes/1437/endless` (computed from
digits-only). They never match → `resolved_worktree_endless_go` returns
None → `_resolve_endless_go` falls through to `shutil.which("endless-go")`
→ the global symlink at `/usr/local/bin/endless-go` runs. Silent. No
error. E-1510's whole purpose is defeated.

The two `f"e-{task_id}"` path-construction sites (lines 358 and 387) are
correct by accident for the no-suffix case (e.g. `e-1437`) — they
reconstruct the right dir name from digits. They are wrong for any
slugged worktree (e.g. `e-1437-foo`) because the slug is dropped. Latent
today only because the discovery session's worktree happens to be
unslugged.

## How it slipped past existing tests

`tests/test_event_bridge_worktree_binary.py:253-261` calls
`config.sandbox_config_dir("7777")` (digits-only) to set the fake
`RESOLVED_CONFIG_DIR`, exactly matching what the broken function computes.
The test is asserting a self-consistent broken loop. The realistic value
written by `apply_db_choice` was never exercised.

## Fix

`src/endless/config.py`:

1. `resolved_worktree_endless_go` (line 333): replace `worktree_task_id`
   with `worktree_dir_name` and replace `f"e-{task_id}"` with the
   resulting dir name.

2. `worktree_python_reexec_target` (line 361): same — replace
   `worktree_task_id` with `worktree_dir_name` and `f"e-{task_id}"`
   with the dir name.

3. `worktree_task_id` (line 208): keep as-is. The function is still
   useful for display/lookup of the bare ticket id; only the two path-
   construction call sites misused it. Add a one-line docstring note
   that it's NOT suitable for sandbox/worktree path construction —
   use `worktree_dir_name` for that.

Total change in config.py: ~6 lines.

## Test fixes

`tests/test_event_bridge_worktree_binary.py`:

1. `_make_worktree_layout` (line 23): change the `task_id: str = "9999"`
   parameter to `worktree_dir: str = "e-9999"`. Update internal use to
   `wt = endless / "worktrees" / worktree_dir` (drops the `f"e-{...}"`
   reconstruction).

2. `synthetic_sandbox` fixture (line 45): pass `"e-9999"` (the dir name)
   to `sandbox_config_dir` so the test matches what `apply_db_choice`
   writes in production.

3. `test_resolver_helper_returns_path_for_sandbox` (line 253): same —
   pass `"e-7777"` to both `_make_worktree_layout` and
   `sandbox_config_dir`.

4. Add a new test `test_resolver_helper_returns_path_for_slugged_sandbox`:
   stand up the worktree at `e-7777-add-foo`, set `RESOLVED_CONFIG_DIR`
   to the sandbox for that dir name, assert the returned binary path
   ends in `.../e-7777-add-foo/bin/endless-go` (not `.../e-7777/bin/...`).
   This is the regression guard for the path-construction half of the
   bug.

`tests/test_cli_python_reexec.py`:

1. `worktree_python_reexec_target` callers there construct a worktree
   under `e-9999` (or similar). Verify they still pass after the fix;
   if any rely on `f"e-{task_id}"` reconstruction, update them to use
   the dir name directly.

2. Add a slugged regression here too: `test_reexec_target_handles_slugged_worktree`.

## Verification

1. `just test` — full Python suite green (existing tests adjusted, new
   slugged tests added).
2. From `/Users/mikeschinkel/Projects/endless/.endless/worktrees/e-NNNN`
   with `--db sandbox` active and `bin/endless-go` freshly built:
   `endless --db sandbox task add "test" --phase urgent --project endless`
   followed by `endless --db sandbox sql "SELECT COUNT(*) FROM
   project_next_pending"` returns 1 (proves the worktree binary with
   E-1437's hook code is actually running, not the global one without
   it). This is exactly the verification that failed in the discovery
   session for E-1437.
3. Manually exercise a slugged worktree if any are available (or
   create a throwaway `git worktree add` with a slug to confirm).

## Out of scope

- Refactoring `worktree_task_id` or `worktree_dir_name` semantics. The
  helpers are fine; their misuse is the bug.
- Renaming `sandbox_config_dir`'s parameter — it's named
  `worktree_dir_name` already, so the type signature was honest. The
  callers ignored it.

## Discovery

Surfaced 2026-06-11 while verifying E-1437 end-to-end: the auto-add
hook code in the freshly-built worktree binary was bypassed in favor
of the global binary, leaving `project_next_pending` empty after an
`endless --db sandbox task add ... --phase urgent` invocation. E-1437
landed regardless (Go tests covered the same code path), but the live
CLI verification step was blocked.
