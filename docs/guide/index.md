# Using Endless in a Claude Code Session

This guide tells a Claude Code session how to work with Endless on a tracked project.

## What Endless is

Endless is a project awareness tool. It tracks **what you're working on**, **why**, and **whether you declared your intent** before making changes. It provides:

- A **task tree** — hierarchical items representing what needs to be done.
- **Decisions** as first-class artifacts — rationale that lives alongside tasks.
- **Per-task git worktrees** — your work happens in an isolated branch, not on `main`.
- **Session tracking** — records which Claude session is working on which task.
- **Enforcement** (optional) — a hook that can block Write/Edit until you claim a task.
- A **web dashboard** at `http://localhost:8484` (start with `endless serve`).

## Status

Endless is in active development — paving the cowpaths. Expect rough edges, expect change. Your honest feedback on friction makes Endless better. When something is wrong or surprising, say so. When something is missing, file a task (`endless task add ...`).

## The happy path

When your user gives you a task ID:

1. `endless task show <id> --text` — read the task and any attached plan.
2. `endless task claim <id>` — claim the task. This automatically creates a git worktree at `.endless/worktrees/e-<id>/` for your work. Every task gets its own worktree so multiple sessions can work in parallel without stepping on each other, and `main`'s working tree stays clean.
3. Get into the worktree:
   - **`/cd <worktree-path>`** — the primary move for a Claude Code session. `task claim` prints the exact `/cd` line; running it changes Claude's own working directory, so every later tool (Read/Write/Edit, and a fresh Bash) defaults to the worktree instead of main. Do this once, right after claiming, and you no longer have to qualify paths to avoid editing main by accident. Pass an **absolute** path — `/cd` does not expand `~` or `$(...)`, and the first `/cd` into a directory prompts you to trust it.
   - `cd "$(endless worktree for-task <id>)"` moves only the Bash shell's cwd, not Claude's — file tools still default to main. Prefer `/cd`.
   - run `eval "$(endless shell-init)"` once per shell, then `esu` to cd to your session's worktree *and* export `ENDLESS_SESSION_ID` so subsequent endless commands route through the worktree's source (not the global install). `esu`/`eswt` are complementary to `/cd`: they handle session routing, `/cd` handles Claude's working directory. See **Shell helpers** in `endless guide orchestration`.
4. Do the work in the worktree.
5. When implementation is complete:
   - `endless task update <id> --status unverified`, **and**
   - In your reply to the user, include **how to test**: the specific commands, files, or UI actions that verify the change. Don't just say "ready" — say "ready; verify by running X then checking Y." The user shouldn't have to ask.
6. Report completion to your user with the task ID. Example: "Done — E-752 is ready for verification. To verify: run `endless guide --list` and confirm the 4 expected slugs."
7. **Do not mark `confirmed` yourself.** Only your user does that, after verifying. If you can't easily verify but believe it works, run `endless task assume <id> --outcome "..."` instead.

When implementation is verified, land the work with `endless worktree land <id>` (auto-commits endless-managed files, rebases onto main, fast-forwards, removes the worktree).

## Task statuses

| Status        | Meaning                                                                                                       |
|---------------|---------------------------------------------------------------------------------------------------------------|
| `unplanned`  | Not yet planned — needs design work. Attach a plan with `task update <id> --text-file <path>` and the task auto-promotes to `ready`. |
| `ready`       | Planned and ready to implement.                                                                                |
| `underway` | A session has claimed the task and is working on it. Set automatically by `task claim`.                        |
| `unverified`      | Implementation done, awaiting verification. **Still blocks dependents.**                                       |
| `confirmed`   | Verified and done. **Unblocks dependents.** Only the user confirms.                                            |
| `assumed`     | Believed complete, will verify when used naturally. **Unblocks dependents.**                                   |
| `blocked`     | Waiting on something else.                                                                                     |
| `revisit`     | Was partially planned but needs re-evaluation.                                                                 |
| `declined`    | Active decision not to do this. Requires `--reason`.                                                           |
| `obsolete`    | Made irrelevant by other changes.                                                                              |

Use `assumed` (not `unverified`) when the only way to test the work is by using it in a downstream task — set `--outcome` explaining what was done and how confidence was established.

## Task phases

| Phase    | Meaning                                                                                                                              |
|----------|--------------------------------------------------------------------------------------------------------------------------------------|
| `urgent` | Time-critical priority; takes precedence over `now`. Use sparingly.                                                                  |
| `now`    | Current priority.                                                                                                                    |
| `next`   | Up next.                                                                                                                             |
| `later`  | Future work, not urgent — **committed to do eventually**.                                                                            |
| `maybe`  | Considered but not committed — **may or may not be done**. Distinct from `later`. Promote to `now` or `next` when decided.           |

Don't conflate `blocked` ("will do when X resolves") with `maybe` ("might do at all").

## Blocking semantics

When task A is blocked by task B (`endless task block A --by B`):

- B in `unverified` → A is **still blocked**. Unverified means "not yet trusted."
- B in `confirmed` or `assumed` → A is **unblocked**.
- B in `declined` or `obsolete` → A is **unblocked**.

In `task show`, blocking relations appear in the **This task:** section, where each row opens with a directional phrase that names what the current task does: a `Blocked by:` row points to a task that blocks this one, a `Blocks:` row points to a task this one blocks, each tagged with the related task's `[status]` — which tells you whether a blocker is still active (e.g. `unverified`) or resolved (`confirmed`/`assumed`).

## Common patterns

```bash
# Find work
endless task next                                # actionable tasks, ranked
endless task active                              # underway + unverified
endless task recent                              # recently updated

# Record a new task discovered during work — use the literal ID printed
endless task add "Verb-first title" --parent <current_id> --description "..."

# Record a decision discovered during work (creates a paired decision task)
endless task update <current_id> --decision "Why we picked X over Y."

# Mark for replanning
endless task update <id> --status revisit

# Hand off to another session
endless task release <id>                        # then the other session: task claim <id>
endless task spawn <id>                          # or spawn a fresh Claude session

# Read a task you didn't claim — no claim needed for reads
endless task show <id> --text --children --llm
```

## Sections

For details, run `endless guide <section>`:

- **tasks** — task CRUD reference, field semantics (title/description/text/analysis/notes/outcome), and verbs.
- **orchestration** — per-task worktrees, spawning sessions, inter-session channels, commit-to-main policy.
- **decisions** — documenting decisions as first-class items, **including STRONG guidance about preference vs prohibition — read this**.
- **sessions** — recording session status snapshots (`endless session snapshot add`), the `session_statuses` row shape, when to call it, and discovery patterns for "who am I."
- **reference** — projects, SQL, snapshots, tmux integration, file layout.

Run `endless guide --list` to print just the section slugs.

<!-- BEGIN generated: command/topic cross-reference (regenerate via /regenerate-guide) -->
## Where to look (command / topic → section)

Have a command or topic and need the guidance for it? Find the row, then
run `endless guide <section>`. Subcommands inherit their group's row unless
listed separately. (Generated — do not hand-edit; run `/regenerate-guide`.)

### Commands

| Command | Section | Covers |
|---|---|---|
| `agents` | _(none yet)_ | the `endless agents` command (epic-scoped listing of working background agents) isn't covered by the guide yet. |
| `channel` | orchestration | Inter-session channels: messaging between concurrent sessions. |
| `db` | orchestration | Choosing the database (--db main/sandbox) in self-dev worktrees. |
| `decision` | decisions | Decisions as first-class items; preference vs prohibition (read this). |
| `discover` | reference | Finding and registering unregistered projects. |
| `docs` | _(none yet)_ | the `docs` command is temporarily disabled and not covered by the guide. |
| `epic` | _(none yet)_ | the `endless epic` convenience surface (add/show/list/update over type=epic tasks) isn't covered by the guide yet. |
| `guide` | reference | The session guide; run `endless guide` for the index, `--list` for sections. |
| `list` | reference | Listing registered projects. |
| `note` | _(none yet)_ | project notes aren't covered by the guide yet. |
| `notes` | _(none yet)_ | project notes aren't covered by the guide yet. |
| `phrase` | _(none yet)_ | matchers (action regexes) config isn't covered by the guide yet. |
| `plan` | tasks | 'plan' is the former name for 'task' (renamed); use 'task'. |
| `purge` | reference | Removing the .endless/ directory from a project. |
| `register` | reference | Registering a directory as a project. |
| `rename` | reference | Renaming a project. |
| `scan` | reference | Scanning and reconciling registered projects. |
| `serve` | reference | The web dashboard at http://localhost:8484. |
| `session` | sessions | Recording session status; discovery (who am I); reading status. |
| `set` | reference | Setting a project field. |
| `setup` | _(none yet)_ | hook/integration setup (claude-hook, prompt-hook, channel-plugin) isn't covered by the guide yet. |
| `shell-init` | orchestration | Shell helpers (esu/eswt) to enter your task's worktree. |
| `sql` | reference | Read-only SQL against the Endless DB. |
| `status` | reference | Detailed status of a project. |
| `suggestions` | _(none yet)_ | the enforcement-relaxation suggestions workflow isn't covered by the guide yet. |
| `task` | tasks | Task CRUD, field semantics (title/description/text/analysis/notes/outcome), status transitions, relations. |
| `task attach` | orchestration | Attaching to a running background agent (replaces the current process; refuses inside a Claude session without --force). |
| `task claim` | orchestration | Claiming a task: creates the per-task worktree and binds your session. |
| `task handoff` | orchestration | The generated handoff text for a spawned session. |
| `task release` | orchestration | Releasing a task so another session can claim it. |
| `task spawn` | orchestration | Spawning a session on a task: foreground/background, attach verbs, coordinator pattern. |
| `tmux` | reference | Tmux status-line and popup integration. |
| `unregister` | reference | Unregistering a project (config preserved on disk). |
| `verb` | tasks | Verbs: the registered actions that can begin a task title. |
| `worktree` | orchestration | Per-task git worktrees: getting in, landing, abandoning, inspecting. |

### Topics

| Topic | Section | Covers |
|---|---|---|
| commit-to-main policy | orchestration | When to commit to main vs work only in a worktree. |
| who am I / current session | sessions | Discovering your session id and the task it's bound to. |
| preference vs prohibition | decisions | Soft signals ('ideally','usually') are not rules - verify before recording. |
| the handoff (generated, not authored) | orchestration | Spawned sessions get a rendered handoff; agents never write it. |
| worktree DB sandbox (--db main vs sandbox) | orchestration | Self-dev DB routing and the --db choice. |
| shell helpers (esu / eswt) | orchestration | cd into your worktree and export ENDLESS_SESSION_ID. |
| blocking semantics | tasks | How unverified/confirmed/assumed affect whether a blocker is still active. |
| verbs | tasks | The registered action words that can begin a task title. |
| research-task field model | tasks | For a research task, text = the request, outcome = the deliverable. |
<!-- END generated -->

## Important notes (always relevant)

- **Don't mark items `confirmed`.** Set them to `unverified` and let your user confirm — or `assume` if you can't easily verify.
- **Always claim before writing code.** Even when enforcement is off, claiming registers your session and creates the worktree.
- **Use the worktree.** Don't make project changes in the `main` checkout's working tree.
- **Use `--llm` for agent-friendly output.** `task list --llm`, `task show --llm`, `task next --llm`, etc.
- **Tasks have hierarchy.** Use `--parent <id>` when adding child items.
- **Verify preferences before recording prohibitions.** Soft signals ("ideally", "usually", "I'd prefer") are not rules. See `endless guide decisions`.
- **Use the literal task ID printed by `task add`.** IDs advance globally across parallel sessions — never guess.
- **`.endless/db-ledger/` is durable state, committed to git.** Don't add it to `.gitignore`.
