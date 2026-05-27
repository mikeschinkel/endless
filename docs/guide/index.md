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
   - `cd "$(endless worktree for-task <id>)"`, or
   - run `eval "$(endless shell-init)"` once per shell, then `esu` to cd to your session's worktree *and* export `ENDLESS_SESSION_ID` so subsequent endless commands route through the worktree's source (not the global install). See **Shell helpers** in `endless guide orchestration`.
4. Do the work in the worktree.
5. When implementation is complete:
   - `endless task update <id> --status verify`, **and**
   - In your reply to the user, include **how to test**: the specific commands, files, or UI actions that verify the change. Don't just say "ready" — say "ready; verify by running X then checking Y." The user shouldn't have to ask.
6. Report completion to your user with the task ID. Example: "Done — E-752 is ready for verification. To verify: run `endless guide --list` and confirm the 4 expected slugs."
7. **Do not mark `confirmed` yourself.** Only your user does that, after verifying. If you can't easily verify but believe it works, run `endless task assume <id> --outcome "..."` instead.

When implementation is verified, land the work with `endless worktree land <id>` (auto-commits endless-managed files, rebases onto main, fast-forwards, removes the worktree).

## Task statuses

| Status        | Meaning                                                                                                       |
|---------------|---------------------------------------------------------------------------------------------------------------|
| `needs_plan`  | Not yet planned — needs design work. Attach a plan with `task update <id> --text <file>` and the task auto-promotes to `ready`. |
| `ready`       | Planned and ready to implement.                                                                                |
| `in_progress` | A session has claimed the task and is working on it. Set automatically by `task claim`.                        |
| `verify`      | Implementation done, awaiting verification. **Still blocks dependents.**                                       |
| `confirmed`   | Verified and done. **Unblocks dependents.** Only the user confirms.                                            |
| `assumed`     | Believed complete, will verify when used naturally. **Unblocks dependents.**                                   |
| `blocked`     | Waiting on something else.                                                                                     |
| `revisit`     | Was partially planned but needs re-evaluation.                                                                 |
| `declined`    | Active decision not to do this. Requires `--reason`.                                                           |
| `obsolete`    | Made irrelevant by other changes.                                                                              |

Use `assumed` (not `verify`) when the only way to test the work is by using it in a downstream task — set `--outcome` explaining what was done and how confidence was established.

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

- B in `verify` → A is **still blocked**. Verify means "not yet trusted."
- B in `confirmed` or `assumed` → A is **unblocked**.
- B in `declined` or `obsolete` → A is **unblocked**.

In `task show`, blocking relations appear in the **Links:** section: `(blocked by)` for a task that blocks this one and `(blocks)` for a task this one blocks, each tagged with the related task's `[status]` — which tells you whether a blocker is still active (e.g. `verify`) or resolved (`confirmed`/`assumed`).

## Common patterns

```bash
# Find work
endless task next                                # actionable tasks, ranked
endless task active                              # in_progress + verify
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
- **sessions** — recording session status (`endless session status add`), the `session_statuses` row shape, when to call it, and discovery patterns for "who am I."
- **reference** — projects, SQL, snapshots, tmux integration, file layout.

Run `endless guide --list` to print just the section slugs.

<!-- BEGIN generated: command/topic cross-reference (regenerate via /regenerate-guide) -->
## Where to look (command / topic → section)

Have a command or topic and need the guidance for it? Find the row, then
run `endless guide <section>`. Subcommands inherit their group's row unless
listed separately. (Generated — do not hand-edit; run `/regenerate-guide`.)

| Command / topic | Section | Covers |
|---|---|---|
| `endless channel` | orchestration | Inter-session channels: messaging between concurrent sessions. |
| `endless db` | orchestration | Choosing the database (--db main/sandbox) in self-dev worktrees. |
| `endless decision` | decisions | Decisions as first-class items; preference vs prohibition (read this). |
| `endless discover` | reference | Finding and registering unregistered projects. |
| `endless docs` | _(none yet)_ | the `docs` command is temporarily disabled and not covered by the guide. |
| `endless guide` | reference | The session guide; run `endless guide` for the index, `--list` for sections. |
| `endless list` | reference | Listing registered projects. |
| `endless note` | _(none yet)_ | project notes aren't covered by the guide yet. |
| `endless notes` | _(none yet)_ | project notes aren't covered by the guide yet. |
| `endless phrase` | _(none yet)_ | matchers (pivots, action regexes) config isn't covered by the guide yet. |
| `endless plan` | tasks | 'plan' is the former name for 'task' (renamed); use 'task'. |
| `endless purge` | reference | Removing the .endless/ directory from a project. |
| `endless register` | reference | Registering a directory as a project. |
| `endless rename` | reference | Renaming a project. |
| `endless scan` | reference | Scanning and reconciling registered projects. |
| `endless serve` | reference | The web dashboard at http://localhost:8484. |
| `endless session` | sessions | Recording session status; discovery (who am I); reading status. |
| `endless set` | reference | Setting a project field. |
| `endless setup` | _(none yet)_ | hook/integration setup (claude-hook, prompt-hook, channel-plugin) isn't covered by the guide yet. |
| `endless shell-init` | orchestration | Shell helpers (esu/eswt) to enter your task's worktree. |
| `endless sql` | reference | Read-only SQL against the Endless DB. |
| `endless status` | reference | Detailed status of a project. |
| `endless suggestions` | _(none yet)_ | the enforcement-relaxation suggestions workflow isn't covered by the guide yet. |
| `endless task` | tasks | Task CRUD, field semantics (title/description/text/analysis/notes/outcome), status transitions, relations. |
| `endless task claim` | orchestration | Claiming a task: creates the per-task worktree and binds your session. |
| `endless task handoff` | orchestration | The generated handoff text for a spawned session. |
| `endless task release` | orchestration | Releasing a task so another session can claim it. |
| `endless task spawn` | orchestration | Spawning a fresh Claude session on a task. |
| `endless tmux` | reference | Tmux status-line and popup integration. |
| `endless unregister` | reference | Unregistering a project (config preserved on disk). |
| `endless verb` | tasks | Verbs: the registered actions that can begin a task title. |
| `endless worktree` | orchestration | Per-task git worktrees: getting in, landing, abandoning, inspecting. |
| _topic:_ commit-to-main policy | orchestration | When to commit to main vs work only in a worktree. |
| _topic:_ who am I / current session | sessions | Discovering your session id and the task it's bound to. |
| _topic:_ preference vs prohibition | decisions | Soft signals ('ideally','usually') are not rules - verify before recording. |
| _topic:_ the handoff (generated, not authored) | orchestration | Spawned sessions get a rendered handoff; agents never write it. |
| _topic:_ worktree DB sandbox (--db main vs sandbox) | orchestration | Self-dev DB routing and the --db choice. |
| _topic:_ shell helpers (esu / eswt) | orchestration | cd into your worktree and export ENDLESS_SESSION_ID. |
| _topic:_ blocking semantics | tasks | How verify/confirmed/assumed affect whether a blocker is still active. |
| _topic:_ verbs | tasks | The registered action words that can begin a task title. |
<!-- END generated -->

## Important notes (always relevant)

- **Don't mark items `confirmed`.** Set them to `verify` and let your user confirm — or `assume` if you can't easily verify.
- **Always claim before writing code.** Even when enforcement is off, claiming registers your session and creates the worktree.
- **Use the worktree.** Don't make project changes in the `main` checkout's working tree.
- **Use `--llm` for agent-friendly output.** `task list --llm`, `task show --llm`, `task next --llm`, etc.
- **Tasks have hierarchy.** Use `--parent <id>` when adding child items.
- **Verify preferences before recording prohibitions.** Soft signals ("ideally", "usually", "I'd prefer") are not rules. See `endless guide decisions`.
- **Use the literal task ID printed by `task add`.** IDs advance globally across parallel sessions — never guess.
- **`.endless/db-ledger/` is durable state, committed to git.** Don't add it to `.gitignore`.
