# Worktrees and Commit-to-Main Policy

Every task you claim gets its own git worktree (E-1168). All work happens there — never in the main checkout's working tree.

## Why

- **Isolation.** Multiple sessions can work on multiple tasks concurrently without stepping on each other.
- **Clean main.** The main checkout's working tree stays clean (E-1200). Switching between tasks doesn't require stashing.
- **Reviewable history.** When the task lands, its commits arrive on `main` together via `worktree land`.

## Auto-creation on claim

```bash
endless task claim <id>
```

This:

1. Sets the task status to `in_progress`.
2. Binds the task to your session.
3. Creates a git worktree at `.endless/worktrees/<slug>/` rooted on a fresh branch `task/<id>-<slug>`.
4. Writes companion metadata to `.endless/worktree.json` (task_id, base_branch, branch, timestamp).

If the task has an uncommitted plan file in main (`.endless/plans/E-NNNN.md`), claim refuses with a message telling you to commit it first (E-1169). Plan files are global-config artifacts and commit directly to main is allowed for them.

## Getting into the worktree

```bash
eval "$(endless shell-init)"     # once per shell
eswt <id>                        # cd to the worktree for task <id>
```

Or directly:

```bash
cd "$(endless worktree for-task <id>)"
```

## Inspecting

```bash
endless worktree list                       # all worktrees for the current project
endless worktree show <slug-or-id>          # detail for one
endless worktree current                    # what worktree is cwd in (or "none")
endless worktree for-task <id>              # resolve a task ID to its path
```

## Landing the work

When the task is verified (or you're using `assume`):

```bash
endless worktree land <id>
endless worktree land <id> --dry-run        # preview without making changes
```

`land` performs:

1. Auto-commit endless-managed dirt (verbs.json, ledger entries) — these auto-commit to main per E-1141.
2. Rebase the task branch onto current `main`.
3. Fast-forward `main` to the rebased tip.
4. Remove the worktree.

**Do not merge to main any other way.** `worktree land` is the single sanctioned path (E-1199). The exception is global-config artifacts (verbs.json, db-ledger entries, plan files) which auto-commit to main directly.

## Abandoning a worktree

```bash
endless worktree drop <id>
endless worktree drop <id> --force          # refuses dirty/unmerged/foreign without this
```

Use `drop` when the work is being abandoned (task declined/obsolete). Don't `drop` over `land` to skip review.

## Commit-to-main policy (E-1199, E-1200)

| What                              | Where it commits          | How                                           |
|-----------------------------------|---------------------------|-----------------------------------------------|
| Task work (code, docs, tests)     | Worktree branch → main    | `worktree land` only                          |
| Plan files (`.endless/plans/`)    | Main directly             | Manual `git commit` from main; required before claim |
| DB ledger (`.endless/db-ledger/`) | Main directly             | Auto by endless-event hook                    |
| Verbs (`verbs.json`)              | Main directly             | Auto on `worktree land`                       |
| Project config (`.endless/config.json`) | Worktree branch → main | Follows task work; not auto                  |

Main's working tree stays clean otherwise (E-1200). If you see uncommitted changes in main that aren't on the allowlist above, that's a bug worth filing.

## See also

- `endless guide tasks` — `task claim`, `task release`
- `endless guide layout` — file paths under `.endless/`
- `endless guide spawn` — spawning a session into a worktree via `--worktree`
