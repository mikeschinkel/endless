# Orchestration: Worktrees, Shell Helpers, Spawning

How sessions are isolated, navigated, spawned, and handed off. Everything multi-session lives here.

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

- `--db main` — the main database at `~/.config/endless/endless.db`. Use it for
  **managing the project**: filing tasks, claiming, status updates, ledger entries.
- `--db sandbox` — this worktree's throwaway DB under
  `~/.cache/endless/sandboxes/worktree-e-NNN/`. Use it for **testing endless
  itself** so experiments never touch the main database.

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

### Stale worktrees (`worktree sync`)

A worktree branched before a change landed does not have that change, and keeps
not having it until someone rebases. A fix to a file every task shares can
therefore reach `main` and reach no working checkout at all — invisibly, in
every worktree at once.

```bash
endless worktree sync                       # report which worktrees are behind
endless worktree sync --apply               # rebase the ones that safely can be
```

Dry run by default. It skips any worktree holding uncommitted changes (that is
a session's in-flight work) and the one you are standing in, aborts and reports
a rebase that conflicts, and never removes anything. A worktree it cannot sweep
is left exactly as it was, for its own session to rebase in place.

### Why a worktree is unsettled (`task unsettled`)

`session status` marks a row with **◆** when its worktree is *unsettled*. That is a union of two sub-states which need **opposite fixes**, so the marker alone doesn't tell you what to do:

| Sub-state    | Meaning                                | Fix                        |
|--------------|----------------------------------------|----------------------------|
| `modified`   | Uncommitted working-tree changes       | Commit or discard          |
| `unlanded`   | Commits on the branch not yet on `main`| `endless worktree land <id>` |

Because ◆ means *there is still something to do here*, an unsettled row is never rendered dim — not when its status is terminal (`confirmed`/`assumed`/`completed`), not when its phase is `later`/`maybe`. Dim reads as "done, ignore me", which is precisely the wrong signal for a worktree still awaiting a land.

`task unsettled` expands the marker:

```bash
endless task unsettled <id>                 # full breakdown for one task
endless task unsettled --all                # survey every worktree, one line each
endless task unsettled --all --include-settled   # include the settled ones too
```

A target is required — bare `task unsettled` is an error. The survey walks every worktree on disk and probes each with git, so it is asked for explicitly rather than stumbled into.

The per-task form lists exactly which files are uncommitted — separating **your** work from endless's own auto-managed files (`verbs.jsonl`, ledger entries), which `worktree land` commits for you — and which commits are not yet on main, with the land command to run.

It is the inverse of `task landed`, and it reads the *same* probe that raises the ◆, so the marker and its explanation cannot disagree. Note both are fail-open: if a git call fails the verdict reads "settled", and the command says so rather than claiming the tree is clean.

Distinct from `worktree check`, which reports *handoff anomalies* and is deliberately silent about commits ahead of main (the normal pre-land state). Use `worktree check` at handoff; use `task unsettled` when you want to know why something hasn't landed.

### Committing your work

**Endless never commits your work for you. You commit it, on the task branch, before you hand off and before you land.** Nothing downstream does it on your behalf.

From inside the worktree:

```bash
git status --short                                   # see what's yours
git add -A -- ':!.endless/db-ledger' ':!.endless/verbs.jsonl'
git commit -m "E-<id>: what changed"
```

#### The commit message

**Form: `E-<id>: <verb-first summary>`.** The task id that owns the change, a colon, then what the commit does — imperative mood, lower-case after the colon, no trailing period, one line:

```
E-101: route closed tasks to ⇥ instead of ⁇ in session status
E-102: show session provenance on `task show`
E-103: add the missing commit-your-work step to the guide
```

Your commits ride into `main` on `worktree land`, so the subject line is what `main`'s history says about the task forever. The id prefix is the load-bearing part: it makes every landed change traceable back to the task that justified it (`git log --grep 'E-101'`), which is the whole point of tracking intent. Endless's own auto-commits never carry a task id — they use subjects of their own (`Endless: record ledger entry`, `lesson: <summary>`) — so yours and its are distinguishable at a glance in `git log --oneline`.

One task's work is normally one commit; split into several only when the steps are independently reviewable, and prefix each with the same id.

Endless auto-commits a fixed, narrow set of its own files — and none of them is your work:

| Path                                                 | Committed by                                       |
|------------------------------------------------------|----------------------------------------------------|
| `.endless/verbs.jsonl`                               | endless, on `worktree land`                        |
| `.endless/db-ledger/*.jsonl`                         | endless, on the main checkout, via the event hook  |
| `.endless/LESSONS.md`                                | endless, on the main checkout, at write time       |
| `.endless/plans/E-<id>.md`                           | endless, when it writes the plan into the worktree |
| **everything else — source, docs, tests, config**    | **you, with `git commit`**                          |

Two of those never wait for `land`: a ledger entry is committed by the event hook as it is written, and a lesson is committed by `endless lesson write` as it is written. Both land on the main checkout, in a single-file commit, from wherever you ran the command — which is why recording a correction does not oblige you to re-land a task that was already finished.

The two exclusions in the `git add` above are not cosmetic. Ledger entries are recorded **on the main checkout only**; a ledger commit that rides a task branch into `main` would rebase a branch-authored segment into shared database history, so `land` refuses outright (`the branch has N commits modifying the database ledger`). A blanket `git add -A` in a worktree the event hook has written to is the usual way that happens. Leave both paths alone and let endless commit them.

That same partition is why "auto-commits endless-managed modifications" in step 1 of `land` below is not a safety net for *your* files. If your changes are still uncommitted, `land` refuses:

```
worktree for E-<id> has uncommitted user changes; cannot land.
```

The omission surfaces under three different names, all meaning "you never committed": `worktree check` reports the files as a handoff anomaly, `task unsettled` reports the worktree `modified`, and `land` refuses. Commit **before** you flip the task to `unverified` — work that exists only in a dirty working tree isn't reviewable, and the user can't land it without writing your commit for you.

### Landing the work

**Do not run `worktree land` (or `worktree drop`) without asking your user first — every time, including when you are certain the work is done.** This is a standing rule, not a per-task instruction: a session that has lost its original prompt to a context compaction still may not land on its own initiative.

Why it is the user's call and not yours:

- Landing publishes your branch into shared `main` history and advances it. Undoing that means rewriting `main`.
- `unverified` means *awaiting the user's verification*. Landing your own work asserts the verification you were told not to perform — and `assume` is for "I can't test this," not for "I've decided it's approved."
- The user is the only one who knows what else is in flight: other worktrees mid-rebase, a release in progress, work that should land in a particular order.

So: reach `unverified`, hand over the one command that verifies it, and stop. Landing is what happens after your user says so.

When your user has said to land, and the task is verified (or you're using `assume`):

```bash
endless worktree land <id>
endless worktree land <id> --dry-run        # preview without making changes
```

`--dry-run` is the exception to the rule above: it changes nothing, so use it freely to preview what a land would do.

`land` performs:

1. Auto-commits endless-managed modifications (verbs.jsonl, ledger entries) — these auto-commit to main as global-config artifacts.
2. Rebases the task branch onto the project's current default branch.
3. Fast-forwards that branch to the rebased tip.
4. Records the landing (`task.landed`). **The worktree directory and its branch stay put** — `land` never removes them. A separate reaper sweep (`worktree reap`) deletes a landed worktree once it is older than `worktree_ttl` (`.endless/config.json`, default 14d) and no live process holds a cwd inside it. Retention is what makes re-landing work: commit a follow-up on the same branch and land again, and the dir and branch are reused.

**Do not merge to main any other way.** `worktree land` is the single sanctioned path. The exception is global-config artifacts (verbs.jsonl, db-ledger entries) which auto-commit to main directly.

**The default branch is resolved, not assumed.** Endless takes the first of: `default_branch` in `.endless/config.json`; `origin/HEAD`; `init.defaultBranch`; then `main` or `master`, whichever exists. Each candidate must name a branch that actually exists in the repository. If none does, Endless says so and refuses rather than guessing — a wrong base branch silently mis-reports what has landed, and would rebase onto the wrong thing. Set `default_branch` when the repository's convention is anything the detection steps cannot see, or run `git remote set-head origin --auto` to populate `origin/HEAD` on a fresh clone. The same resolution backs the ◆ unsettled marker and the worktree reaper, so all three agree on what "landed" means.

Landing into a branch that is not `main` is fully supported; nothing in Endless requires the name.

#### Post-land script

If a task's change needs a one-time action on **main** *after* it lands — most often removing the untracked files a newly-un-ignored path leaves behind (a commit only moves tracked content, so no merge can delete them), or a fixup git won't perform on merge — commit an **idempotent** `.endless/hooks/post-land/e-<id>.sh` (`chmod +x`) on your branch. It rides into main with the task, and `worktree land` runs it once right after the merge:

- **Invocation.** Exec'd directly (its own shebang) with **cwd = the main checkout** and **`$1` = the main checkout root**. `ENDLESS_TASK_ID`, `ENDLESS_MERGE_SHA`, `ENDLESS_WORKTREE_PATH`, and `ENDLESS_BASE_BRANCH` are exported into its environment.
- **Failure is non-fatal and loud.** The merge already advanced main, so a non-zero exit never unwinds the land — endless prints a warning naming the script, exit code, cwd, and the command to re-run it, then reports the land as done. Write the script so re-running it is safe; that is how a failed run is finished.
- Absent script → nothing happens. Present but not executable → a warning, and the step is skipped (the land still succeeds).

#### Post-land residue check

After the post-land script runs (or if none was shipped), `worktree land` verifies the *outcome*: it compares the files that were ignored-and-present on main **before** the land against the files that are untracked-and-present **after** it, and the intersection is **residue** — files a path this land un-ignored left behind that neither the merge nor any script removed. Empty intersection → silent (the common case, and every land that un-ignores nothing). Non-empty → a loud, actionable error listing the residual paths and pointing at the script that should have removed them (or noting none was shipped). It is **non-fatal** — the merge already advanced main — but the land **exits non-zero** so automation notices, because a *later* land could otherwise sweep the residue into a commit. Intentionally-tracked content under an un-ignored path is tracked, never untracked, so it passes silently.

### Abandoning a worktree

```bash
endless worktree drop <id>
endless worktree drop <id> --force          # refuses in-use/modified/unlanded/foreign without this
```

Use `drop` when the work is being abandoned (task declined/obsolete). Don't `drop` over `land` to skip review.

`drop` is under the same **ask-first** rule as `land` (see [Landing the work](#landing-the-work)) — more so, since it discards work rather than publishing it. Deciding that a task is abandoned is the user's decision to make, not yours to act on.

#### Diverged history is a reset, never a drop

**Never offer to drop a worktree.** Retention is the design — landing keeps the worktree and its branch so a reopened task still has one, and the reaper reclaims it after a grace period. A retained worktree is recovery state, not leftover mess. "Ask first" is a boundary, not an invitation to keep asking.

`drop` removes a **directory**. A live session is *sitting in* that directory, so dropping it deletes that session's cwd out from under it — the session is orphaned, and the only recovery is recreating the directory by hand. Reach for `drop` only when the *work* is being abandoned.

Twice recently a session recommended dropping a worktree because its git history had diverged from `main`, when no committed work would have been lost and the correct move was to fix the branch **in place**:

```bash
git -C <worktree> rebase main          # replay the branch on current main
git -C <worktree> reset --hard main    # discard the branch's commits, keep the worktree
```

Both leave the directory — and whoever is working in it — intact. "The branch is in a bad state" is a branch problem; deleting the checkout to fix it is like reinstalling the OS to rename a file.

`drop` now refuses when anything is still using the worktree — a non-ended session with the task active, or any process holding cwd inside it — and names which. That refusal is a backstop, not the rule: **if the answer is "reset or rebase", never propose the drop in the first place.** `--force` skips the check; using it to get past a refusal you did not read is how the orphaning happens.

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
| `esm`    | "Endless session monitor." Live dashboard — re-renders `session status` every 2s until Ctrl-C. Passes through `--all` / `--tree`.        | Watching a spawned session work without polling by hand.  |
| `eeh`    | "Endless errors here." Runs `errors show`: the recorded errors the `session status` badge is counting, plus how to dismiss them. Takes `--detail` / `--all`. | The badge says `Run eeh` — this is what it means.        |
| `eswt`   | *(Planned, not yet shipped.)* "Endless switch worktree." Pure `cd` to a task's worktree, given a task ID. Distinct from `esu` in that it does not export `ENDLESS_SESSION_ID`. | Quick navigation without binding. Until shipped, use `cd "$(endless worktree for-task <id>)"`. |

`esm` and `eeh` are read-only views and need no session binding.

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

Spawn opens a new tmux window with Claude visible and interactive. tmux is the only delivery surface — Endless does not dispatch headless background agents.

The window is built as three panes: Claude on the **left** at half width and full height (focused when the window opens), `endless session monitor` **top-right**, and a bare `$SHELL` **bottom-right** for ad-hoc `endless` commands. The monitor resizes its own pane to the frame it is rendering on every repaint, capped at 80% of the window height, so the shell keeps the rest of the column; with no rows to show it holds a small fixed block rather than collapsing to a sliver.

**Pane working directories are not the same.** Claude's pane gets the task's **worktree** — that pane is the branch's work. The monitor and shell panes get the **project directory**, because the Python CLI routes its DB from cwd: run from inside a `self_dev` worktree, every ad-hoc `endless` command in the shell pane needs an explicit `--db main` to reach the main database. The monitor pane follows the same rule for consistency, though its own view no longer depends on it — `session-status` pins the main DB regardless of cwd, since session and pane state are machine-scoped rather than project-scoped. The trade-off is that `git`/`just` in the shell pane act on the main checkout, not on the task branch.

Spawn **pre-claims** the task (status → `underway`, per-task worktree created) and runs its pre-flight refusals before launching, so the spawned session always lands in a fully-claimed state and never needs to run `endless task claim` itself.

### Per-type handoff variants

The handoff is rendered from a per-type template, chosen from the task's `type`:

- **`todo`** — frames the work around a verify end-state: implement, flip to `unverified`, supply how-to-test.
- **`bugfix`** — leads with "reproduce the bug first, before changing anything."
- **`research`** — findings *are* the deliverable: end-state is `completed` with the conclusions written to the task's outcome, not a code-verify cycle.
- **`epic`** — a coordinator role (see [Coordinator pattern for epics](#coordinator-pattern-for-epics)).

Any other or unset type falls back to the `todo` variant.

### The handoff is generated, not authored

There is nothing to write. The handoff is rendered from the per-type template (`handoff/<type>.md.tmpl`) merged with the task's id and title plus runtime context (its worktree and branch). The substantive design lives in the task's `--text` plan, which the handoff tells the spawned session to read — so a prompt can no longer drift from the plan.

Inspect the exact text spawn will paste:

```bash
endless task handoff <id>
```

The handoff is deliberately lean — it delegates the workflow rules to `endless guide` rather than restating them. It carries: the spawned task's id and title, the pointers to run `endless guide` and `endless task show <id> --text`, and the drive-to-completion rules (flip to `unverified` with how-to-test; don't `worktree land` without asking, and never remove a worktree; file drive-by work as separate tasks with `--cleans-up <id>`).

To change what every spawned session is told, edit the template — see [Customizing handoff templates](#customizing-handoff-templates). There is no per-task prompt to maintain.

### Claiming into a session that's already running

Spawn is not the only way a session picks up a task. When you run `endless task claim <id>` from inside a session that has been going for a while — a retrofit rather than a fresh dispatch — you get the same type handoff, delivered as context folded against that command's own output. You don't ask for it and there is nothing to render by hand.

It differs from the spawn text only in its arrival framing, because that is the only thing that actually differs: a spawned session is born inside the task's worktree, whereas a claimed-in one is still wherever it was and has to `/cd` there (the cwd gate refuses write tools until it does), and it arrives carrying planning that belongs in the task's `--text`, not in the transcript. Everything else — which worktree, the `--db main` routing that implies, one-session-one-task, and the per-type deliverable and terminal-status rules — is rendered from the shared `handoff/_mechanics.tmpl` partials that the per-type spawn templates also pull from, so the two renderings cannot drift.

Inspect it the same way as any other template:

```bash
endless internal template render handoff/claim < vars.json
```

{{if .report_gate}}Every handoff's closing `Final message` line follows one discipline: **write the reply you mean to send, and let the minimizer cut it**. For git state it defers to `endless worktree check`, which prints one line per genuine anomaly and stays silent when the worktree is clean — so a spawned session puts whatever that command prints into its draft and otherwise says nothing about git (a branch ahead of main and the absence of stray files are not anomalies). Beyond git it surfaces state outside endless (CI, services) only when actually in play, plus the how-to-test. Recaps of status, phase and relationships, and confirmations that a problem does not exist, are what the minimizer deletes — a second party applying that judgment is the whole point.
{{else}}Every handoff's closing `Final message` line follows one discipline: **say what your user has to act on, and nothing they did not ask for**. For git state it defers to `endless worktree check`, which prints one line per genuine anomaly and stays silent when the worktree is clean — so a spawned session relays whatever that command prints and otherwise says nothing about git (a branch ahead of main and the absence of stray files are not anomalies). Beyond git it surfaces state outside endless (CI, services) only when actually in play, plus the how-to-test. It does not recap task status, phase or relationships, and does not confirm that a problem does not exist. Where `report_gate` is off there is no second party to apply that judgment, so the handoff states it outright rather than leaving it to be cut.
{{end}}
### A session owns one task for its lifetime

`sessions.task_id` is **write-once**: set when the session claims, then never
cleared and never re-pointed. Work on a different task is a different session.
The column is not bookkeeping — it is the only route back to a session's
transcript, which is where the reasoning behind the work lives.
`endless session goto E-<id> --resume` resolves through it, and a session whose
pointer was cleared reports as one that *never claimed a task*: the transcript
survives, but nothing can find it.

The rule is enforced in the database, not only in the verbs: a
`BEFORE UPDATE OF task_id` trigger aborts any write that changes an already-set
value, including a clear. The verbs refuse first and explain themselves, because
a SQLite abort is not an answer you can act on — but the trigger is what catches
a writer nobody enumerated, which is how the invariant was broken before it
existed.

Three consequences you will meet:

- **`endless task release` is disabled.** Clearing the pointer and then setting
  a new one is a re-point through the back door. The command still exists and
  answers with the invariant rather than vanishing. To stop working a task and
  leave it for someone else, hand it back by status —
  `endless task update <id> --status revisit` — which leaves your session
  reachable.
- **`endless task bind` is first-set-only.** It may fill a session that holds no
  task. It may not move a bound one; it refuses and names what the session
  already holds.
- **`endless task reopen` leaves the binding alone.** It changes task state and
  nothing else, so the session that did the work stays reachable afterwards.

The same rule governs the two places that used to clear the pointer implicitly:
confirming a task now idles the session without unbinding it, and
`endless task chat` no longer unbinds a session that already holds a task.

### `endless task spawn`

```bash
endless task spawn <id>                           # new tmux window
endless task spawn <id> --permission-mode plan    # override the spawned session's permission mode (default: auto)
endless task spawn <id> --model <model>           # pass a --model through to the spawned claude (optional)
endless task spawn <id> --worktree <path>         # cd to <path> instead of the spawn-created worktree
endless task spawn <id> --force                   # allow spawn on a done-ish task (demotes status)
```

`--reopen` is retired. Reopening settled work in a *fresh* session threw away
the session that did it — the only place its reasoning lives. Reopen in that
session instead: `endless session goto <id> --resume --revisit`.

Foreground flow:

1. Validates tmux is running (fails otherwise).
2. Refuses if the task is in a done-ish status (`unverified`/`confirmed`/`declined`/`obsolete`/`assumed`/`completed`) without `--force`, or if another live session already owns the task. On the reopenable subset (`assumed`/`confirmed`/`completed`) the refusal routes to `session goto <id> --resume --revisit` rather than offering `--force`, because a second session on settled work is rarely what you want.
2. Refuses if **any** session ever claimed the task — live or long since ended. Ownership is read off `sessions.task_id`, which is write-once, so the record survives the claiming session's death. The refusal names that session and routes to `endless session goto <id> --resume`; with several claimants it names the most recent and lists the rest. There is **no** override: `--force` governs only the status demotion, and a claim cannot be released. To work a task a prior session claimed, resume that session — starting a second one throws away the reasoning that only exists there.
3. **Pre-claims the task**: flips status to `underway` (emitting `task.status_changed`) and creates the per-task worktree at `.endless/worktrees/e-<id>/`.
4. Renders the handoff from the template and writes it to a temp file.
5. Launches Claude as the tmux window's *command* through the `endless-go spawn-window` launcher: the launcher creates a window named `<project>_<slug>[E-NNNN]` at the spawn-created worktree (or `--worktree <path>`), sets the window variables `@endless_spawned_by`, `@endless_task_id`, `@endless_project_id` in-process **before** exec, then execs `claude --permission-mode auto` with the handoff as its positional prompt argument. The handoff text never touches a command line or the session environment, and there is no send-keys, no readiness sleep, and no plan-mode step.
6. The spawned Claude's `SessionStart` hook reads `@endless_spawned_by` and records the session→task binding (no status flip — spawn already did it). Because the launcher sets the window options before exec, this read no longer races the launch.

The spawned session can discover its task ID from the tmux window variable:

```bash
tmux show-window-options -v @endless_task_id    # prints the task ID
```

### Fix a bug in your own landed work

**A bug in work you just landed is not new work.** It is the task you already
did, done wrong. Reopen that task rather than filing a peer beside it:

```bash
endless task update E-<id> --status revisit    # the task that shipped the bug
```

`revisit` is where reopened landed work belongs — the status means "needs
re-evaluation before it can proceed", which covers both a plan that no longer
holds and work that shipped and turned out wrong. `task reopen E-<id>` and
`session goto E-<id> --resume --revisit` land there too, so all three routes
agree.

Why this over filing a new task: a filed task is a standing claim on your
user's attention. It gets read, re-read, and triaged past on every pass through
the backlog, whether or not it is ever worked. Filing the bug you just
introduced does not defer its cost — it converts a small cost now into a
recurring one, and it splits one piece of work across two ledger entries that a
reader has to reassemble.

**Reuse the task's existing worktree.** Landing retains the worktree and its
branch (see [Landing the work](#landing-the-work)), so the reopened task
usually still has one:

```bash
endless worktree for-task <id>                 # path, if it still exists
endless session goto E-<id> --resume --revisit # reopen + resume the session that did the work
endless session resume <ref> --reopen          # worktree was reaped: rebuild it
```

Do **not** create a second worktree for the same task. Two worktrees on one
task means two branches landing the same work.

**Re-verify with the suite that already exists.** The task's
`.endless/tasks/e-<id>/` suite still exists and still applies — it encoded the
acceptance criteria the bug just violated. Re-run it with `endless task verify`,
and extend it with the case that escaped rather than authoring a second suite
beside it. A reopened task is still YOUR task, so the own-task-only refusal
below does not fire on it — that is the case its second condition exists for.

**When a separate task IS right.** The test is whether the discovery is a
*defect in what shipped* or *new work the conversation surfaced*. Reopen for
the first; file for the second. Signals that you are looking at new work: it
changes what the task was supposed to do rather than whether it did it; it
touches code the task never touched; it needs a decision your user has not
made yet. When in doubt, ask — the cost of asking is one message, and the cost
of guessing wrong is either a stranded task or a reopened one that never should
have been.

### Coordinator pattern for epics

Spawning a task whose type is `epic` opens a foreground window for a **coordinator**. The coordinator does **not** implement the epic's work directly — its job is to drive the epic's children through `unplanned` → `ready` → `underway` → `unverified`, dispatching child sessions and reviewing them.

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

### Customizing handoff templates

The handoff templates ship embedded in the `endless-go` binary. The first time a template renders in a consumer project, its embedded copy is **materialized** per-file to `<project_root>/.endless/templates/handoff/<type>.md.tmpl` and auto-committed, so the on-disk file is tracked and editable.

The set is: one wrapper per task type (`todo`, `bugfix`, `research`, `epic`, `brainstorm`), the `claim` wrapper for claiming into a running session, `respawn` for a reopened task, and two shared partials the wrappers pull from — `_close.tmpl` (the closing `Final message` discipline) and `_mechanics.tmpl` (the type mechanics both the spawn and claim renderings must agree on). Edit a shared partial to change a line everywhere at once; edit a wrapper to change one context's framing.

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

## Verification suites & the one-command handoff

How a task proves itself before it lands, and how a session hands that proof back without burying you in instructions.

### One suite per task

Every task carries **one verification suite** — a single, self-contained proof that the change does what it claims. It lives in that task's own directory, `.endless/tasks/e-<id>/`, in one of two forms:

| Form | File | What it is |
|------|------|------------|
| Manifest | `verify.toml` | A declarative list of `[[check]]` entries the runner executes and normalizes (`gotest`, `pytest`, any TAP-emitting command). |
| Script | `verify.sh` | A bash suite that sources `.endless/tasks/_harness.sh` and asserts. Sourcing the harness is what makes it refuse a direct run, so every suite does it. |

A task carries one or the other; during a conversion it may hold both, and the manifest wins. A good suite:

- Prints pass/fail per check, then a summary.
- Exits `0` when everything passes and non-zero on any failure.

The **runner** provides the isolation — a per-run temp `HOME` and `XDG_CONFIG_HOME`, so a suite cannot read or pollute your real config or the main database. A suite that additionally builds its own throwaway fixture dir is belt-and-braces, not a requirement.

The suite folds in the task's own unit tests as a first, fail-fast check, so the one command is a complete proof for that task **at land time**.

### Running one: `endless task verify`

```bash
endless task verify            # this session's task, or the worktree you are in
endless task verify E-<id>     # a named task
```

This is the only front door, and it is the front door for both forms — which is the point: the command a session hands back is identical whether the suite is a script today or a manifest later.

**It is sufficient on its own — no `cd` first, and nothing to remember.** With no id it takes the session's active task, falling back to the task whose worktree you are standing in (the same two sources `just land` uses, and the second needs no database, so it works outside tmux and in a self-dev worktree with no `--db`). Having resolved the task it runs the suite **in that task's worktree**, because a suite is a pre-land gate and has to prove the candidate tree — asking from the main checkout would otherwise run main's copy against code the task hasn't landed.

The runner exports three variables into the suite's environment: `ENDLESS_VERIFY_TASK` (the id), `ENDLESS_VERIFY_DIR` (the suite's own directory — read files you ship beside a suite from here rather than typing the path), and `ENDLESS_VERIFY_RUN` (the per-run temp dir, whose presence is what proves the runner started you).

Running a `verify.sh` directly is not a shortcut, it is a different (and worse) thing: it skips the isolation, and it skips the check that the suite is yours. Every suite refuses a direct run and names the command to use instead.

**The runner refuses another task's landed suite.** Ask it for a suite that has landed and is not the task you are working on — not your session's task, and not the task whose worktree you are standing in — and it exits non-zero without running anything, naming what to run instead. Both halves of that condition matter: a task that landed and was then *reopened* is still your task, so re-verifying and re-landing your own work is untouched.

> Inside Endless's own checkout, `just verify [E-NNNN]` adds one self-dev-only thing: `--db sandbox`, which selects the **worktree's** `endless-go` so a pre-land gate exercises candidate code rather than main's. Everything else it does, `endless task verify` now does itself.

### A verify suite is a land-time gate, not a standing regression suite

A verify suite proves *one* task before it lands. Running it is a **one-shot, land-time gate**: whether it still runs — or passes — after that task lands is undefined, and nothing re-runs it for you. It is not the project's regression suite. So:

- **Don't run another task's already-landed verify suite** to check your work. A failure in it after land is meaningless — its fixtures and assertions were pinned to that task's moment, and the code around it has since moved on. The runner enforces this rather than asking; a glob over the suite directory is the same mistake at scale, and gets the same refusal at the first foreign suite.
- **Don't edit a landed task's verify suite.** It records what was true when that task landed; retrofitting it to a later change rewrites that history. If your change alters a string or behavior a landed suite asserted, leave the suite alone.
- **Coverage that must survive belongs in the project's own test suite** (what `just test` / `go test` exercises), not only in a verify suite. If a verify suite is the *only* place a behavior is checked, that behavior is unprotected the moment the task lands — mirror it into the durable suite.

The same rules sit in `.endless/tasks/CLAUDE.md`, at the point of contact, and the runner's refusals point there.

### The one-command handoff contract

This is the load-bearing policy. When a session hands a finished task back for verification, it must:

1. **Run the project-wide regression itself** — the full test suite, lint, build — and **report the outcome in prose.** Never hand the user a list of commands to run to check the work; the session runs them and states the result.
2. **Hand the user exactly ONE verification command** — `esu && endless task verify` — and nothing more.
3. **Fold the task's own tests into that one suite** as a fail-fast check, so the single command is a complete proof.
4. **Never enumerate a manual checklist.** No "to test: run A, then B, then check C." One command, or nothing.

The point is that verification is *dense*: one line the user runs, one prose sentence on regression, done. A handoff that lists five things to try by hand has failed the contract even if every item is correct.

### Forthcoming

The runnability modes (how a suite declares what substrate it can run against) and the sandbox tier ladder are still in progress. Until they ship, a suite runs at Tier 0 — a temp working dir plus an isolated `HOME`/`XDG_CONFIG_HOME` — and a manifest that declares `needs` or `seed` fails loudly rather than running something weaker than it asked for.
