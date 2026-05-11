# Orchestration: Worktrees, Spawning, Channels

How sessions are isolated, spawned, and how they talk to each other. Everything multi-session lives here.

---

## Worktrees

Every task you claim gets its own git worktree (E-1168). All work happens there — never in the main checkout's working tree.

### Why

- **Isolation.** Multiple sessions can work on multiple tasks concurrently without stepping on each other.
- **Clean main.** The main checkout's working tree stays clean (E-1200). Switching between tasks doesn't require stashing.
- **Reviewable history.** When the task lands, its commits arrive on `main` together via `worktree land`.

### Auto-creation on claim

```bash
endless task claim <id>
```

This:

1. Sets the task status to `in_progress`.
2. Binds the task to your session.
3. Creates a git worktree at `.endless/worktrees/<slug>/` rooted on a fresh branch `task/<id>-<slug>`.
4. Writes companion metadata to `.endless/worktree.json` (task_id, base_branch, branch, timestamp).

If the task has an uncommitted plan file in main (`.endless/plans/E-NNNN.md`), claim refuses with a message telling you to commit it first (E-1169). Plan files are global-config artifacts and direct commit to main is allowed for them.

### Getting into the worktree

```bash
cd "$(endless worktree for-task <id>)"
```

Or via shell helpers:

```bash
eval "$(endless shell-init)"     # once per shell
esu                              # cd to your active session's worktree (auto-resolves)
esu <session_id>                 # explicit session ID if needed
```

Note: `esu` resolves by *session* (the running Claude session), not by task ID. After `task claim` your session is bound to the task, so bare `esu` works.

### Inspecting

```bash
endless worktree list                       # all worktrees for the current project
endless worktree show <slug-or-id>          # detail for one
endless worktree current                    # what worktree is cwd in (or "none")
endless worktree for-task <id>              # resolve a task ID to its path
```

### Landing the work

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

### Abandoning a worktree

```bash
endless worktree drop <id>
endless worktree drop <id> --force          # refuses dirty/unmerged/foreign without this
```

Use `drop` when the work is being abandoned (task declined/obsolete). Don't `drop` over `land` to skip review.

### Commit-to-main policy (E-1199, E-1200)

| What                              | Where it commits          | How                                           |
|-----------------------------------|---------------------------|-----------------------------------------------|
| Task work (code, docs, tests)     | Worktree branch → main    | `worktree land` only                          |
| Plan files (`.endless/plans/`)    | Main directly             | Manual `git commit` from main; required before claim |
| DB ledger (`.endless/db-ledger/`) | Main directly             | Auto by endless-event hook                    |
| Verbs (`verbs.json`)              | Main directly             | Auto on `worktree land`                       |
| Project config (`.endless/config.json`) | Worktree branch → main | Follows task work; not auto                  |

Main's working tree stays clean otherwise. If you see uncommitted changes in main that aren't on the allowlist above, that's a bug worth filing.

---

## Spawning another Claude session

`endless task spawn` opens a new tmux window, launches Claude inside it, and pastes the task's prompt as the opening input. Use it to delegate independent work to a fresh session.

### Prerequisite: set the prompt

The task must have a `prompt` field set. Without one, `spawn` errors:

```bash
endless task update <id> --prompt /path/to/prompt.md
```

The prompt is the *opening input* to the spawned session — typically a directive like:

```text
You are working on E-NNNN. Run `endless task show E-NNNN --text` to read the
plan. Then claim the task and implement it. When done, set status to verify.
```

Inspect with:

```bash
endless task prompt <id>           # raw output, suitable for piping
endless task show <id> --prompt    # decorated output
```

### Spawn

```bash
endless task spawn <id>
endless task spawn <id> --no-plan                # skip /plan mode; send prompt directly
endless task spawn <id> --worktree <path>        # cd to <path> before launching claude
```

What it does:

1. Validates tmux is running (fails otherwise).
2. Reads the task's prompt.
3. Creates a new tmux window named `<project>_<slug>[E-NNNN]`.
4. Sets tmux window variables `@endless_task_id` and `@endless_project_id`.
5. `cd`s to the project's main checkout (or `--worktree <path>` if given).
6. Launches `~/.local/bin/claude` (falls back to `claude` on PATH).
7. Waits ~5s for Claude to start, then enters `/plan` mode (unless `--no-plan`).
8. Pastes the prompt and presses Enter.

The spawned session sees the prompt as if you'd typed it.

### What the spawned session needs to do

Spawn does *not* auto-claim the task. The spawned session must claim explicitly:

```bash
endless task claim <id>
```

This is by design — the spawn workflow is "here's a fresh Claude, here are its instructions"; the session then acts on them. A typical prompt tells the session to run `endless task show <id> --text --prompt`, then claim, then work.

The session can discover its task ID from the tmux window variable:

```bash
tmux show-window-options -v @endless_task_id    # prints "1248" or similar
```

### Authoring the prompt

A good task prompt:

- States the task ID up front.
- Tells the session to read context (`endless task show`, `endless guide`).
- Tells it to claim the task.
- Describes the work briefly (the full plan lives in `text`).
- Says what completion looks like (set `--status verify`, what to verify).

Keep it short — the bulk of the plan should be in `--text`, not duplicated in the prompt.

---

## Inter-session channels

Endless supports messaging between concurrent Claude Code sessions via channels. Useful when multiple sessions are working on related tasks in the same project.

### Basic flow

```bash
# Session A: advertise availability
endless channel beacon

# Session B: pair with the beacon
endless channel connect                          # auto-detects if one beacon exists
endless channel connect <channel_id>             # explicit ID if multiple beacons

# Either side: send a message
endless channel send "E-839 is done; task update now accepts --phase"

# Either side: read incoming messages
endless channel inbox

# List active beacons for the project
endless channel list

# Tear down
endless channel close
```

### How it works

- One session calls `beacon` to register as available.
- Another session calls `connect` to pair with it.
- Messages are delivered via MCP notifications. The receiving session sees a channel event and runs `endless channel inbox` to read it.
- Channels are project-scoped: `connect` with no argument finds the beacon for the current project.

**Do not run `endless channel inbox` unprompted.** Only when a channel event is delivered, or your user asks.

### Common patterns

- **Handoff.** "E-839 confirmed — task update now accepts --phase. You can unblock E-845."
- **Coordination.** "About to push migration that renames column `foo` → `bar`. Hold for 5 min on E-851."
- **Discovery.** "Filed E-867 — saw a similar issue in the calling code while working on E-845."

### When not to use channels

- Durable handoffs across sessions that aren't both alive → file a task instead.
- For decisions, record a decision (see `endless guide decisions`).
- For status anyone needs to know, update the task field directly.

Channels are for *live* coordination. Persistent state lives in the DB.
