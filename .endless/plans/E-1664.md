# E-1664 — A self_dev land always uses the worktree's endless-go binary

## Context

E-1660 fixed `just land` tripping the `task_types` integrity gate when a branch
ADDS a mirrored-enum value (a new `task_types` row): the land applies the schema
change with the worktree binary, then `endless worktree land` emits the
`task.landed` event. Under `--db main` (pinned by `default_db_to_main()`, E-1628),
`_resolve_endless_go()` resolves the **global** `endless-go` via `shutil.which` —
which `just build` hasn't refreshed yet, so its `tasktype` enum lacks the new
constant and `monitor.DB()`'s `VerifyIntegrity` fails closed, forcing a manual
`just install && just land` recovery.

E-1660's fix was a per-shellout PATH-prepend in the Justfile recipe — fragile:
every `endless-go` shellout must individually be pointed at the worktree binary,
and the next one added can silently forget.

## Core insight (the design driver)

Binary selection during a land is **not a choice** the way `--db main|sandbox`
is. `--db` picks between two valid targets. The executable has exactly **one**
correct value during a self_dev land: the **worktree's** binary — it is the
candidate-under-development, and because the land applies the schema change
first, it is the only binary whose embedded schema/enums match the rows just
written. The global is by construction the not-yet-refreshed one mid-land.

So this is an **invariant, not a parameter**: a self_dev land uses the worktree
binary, full stop. No flag, no env var, no override surface. (An env var was
rejected — it leaks across inherited environments into the wrong context, and
`_resolve_endless_go` runs on every schema-mutating shellout. A flag was rejected
— selection isn't a choice to expose.)

**Missing-binary edge → loud error, not silent global fallback.** E-1660's only
use of the global was a silent fallback when the worktree binary was absent.
E-1662 shows that exact fallback is a bug-source ("...fall back to the GLOBAL
binary... this is also what left e-1643 exposed to the stale-global-binary
integrity skew"). So an absent worktree binary during a self_dev land is a loud
failure ("worktree not built — run `just build`"), enforcing the self_dev
"exercise candidate code" invariant. A *fully reaped* worktree can't be landed
anyway — `git worktree` won't list it, so `land_worktree` fails at
`_branch_for_task` before any binary is needed. No legitimate global-fallback
path remains. (Slightly stricter than strictly necessary for a no-schema-change
land where the global would also work — but that strictness is aligned with
E-1662: an unbuilt self_dev worktree is a bug to surface, not mask.)

## Approach

### 1. `_resolve_endless_go()` gains an explicit `override` parameter

`src/endless/event_bridge.py:27` — `def _resolve_endless_go(override: str | None
= None)`. When `override` is given, validate it is a file and executable; return
it; raise a loud `click.ClickException` naming the path (`~`-relative via the
existing lines 39-42 helper) if invalid. When `None`, existing behavior is
unchanged (`resolved_worktree_endless_go()` → `shutil.which`). Pure internal
plumbing — no env, no CLI surface.

### 2. Thread it through the land's emit only

- `emit_event(...)` (event_bridge.py:55) gains `endless_go_bin: str | None =
  None`, passed to `_resolve_endless_go(override=endless_go_bin)`.
- `_record_landing(...)` (worktree_cmd.py:~1264, the land's emit wrapper) gains
  `endless_go_bin` and forwards it to `emit_event`.
- `apply_change` / `backup_db` are **untouched** (separate top-level Justfile
  invocations; out of E-1664 scope — see "Left alone" below).

### 3. `land_worktree` computes the invariant via a testable helper

Add a small seam so the script can assert this without standing up a full land:

`src/endless/worktree_cmd.py` — `def _resolve_land_endless_go(worktree_path:
Path, project_root: Path) -> str | None:` returns `None` for a non-self_dev
project (`config.project_is_self_dev(project_root)` is False ⇒ global, unchanged);
otherwise computes `worktree_path / "bin" / "endless-go"`, and if it is not a
file / not executable raises a loud `click.ClickException` ("self_dev worktree
isn't built — run `just build` in the worktree before landing"), else returns its
`str`. `land_worktree` (line 1306), past the dry-run guard, calls this helper and
passes the result as `endless_go_bin` to `_record_landing`. The three branches
(self_dev+present → path, self_dev+missing → loud error, non-self_dev → None) are
exactly what e-1664-verify.sh exercises.

### 4. Justfile: remove E-1660's PATH-prepend AND its guard

`justfile:166` land recipe — the `if [ -x "$wt/bin/endless-go" ]; then PATH=…
endless worktree land …; else …; fi` block becomes a plain
`endless worktree land "$tid"`. The product code now enforces the worktree binary
for self_dev lands and fails loudly if unbuilt.

## Files to modify

- `src/endless/event_bridge.py` — `override` param + validation in
  `_resolve_endless_go`; `endless_go_bin` param on `emit_event`; docstrings.
- `src/endless/worktree_cmd.py` — `_resolve_land_endless_go` helper; call it in
  `land_worktree`; `endless_go_bin` thread through `_record_landing`.
- `justfile` — plain land call (drop E-1660 block).
- `tests/tasks/e-1664-verify.sh` — **the single verification surface** (deliverable;
  ad-hoc reference-style, modeled on `tests/tasks/e-1660-verify.sh` — NOT the
  E-1596 `verify.toml` system the other session is building).

## Left alone / out of scope (with reasons, not silent)

- **apply-change / backup E-1510 PATH-prepends** stay. They are separate
  processes, so no per-invocation mechanism can be "set once" across them; true
  set-once de-fragilization only comes from consolidating the land steps into one
  process — the deferred **project-configurable land hook**, which belongs in the
  existing **extension-model family** (E-1014 shipped `.endless/extensions/use.sh`;
  E-1086 worktree-init hook; E-1188 the three-tier model design). Captured there,
  not built here.
- **E-1613** (add `ENDLESS_GO_BIN` env override to `_resolve_endless_go` so
  verify scripts can pin the artifact-under-test) should be **marked obsolete**.
  Both its halves are dead: (a) its env-var mechanism is exactly the leakage
  approach rejected here; (b) its only concrete driver is already solved — the
  verify-suite scripts (e.g. `tests/tasks/e-1660-verify.sh`) invoke the worktree
  binary by absolute path (`WT_BIN="$REPO_ROOT/bin/endless-go"`), bypassing
  `_resolve_endless_go` entirely and already loud-erroring if unbuilt. Nothing
  remains for the override to add. E-1664's `override` param is internal land
  plumbing, not a verify-script surface, so it neither implements nor revives
  E-1613.

## Verification

The verification is a single committed script. The handoff to Mike is exactly:

```
esu
./tests/tasks/e-1664-verify.sh
```

`tests/tasks/e-1664-verify.sh` (self-contained; builds/asserts the worktree
binary itself, isolates via temp `HOME`/`XDG_CONFIG_HOME` + a schema-seeded DB,
invokes the worktree `endless-go` by absolute path, prints pass/fail per check +
a summary, exits 0 all-pass / 1 any-fail / 2 setup error). Checks:

- **Gate still armed (not weakened):** an emit against a DB carrying a phantom
  `task_types` row with no enum constant MUST fail with the exact integrity error
  (matched case-accurately per E-1596's lesson).
- **Matching-enum clears the gate:** the same emit against a clean schema-seeded
  DB gets past integrity.
- **Override honored:** `_resolve_endless_go(override=<valid>)` returns it;
  `override=<missing/non-exec>` raises a loud error; `override=None` ⇒ unchanged
  global/sandbox resolution (Python one-liners via `uv run`).
- **Land invariant:** `_resolve_land_endless_go` returns the worktree binary path
  when self_dev + present, raises the loud "not built" error when self_dev +
  missing, returns `None` for a non-self_dev project.
- **Justfile regression guard:** the `land` recipe invokes `endless worktree land
  "$tid"` plainly — no `PATH="$wt/bin:…"` wrapper and no E-1660 guard block
  around the land call.

## Ledger coordination (post-implementation, confirm with Mike)

- E-1664 already `cleans_up` E-1660.
- Mark **E-1613 obsolete** with a note recording why (env approach rejected;
  verify scripts pin the binary by absolute path, bypassing `_resolve_endless_go`).
  It sits under E-1596, so confirm the disposition with Mike; exact verb/status
  resolved against `endless guide` at implementation time.
- Optionally note the land-hook requirement against E-1188 (extension model) so
  the apply-change consolidation has a tracked home — confirm before editing his
  design task.
