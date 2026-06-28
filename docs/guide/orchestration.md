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

1. Sets the task status to `underway`.
2. Binds the task to your session.
3. Creates a git worktree at `.endless/worktrees/e-<id>/` rooted on a fresh branch `task/<id>-<slug>`.
4. Writes companion metadata to `.endless/worktree.json` (task_id, base_branch, branch, timestamp).

One task gets exactly one worktree: `.endless/worktrees/e-<id>/`. Only that canonical name is recognized — a directory created by hand under any other name simply isn't seen as the task's worktree. When you need a *second* checkout for the same line of work — an A/B comparison, running one copy while editing another, a `git bisect`, or a throwaway snapshot — file a **child task** and claim it. The child gets its own `e-<child-id>/` worktree (and its own sandbox), so the two checkouts are first-class, independently tracked, and land or drop on their own.

### Project bootstrap hook

A fresh worktree often needs project-specific setup endless can't bake in — Go's `go.mod` replace paths resolve from the main checkout but not a worktree, Node needs `npm install` or a `node_modules` symlink, Python may need a venv, Rust a `target/` clean, and so on. Endless makes this pluggable: if `<project-root>/.endless/hooks/post-worktree-create.sh` exists and is executable, endless runs it right after the worktree is created.

- **Discovery.** The main checkout's `.endless/hooks/post-worktree-create.sh` (tracked and version-controlled in your repo). Endless ships no default — each project writes its own.
- **Invocation.** The script is exec'd directly (its own shebang) with **cwd = the new worktree** and **`$1` = the worktree path**. No shell-string interpolation.
- **Failure is non-fatal and loud.** If the hook exits non-zero, endless keeps the worktree and prints a warning naming the script, exit code, worktree path, and the command to re-run it.
- **The hook must be idempotent / re-runnable.** Because there's no teardown, completing a failed bootstrap is just re-running the hook. Write it so a second run on an already-bootstrapped worktree is a safe no-op (or a clean regenerate).

Plan files for a task live in the task's worktree at `<worktree>/.endless/plans/E-NNNN.md`, not in main, and ride into main when the task lands. The DB's `tasks.text` column is the source of truth; the on-disk file is a mirror that lives with the branch. `endless task update <id> --text-file <path>` writes `tasks.text`; it does **not** create a worktree. The plan file is materialized from `tasks.text` when the worktree is born (at `task claim`/`task spawn`); if a worktree already exists, `--text`/`--text-file` also mirrors into it. So setting plan text on an unclaimed task touches only the DB — no stray worktrees for tasks you aren't working on yet.

### Getting into the worktree

Direct form:

```bash
cd "$(endless worktree for-task <id>)"
```

Or via shell helpers (next section).

### Choosing the database (`--db`)

When endless develops endless, a self-dev worktree (a `.endless/worktrees/e-NNN`
checkout of a project whose `.endless/config.json` has `"self_dev": true`)
must say which database every command operates on. There is no default — you
pick per invocation:

- `--db main` — the real ledger at `~/.config/endless/endless.db`. Use it for
  **managing the project**: filing tasks, claiming, status updates, ledger entries.
- `--db sandbox` — this worktree's throwaway DB under
  `~/.cache/endless/sandboxes/worktree-e-NNN/`. Use it for **testing endless
  itself** so experiments never touch the real ledger.

```bash
endless --db main task add "Fix the thing"     # before the command
endless task add "Fix the thing" --db main      # or after — position doesn't matter
endless db path --db=sandbox                    # print a DB path without opening it
```

The flag is **mandatory by design** inside such a worktree, and is never an
environment variable: an exported value could silently route every later
command to the wrong DB. Outside a self-dev worktree (the main checkout, or any
downstream project that uses endless as a tool) `--db` is neither required nor
needed.

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

**Do not merge to main any other way.** `worktree land` is the single sanctioned path. The exception is global-config artifacts (verbs.jsonl, db-ledger entries) which auto-commit to main directly.

### Abandoning a worktree

```bash
endless worktree drop <id>
endless worktree drop <id> --force          # refuses dirty/unmerged/foreign without this
```

Use `drop` when the work is being abandoned (task declined/obsolete). Don't `drop` over `land` to skip review.

### Commit-to-main policy

`main`'s working tree stays clean. The full policy:

| What                                    | Where it commits          | How                                                                 |
|-----------------------------------------|---------------------------|---------------------------------------------------------------------|
| Task work (code, docs, tests)           | Worktree branch → main    | `worktree land` only                                                |
| Plan files (`.endless/plans/E-NNNN.md`) | Worktree branch → main    | Written to the worktree by `task update --text`; rides in via `worktree land` |
| DB ledger (`.endless/db-ledger/`)       | Main directly             | Auto by endless-event hook                                          |
| Verbs (`verbs.jsonl`)                   | Main directly             | Auto on `worktree land`                                             |
| Project config (`.endless/config.json`) | Worktree branch → main    | Follows task work; not auto                                         |

If you see uncommitted changes in main that aren't on the allowlist above, that's a bug worth filing as a task.

---

## Shell helpers

Install the helpers permanently with:

```bash
endless setup shell-helpers
```

This appends `eval "$(endless shell-init)"` to your `~/.zshrc`, so the helpers
regenerate on every shell launch and always reflect the current snippet. To
load them in the current shell without installing, run that eval line directly:

```bash
eval "$(endless shell-init)"
```

This adds the following functions:

| Function | What it does                                                                                                                          | Typical use                                              |
|----------|---------------------------------------------------------------------------------------------------------------------------------------|----------------------------------------------------------|
| `esu`    | "Endless session use." Resolves a Claude session (active one by default, or `<id>` if given) and (a) cd's to its worktree and (b) exports `ENDLESS_SESSION_ID`. Subsequent endless commands then route through that worktree's source. | After `task claim`, run `esu` to drop into the worktree fully bound. |
| `esp`    | "Endless session project." cd's to the project root (main checkout) of the active or given session.                                   | When you need to do something in `main` (e.g. inspect `git log` or pull) and want to come back. |
| `esf`    | "Endless session forget." Unsets `ENDLESS_SESSION_ID` in the current shell. The session keeps running; only the shell's pointer is cleared. | When you're done coordinating one session and want a fresh shell. |
| `eswt`   | *(Planned, not yet shipped.)* "Endless switch worktree." Pure `cd` to a task's worktree, given a task ID. Distinct from `esu` in that it does not export `ENDLESS_SESSION_ID`. | Quick navigation without binding. Until shipped, use `cd "$(endless worktree for-task <id>)"`. |

These helpers move the **shell's** working directory and (for `esu`) set session
routing. They do **not** change **Claude's own** working directory — the one
Read/Write/Edit and a fresh Bash default to. For that, run the `/cd` slash
command inside the Claude session with the worktree's **absolute** path (printed
by `task claim`, or from `endless worktree for-task <id>`):

```
/cd /abs/path/to/.endless/worktrees/e-<id>
```

`/cd` needs an absolute path — it does not expand `~` or `$(...)`. The first
`/cd` into a directory triggers a one-time trust prompt, and `/cd` requires
Claude Code v2.1.169+. Run it once after `task claim` so every tool defaults to
the worktree instead of main. `/cd` and `esu`/`eswt` are complementary: `/cd`
fixes Claude's working directory, `esu` fixes shell session routing. A claimed
session that hasn't `/cd`'d into its worktree is refused tool use until it does.

`esu`, `esp`, and `esf` all auto-resolve to the sibling Claude pane in tmux when called with no argument.

---

## Spawning another Claude session

`endless task spawn <id>` dispatches a fresh Claude session onto a target task and pastes a **generated handoff** as its opening input. Use it to delegate independent work without context-switching your own session.

Spawn runs in one of two places:

- **Foreground** (`endless task spawn <id>`) — a new tmux window, Claude visible and interactive.
- **Background** (`endless task spawn <id> --bg`) — a headless agent under Anthropic's supervisor process, no terminal attached.

Both **pre-claim** the task (status → `underway`, per-task worktree created) and run the same pre-flight refusals before launching, so the spawned session always lands in a fully-claimed state and never needs to run `endless task claim` itself.

### Foreground vs background

|                  | Foreground (`spawn`)                       | Background (`spawn --bg`)                              |
|------------------|--------------------------------------------|-------------------------------------------------------|
| Where it runs    | new tmux window                            | Anthropic supervisor (no terminal)                    |
| When to use      | the work needs eyes; pairs well with `/plan` mode | a dispatched child of an epic you'll review later |
| Survives         | terminal close (tmux server keeps it)      | terminal close, machine sleep, tmux server crash      |
| Dies on          | tmux server kill, machine shutdown         | machine shutdown, `claude stop`, ~1h idle (unpinned)  |
| Promote to focus | (already focused)                          | `endless task spawn --attach <id>` or `endless task attach <id>` |

### Per-type handoff variants

The handoff is rendered from a per-type template, chosen from the task's `type`:

- **`task`** — frames the work around a verify end-state: implement, flip to `unverified`, supply how-to-test.
- **`bug`** — leads with "reproduce the bug first, before changing anything."
- **`research`** — findings *are* the deliverable: end-state is `completed` with the conclusions written to the task's outcome, not a code-verify cycle.
- **`epic`** — a coordinator role (see [Coordinator pattern for epics](#coordinator-pattern-for-epics)).

Any other or unset type falls back to the `task` variant.

### The handoff is generated, not authored

There is nothing to write. The handoff is rendered from the per-type template (`handoff/<type>.md.tmpl`) merged with the task's id and title plus runtime context (the spawning pane, the spawning session's task). The substantive design lives in the task's `--text` plan, which the handoff tells the spawned session to read — so a prompt can no longer drift from the plan.

Inspect the exact text spawn will paste:

```bash
endless task handoff <id>
```

The handoff is deliberately lean — it delegates the workflow rules to `endless guide` rather than restating them. It carries: the spawned task's id and title, the spawning session's task, the `tmux select-window` line back to your window, the pointers to run `endless guide` and `endless task show <id> --text`, and the drive-to-completion rules (flip to `unverified` with how-to-test; don't `worktree land`/`drop` without asking; file drive-by work as separate tasks with `--cleans-up <id>`).

To change what every spawned session is told, edit the template — see [Customizing handoff templates](#customizing-handoff-templates). There is no per-task prompt to maintain.

### `endless task spawn`

```bash
endless task spawn <id>                           # foreground: new tmux window
endless task spawn <id> --bg                      # background: headless supervised agent
endless task spawn <id> --attach <id>             # open a tmux window onto an already-running bg agent
endless task spawn <id> --no-plan                 # skip /plan mode; send the handoff directly (foreground only)
endless task spawn <id> --worktree <path>         # cd to <path> instead of the spawn-created worktree
endless task spawn <id> --reopen                  # reopen a terminal-status task before spawning
endless task spawn <id> --force                   # allow spawn on a done-ish task (demotes status)
```

Foreground flow:

1. Validates tmux is running (fails otherwise).
2. Refuses if the task is in a done-ish status (`unverified`/`confirmed`/`declined`/`obsolete`/`assumed`/`completed`) without `--force` or `--reopen`, or if another live session already owns the task.
3. **Pre-claims the task**: flips status to `underway` (emitting `task.status_changed`) and creates the per-task worktree at `.endless/worktrees/e-<id>/`.
4. Creates a new tmux window named `<project>_<slug>[E-NNNN]` and sets the window variables `@endless_spawned_by`, `@endless_task_id`, `@endless_project_id`.
5. `cd`s into the spawn-created worktree (or `--worktree <path>` if given) and launches Claude.
6. The spawned Claude's `SessionStart` hook reads `@endless_spawned_by` and records the session→task binding (no status flip — spawn already did it).
7. Waits for Claude to start, enters `/plan` mode (unless `--no-plan`), then renders the handoff, pastes it, and presses Enter.

The spawned session can discover its task ID from the tmux window variable:

```bash
tmux show-window-options -v @endless_task_id    # prints the task ID
```

### Background-agent dispatch (`--bg`)

`--bg` dispatches a detached, supervised agent instead of opening a window — no tmux required. The flow:

1. Pre-claims the task (same status flip + worktree creation as foreground).
2. Renders the handoff for the task's type.
3. Launches a headless Claude agent named `E-<id>` with the handoff as its opening input (via the Anthropic CLI's background mode — see `claude --help`).
4. Captures the short dispatch id the CLI prints.
5. Writes a `sessions` row marked as a background kind (an FK to the `session_kinds` table), recording the short id and the task's nearest epic ancestor for coordinator visibility. The session UUID is filled in later when the agent's `SessionStart` hook fires.

`spawn --bg` returns immediately; the agent runs on its own. `--no-plan` does not apply (a bg agent has no interactive `/plan` step). To watch or steer it afterward, use an attach verb below.

### Attach verbs

Two ways to bring a running background agent into a terminal:

```bash
endless task spawn --attach <id>      # open a NEW tmux window running `claude attach <short-id>`
endless task attach <id>              # replace the CURRENT process with `claude attach <short-id>`
```

- **`spawn --attach <id>`** opens a fresh tmux window onto the agent. It does not dispatch (it requires an existing `--bg` agent) and is mutually exclusive with `--bg`. The agent keeps running when you close or detach the window.
- **`task attach <id>`** execs `claude attach` *in place*, replacing the current process. Because that destroys whatever is running in the current terminal, it **refuses to run from inside a Claude session** unless you pass `--force`:

  > You are inside a Claude session. `endless task attach` replaces the current process; you will lose this session. Re-run with --force to proceed, or open a fresh terminal.

Detaching from an attached agent (`←`, `Ctrl+Z`, or `/exit`) leaves it running in the background — attaching and detaching never stop the agent.

### Coordinator pattern for epics

Spawning a task whose type is `epic` opens a foreground window for a **coordinator**. The coordinator does **not** implement the epic's work directly — its job is to drive the epic's children through `unplanned` → `ready` → `underway` → `unverified`, dispatching child sessions (often with `spawn --bg`) and reviewing them.

The epic handoff injects a breakdown of the children's current states and names the operational mode that breakdown implies:

| Children state        | Coordinator's mode                                   |
|-----------------------|------------------------------------------------------|
| Zero children         | drive decomposition — break the epic into child tasks |
| All `unplanned`      | planning orchestrator — get each child a plan         |
| All `ready`           | dispatcher — spawn children to implement              |
| All `underway`     | observe — sessions are working; monitor and unblock   |
| All terminal          | ask whether to reopen anything or close the epic      |
| Mixed                 | surface the breakdown and ask what to do next         |

(Terminal = `confirmed`/`assumed`/`completed`/`declined`/`obsolete`, collapsed into one bucket.)

### Throttle warning

When you dispatch a background agent and the project already has several active, spawn prints a **soft warning to stderr** — it never blocks. The threshold is `bg_throttle_warn` in the project's `.endless/config.json` (default `3`; set to `0` or negative to disable). The warning notes that each bg agent consumes a parallel-execution slot and that the community-observed sweet spot is 3–5 parallel agents.

### Session lifecycle (background agents)

A background agent is hosted by Anthropic's supervisor, independent of your terminal and tmux. It **survives**:

- closing the terminal or shell that spawned it,
- a tmux server crash,
- the machine sleeping (Claude Code v2.1.142+ resumes on wake instead of treating the gap as idle).

It **dies / stops** on:

- machine shutdown,
- `claude stop`,
- roughly an hour idle while unattached (pinned sessions are exempt).

### Customizing handoff templates

The four handoff templates ship embedded in the `endless-go` binary. The first time a template renders in a consumer project, its embedded copy is **materialized** per-file to `<project_root>/.endless/templates/handoff/<type>.md.tmpl` and auto-committed, so the on-disk file is tracked and editable.

- **Customize project-wide:** edit the materialized `.tmpl` file and commit it.
- **Restore the default:** delete the materialized file — the embedded version renders again on the next spawn (and re-materializes).
- **Per-developer override (not committed):** create `<project_root>/.endless/templates/handoff/<type>.md.local.tmpl`. The lookup order is `.local.tmpl` → committed `.tmpl` → embedded, so a `.local.tmpl` wins. Add `*.local.tmpl` to your `.gitignore` so personal overrides don't get committed by accident — Endless does not modify `.gitignore` for you.

To debug-render any template from JSON variables on stdin:

```bash
echo '{"task_id":"E-NNN","title":"…"}' | endless internal template render handoff/task
endless internal template render handoff/epic --project <name> < vars.json
```

(Self-dev projects render straight from the embedded source without materializing, to avoid an untracked on-disk copy shadowing the committed template.)

> **Note:** Much of this orchestration (return paths, completion-pressure, status display) is being absorbed by the `endless tmux` integration over time.

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
