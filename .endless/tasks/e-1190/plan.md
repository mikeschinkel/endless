# E-1190: Add `endless worktree add` verb

Parent: E-971. Blocked by: E-1186 (the "no create verb" lock must be lifted first to keep the design honest). Relates to: E-1188 (extension model), E-1189 (claude-settings Go port), E-1195 (lock-model review).

## Command surface

```
endless worktree add E-NNN [--purpose PURPOSE] [--ref REF]
```

| Arg | Required | Default | Description |
|---|---|---|---|
| `E-NNN` | yes | — | Task ID. Must exist. |
| `--purpose` | no | `task` | One of `task`, `verify`. Other values rejected. |
| `--ref` | no | auto | Git ref to branch from. Default: `main` for `task`; auto-detected for `verify`. |

## Naming

| Purpose | Branch | Dir |
|---|---|---|
| `task` | `task/NNN-<auto-slug>` | `.endless/worktrees/e-NNN` |
| `verify` | `task/NNN-verify` | `.endless/worktrees/e-NNN-verify` |

`<auto-slug>` derives from the task title: lowercase, kebab-case, ascii-only, capped at ~30 chars. No `--slug` flag — exposing it adds friction without value; `endless task show E-NNN` is the lookup if needed.

## Auto-detect base ref for `--purpose verify`

When `--ref` is not given:

1. If `task/NNN-<slug>` exists AND is **not** in `git branch --merged main` → use that ref. (Concurrent verify: the variant branch starts from the dev branch's tip and tracks ongoing work.)
2. Otherwise → use `main`. (Post-merge verify: the dev branch is already merged or absent.)

`--ref REF` always overrides. Surfacing the detection result on stderr is helpful but not required.

## Steps performed

1. Validate task `E-NNN` exists.
2. Compute branch + dir names from `--purpose`.
3. Resolve base ref (`--ref` or auto-detect).
4. `git worktree add -b <branch> <dir> <base>`.
5. Write `<dir>/.endless/worktree.json`:
    ```json
    {
      "kind": "task",
      "task_id": "E-NNN",
      "purpose": "task" | "verify",
      "base_branch": "<resolved-base>",
      "branch": "<branch>",
      "created_at": "<time.Now().UTC().Format(time.RFC3339)>"
    }
    ```
6. Run baked-in: claude-settings generation (Go-ported per E-1189; falls back to `just claude-settings-init` until that lands).
7. Run project extension hook (per E-1188's design — single `worktree-init` for v1; fire-and-forget on failure with a warning to stderr).

Order matters: claude-settings before project hook. If a project hook ever spawns Claude inside the new worktree, the worktree's `.claude/settings.json` should already exist so the spawned session uses the worktree's hook binary.

## `kind` stays `task`

`verify` is a `purpose` (a CLI concern affecting branch/dir naming) on `kind=task` worktrees. The schema enum doesn't grow — verify worktrees are same-task variants, not a new conceptual category.

## Idempotency

If the target dir or branch already exists, exit 1 with a specific message identifying which collided. No silent no-op. No `--force` flag in v1; explicit removal first via `git worktree remove` + `git branch -D`.

## Locking / session adoption

Out of scope for this verb. The verb does not touch `.endless/worktree.lock`. Whether a Claude session that later enters the worktree claims a lock is governed by E-971's SessionStart-hook model — and that model itself is up for review under E-1195.

## Errors

| Condition | Exit | Message |
|---|---|---|
| Task not found | 1 | `Task E-NNN not found` |
| Dir exists | 1 | `Worktree at <path> already exists; remove it first` |
| Branch exists | 1 | `Branch <name> already exists; remove the branch or pick a different --purpose` |
| `--ref` invalid | 1 | (surface git's error verbatim) |
| Not in main checkout | 1 | `Run from main checkout, not a worktree` |
| `--purpose` unrecognized | 1 | `Unknown purpose '<value>'; allowed: task, verify` |

## Verification

Synthetic test plan, run from a scratch checkout:

1. With a fresh test task `E-NNN`, run `endless worktree add E-NNN --purpose verify`. Expect: dir `e-NNN-verify`, branch `task/NNN-verify`, companion file populated, `claude-settings-init` ran, project hook ran (or warned absent).
2. Re-run the same command — expect exit 1 with the dir-exists message.
3. With `--ref feature-x` (where `feature-x` is an existing branch) — expect verify branch starts from `feature-x`'s tip.
4. With a nonexistent task — expect exit 1, task-not-found.
5. From inside an existing worktree — expect exit 1, main-checkout error.
6. With `--purpose task` (default) on a task whose dev worktree was previously dropped — expect dir `e-NNN`, branch `task/NNN-<auto-slug>`, base = `main`.

## Out of scope

- The lock model itself (E-1195).
- An `endless worktree remove` counterpart (separate task if needed).
- Auto-removal of verify worktrees after testing completes.
- Multi-task worktrees.
- A `--force` flag for idempotency override (add only when a real use case emerges).
