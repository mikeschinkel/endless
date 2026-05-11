# Orchestration: Worktrees, Shell Helpers, Spawning, Channels

How sessions are isolated, navigated, spawned, and how they talk to each other. Everything multi-session lives here.

---

## Worktrees

Every task you claim gets its own git worktree. All work happens there — never in the main checkout's working tree.

### Why

- **Isolation.** Multiple sessions can work on multiple tasks concurrently without stepping on each other.
- **Clean main.** The main checkout's working tree stays clean. Switching between tasks doesn't require stashing.
- **Reviewable history.** When the task lands, its commits arrive on `main` together via `worktree land`.

### Auto-creation on claim

```bash
endless task claim <id>
```

This:

1. Sets the task status to `in_progress`.
2. Binds the task to your session.
3. Creates a git worktree at `.endless/worktrees/e-<id>/` rooted on a fresh branch `task/<id>-<slug>`.
4. Writes companion metadata to `.endless/worktree.json` (task_id, base_branch, branch, timestamp).

If the same task ever needs an additional worktree (testing, alternate experiment, etc.), it lives at `.endless/worktrees/e-<id>-<slug>/`. The primary worktree is always `e-<id>/`.

If the task has an uncommitted plan file in main (`.endless/plans/E-NNNN.md`), `claim` refuses with a message telling you to commit it first. Plan files are global-config artifacts and direct commit to main is allowed for them — `git add` the plan in the main checkout, commit, and retry the claim.

### Getting into the worktree

Direct form:

```bash
cd "$(endless worktree for-task <id>)"
```

Or via shell helpers (next section).

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

1. Auto-commits endless-managed dirt (verbs.jsonl, ledger entries) — these auto-commit to main as global-config artifacts.
2. Rebases the task branch onto current `main`.
3. Fast-forwards `main` to the rebased tip.
4. Removes the worktree.

**Do not merge to main any other way.** `worktree land` is the single sanctioned path. The exception is global-config artifacts (verbs.jsonl, db-ledger entries, plan files) which auto-commit to main directly.

### Abandoning a worktree

```bash
endless worktree drop <id>
endless worktree drop <id> --force          # refuses dirty/unmerged/foreign without this
```

Use `drop` when the work is being abandoned (task declined/obsolete). Don't `drop` over `land` to skip review.

### Commit-to-main policy

`main`'s working tree stays clean. The full policy:

| What                                | Where it commits          | How                                                       |
|-------------------------------------|---------------------------|-----------------------------------------------------------|
| Task work (code, docs, tests)       | Worktree branch → main    | `worktree land` only                                      |
| Plan files (`.endless/plans/`)      | Main directly             | Manual `git commit` from main; required before `task claim` |
| DB ledger (`.endless/db-ledger/`)   | Main directly             | Auto by endless-event hook                                |
| Verbs (`verbs.jsonl`)                | Main directly             | Auto on `worktree land`                                   |
| Project config (`.endless/config.json`) | Worktree branch → main | Follows task work; not auto                              |

If you see uncommitted changes in main that aren't on the allowlist above, that's a bug worth filing as a task.

---

## Shell helpers

Source the helpers once per shell:

```bash
eval "$(endless shell-init)"
```

This adds the following functions:

| Function | What it does                                                                                                                          | Typical use                                              |
|----------|---------------------------------------------------------------------------------------------------------------------------------------|----------------------------------------------------------|
| `esu`    | "Endless session use." Resolves a Claude session (active one by default, or `<id>` if given) and (a) cd's to its worktree and (b) exports `ENDLESS_SESSION_ID`. Subsequent endless commands then route through that worktree's source. | After `task claim`, run `esu` to drop into the worktree fully bound. |
| `esp`    | "Endless session project." cd's to the project root (main checkout) of the active or given session.                                   | When you need to do something in `main` (e.g. commit a plan file) and want to come back. |
| `esf`    | "Endless session forget." Unsets `ENDLESS_SESSION_ID` in the current shell. The session keeps running; only the shell's pointer is cleared. | When you're done coordinating one session and want a fresh shell. |
| `eswt`   | *(Planned, not yet shipped.)* "Endless switch worktree." Pure `cd` to a task's worktree, given a task ID. Distinct from `esu` in that it does not export `ENDLESS_SESSION_ID`. | Quick navigation without binding. Until shipped, use `cd "$(endless worktree for-task <id>)"`. |

`esu`, `esp`, and `esf` all auto-resolve to the sibling Claude pane in tmux when called with no argument.

---

## Spawning another Claude session

`endless task spawn` opens a new tmux window, launches Claude inside it, and pastes the task's prompt as the opening input. Use it to delegate independent work to a fresh session.

### Prerequisite: set the prompt

The task must have a `prompt` field set. Without one, `spawn` errors:

```bash
endless task update <id> --prompt /path/to/prompt.md
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

The spawned session sees the prompt as if you'd typed it. Spawn does *not* auto-claim the task — the spawned session must run `endless task claim <id>` explicitly.

The spawned session can discover its task ID from the tmux window variable:

```bash
tmux show-window-options -v @endless_task_id    # prints the task ID
```

### Authoring the prompt (important — read this whole section)

The spawned session is a fresh Claude. It will only do what the prompt directs it to do, plus what the guide tells it (since you should direct it to read `endless guide`). Until tmux integration takes over more of this orchestration automatically, you (the spawning session) are responsible for writing prompts that drive the spawned session to **completion** — not just "implementation done" but "task closed, worktree landed, no loose ends." A good prompt includes:

**1. Identity and origin.**
- The spawned task ID up front.
- The **spawning session's task ID** and the **spawning session's window/pane** (the user's "where did I come from" anchor). Include the tmux command the user can run to return to your window once the spawned session is complete, e.g. `tmux select-window -t <window_name>`.

**2. Context-gathering directive.**
- Tell it to run `endless guide` (or the relevant section) for general Endless context.
- Tell it to run `endless task show <id> --text --prompt` for the specific plan.

**3. Claim and work.**
- Tell it to `endless task claim <id>` before writing any code.
- Describe the work briefly (the full plan lives in `--text`; don't duplicate it in the prompt).

**4. Goal: drive to completion, not just implementation.**
- The session's goal is to **close the task** so the user can confirm/assume and archive — not just to write the code.
- When implementation is done: set status to `verify` **and include "how to test" instructions** in its handoff message.
- It should not leave its git worktree unmerged and undeleted unless the user explicitly asks it to.
- **Before** merging (`worktree land`) or dropping (`worktree drop`), it must **ask the user**. Don't auto-merge.

**5. Handle loose ends.**
- If it discovers new tasks during implementation (drive-by bugs, follow-ups), file them as separate tasks with `task add` (the canonical relation is `--cleans-up <id>` linking back to the spawned task).
- For any new task it filed, **agree with the user before claiming and implementing it**. Don't quietly scope-creep.
- Pay close attention to any session-channel messages from the spawning session — they may carry corrections or context discovered after the spawn.

**6. Return path for the user.**
- After flipping status to `verify`, the spawned session should print a single `tmux select-window -t <spawning-window-name>` line so the user can copy-paste back to your window with zero ceremony.

**7. Tone.**
- The session should be proactive about closing the task, but **not overbearing** — if the user wants to keep exploring or extending scope from the chat, follow the user's lead.

A minimal but correct prompt looks like:

```text
You are working on E-NNNN. Spawning session: E-MMMM in tmux window "<window_name>".
When complete, the user can return to the spawning session via:
  tmux select-window -t <window_name>

1. Run `endless guide` to learn the workflow.
2. Run `endless task show E-NNNN --text --prompt` to read the plan.
3. `endless task claim E-NNNN` to claim, then cd into the worktree.
4. Do the work.
5. When implementation is done: `endless task update E-NNNN --status verify`,
   then tell the user how to test, and print the tmux-return line above.
6. Do not run `endless worktree land` or `endless worktree drop` without asking
   the user first. Any new tasks you file mid-stream — confirm with the user
   before claiming/implementing them.

Goal: drive this task to a state where the user can confirm and the worktree
can be landed cleanly. Don't leave loose ends.
```

Keep the prompt short — the substantive plan belongs in `--text`, not duplicated here.

> **Note:** Much of this orchestration (return paths, completion-pressure, status display) is being absorbed by the `endless tmux` integration over time. Until then, the spawning session does this work by writing thoughtful prompts.

---

## Inter-session channels

### Why channels exist

The most common motivating case: a session is working on Task A and discovers something that needs to be considered by another session currently working on Task B. Without channels, the user has to copy-paste from one Claude window to the other to relay the message. Channels eliminate that: Session A talks directly to Session B.

Channels are for **live coordination between concurrent sessions** — typically a discovery, correction, or short-lived fact that one session can't easily file as durable state. **Reach for channels infrequently.** Most cross-session communication is better as a filed task or a recorded decision.

### Basic flow

```bash
# Session A: advertise availability
endless channel beacon

# Session B: pair with the beacon
endless channel connect                          # auto-detects if one beacon exists
endless channel connect <channel_id>             # explicit ID if multiple beacons

# Either side: send a message
endless channel send "Found issue in calling-code area — heads up for E-845"

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

### When to reach for channels (and when not)

**Reach for channels when:**

- You discovered something while working on Task A that the *currently active* session on Task B needs to know now — before B's session finishes its current line of thought. (The original motivating case.)
- Two sessions are working in tight tandem on related areas and need brief live coordination ("about to push a column rename — hold for 5 min").

**Don't use channels for:**

- Durable handoffs across sessions that aren't both alive simultaneously → file a task instead.
- Decisions or rationale that should outlive the moment → record a decision (`endless guide decisions`).
- Status that anyone in the project might need to know → update the task field directly.
- Casual coordination that can wait until the next handoff → don't interrupt.

Channels are for *live* coordination. Persistent state lives in the DB.
