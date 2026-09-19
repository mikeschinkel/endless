# Plan snapshot writer doesn't commit when invoked from a worktree against the real DB

## Repro

Reproduced 2026-05-20 during E-1426 verify handoff. Two invocations from a Claude session inside `.endless/worktrees/e-1426` with cwd=worktree and `env -u XDG_CONFIG_HOME` (so the command operates on the real `~/.config/endless/endless.db`, not the worktree sandbox):

```sh
endless task update 1426 --text /Users/mikeschinkel/Projects/endless/.endless/worktrees/e-1426/.endless/plans/E-1426.md
endless task update 1426 --status verify --analysis @/tmp/e1426-verify-handoff.md
```

Each produced a snapshot pair under `<worktree>/.endless/plans/snapshots/`:

- `20260520T165628-69f94a93.{json,md}`
- `20260520T180242-db414302.{json,md}`

but did NOT produce the matching `Endless: snapshot plan` commit that historical writes produce (e.g. `44dd520`, `4f13eda` from prior days).

## Discovery

`just land E-1426` refused to proceed with the auto-managed-files gate:

```
Error: worktree for E-1426 has uncommitted auto-managed files; cannot land.

These paths are owned by endless writers that commit them at write time.
Their presence here means a writer is broken or skipped its commit.
```

## Likely root cause

`env -u XDG_CONFIG_HOME` + cwd-inside-worktree splits the routing:

- The snapshot file path resolves to the worktree's `.endless/plans/snapshots/` (cwd-based).
- The auto-commit logic likely resolves checkout/branch via the DB or env in a way that doesn't match the file's actual location, so the add+commit step runs in the wrong checkout or skips.

Same family as E-1419 (worktree session writes to wrong directory for main-DB resolver) — which E-1426 just fixed for sessions but not for plan snapshots.

## Reliable repro

From any tmux pane inside a worktree:

```sh
ENDLESS_SESSION_ID=<your-eid> env -u XDG_CONFIG_HOME endless task update <id> --text <some-plan-file>
```

Then check `git status` inside the worktree for the new snapshot files.

## Fix direction (for the implementer to confirm)

Two candidates:

1. The snapshot commit step should resolve its working directory from the snapshot file's actual path, not from env/DB.
2. When the cwd is in a worktree but the DB op targets the real DB, the write should be redirected to the main checkout's `.endless/plans/snapshots/` so the commit step works against the same tree.

Option 1 is local to the writer; option 2 changes the file-location contract globally. Probably option 1 unless there's a known reason to centralize snapshots.
