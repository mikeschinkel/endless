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

1. `endless task show <id> --text --prompt` — read the task and any attached plan.
2. `endless task claim <id>` — claim the task. This automatically creates a git worktree at `.endless/worktrees/<slug>/` for your work (E-1168).
3. `cd` into that worktree (or run `eval "$(endless shell-init)"` once, then `eswt <id>`).
4. Do the work in the worktree.
5. `endless task update <id> --status verify` when implementation is complete.
6. Report completion to your user with the task ID. Example: "Done — E-752 is ready for verification."
7. **Do not mark `confirmed` yourself.** Only your user does that, after verifying. If you can't easily verify but believe it works, run `endless task assume <id> --outcome "..."` instead.

When implementation is verified, land the work with `endless worktree land <id>` (auto-commits endless-managed files, rebases onto main, fast-forwards, removes the worktree).

## Sections

For details, run `endless guide <section>`:

- **lifecycle** — statuses, phases, blocking semantics
- **fields** — `title`, `description`, `text`, `prompt`, `analysis`, `notes`, `outcome` — what each is, what flag loads it
- **tasks** — full task CRUD reference (add, update, move, remove, search, link/unlink, decline, replace, etc.)
- **spawn** — spawning another Claude session for a task (`task spawn`, `task prompt`, `--prompt`)
- **worktree** — per-task worktrees, `worktree land`/`drop`, commit-to-main policy
- **decisions** — documenting decisions as first-class items (STRONG guidance — read this)
- **channels** — inter-session messaging between concurrent Claude sessions
- **snapshots** — plan-file snapshots written by the PostToolUse hook
- **verbs** — registered action verbs that validate task titles
- **projects** — registering and inspecting projects
- **sql** — read-only SQL against the Endless DB
- **tmux** — tmux status-line integration (brief; the feature is evolving fast)
- **layout** — where the DB, ledger, config, and binaries live
- **patterns** — common workflows (starting new work, recording discoveries, etc.)

Run `endless guide --list` to print just the section slugs.

## Important notes (always relevant)

- **Don't mark items `confirmed`.** Set them to `verify` and let your user confirm — or `assume` if you can't easily verify.
- **Always claim before writing code.** Even when enforcement is off, claiming registers your session and creates the worktree.
- **Use the worktree.** Don't make project changes in the `main` checkout's working tree (E-1199, E-1200).
- **Use `--llm` for agent-friendly output.** `task list --llm`, `task show --llm`, `task next --llm`, etc. produce a compact token-efficient form.
- **Tasks have hierarchy.** Use `--parent <id>` when adding child items.
- **Verify preferences before recording prohibitions.** When recording decisions, distinguish soft preference from hard rule. See `endless guide decisions`.
