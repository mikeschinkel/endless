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

### Why a worktree is unsettled (`task unsettled`)

`session status` marks a row with **◆** when its worktree is *unsettled*. Per ED-1540 that is a union of two sub-states which need **opposite fixes**, so the marker alone doesn't tell you what to do:

| Sub-state    | Meaning                                | Fix                        |
|--------------|----------------------------------------|----------------------------|
| `modified`   | Uncommitted working-tree changes       | Commit or discard          |
| `unlanded`   | Commits on the branch not yet on `main`| `endless worktree land <id>` |

Because ◆ means *there is still something to do here*, an unsettled row is never rendered dim — not when its status is terminal (`confirmed`/`assumed`/`completed`), not when its phase is `later`/`maybe`. Dim reads as "done, ignore me", which is precisely the wrong signal for a worktree still awaiting a land (E-1707).

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

Endless auto-commits a fixed, narrow set of its own files — and none of them is your work:

| Path                                                 | Committed by                                       |
|------------------------------------------------------|----------------------------------------------------|
| `.endless/verbs.jsonl`                               | endless, on `worktree land`                        |
| `.endless/db-ledger/*.jsonl`                         | endless, on the main checkout, via the event hook  |
| `.endless/plans/E-<id>.md`                           | endless, when it writes the plan into the worktree |
| **everything else — source, docs, tests, config**    | **you, with `git commit`**                          |

The two exclusions in the `git add` above are not cosmetic. Ledger entries are recorded **on the main checkout only**; a ledger commit that rides a task branch into `main` would rebase a branch-authored segment into shared database history, so `land` refuses outright (`the branch has N commits modifying the database ledger`). A blanket `git add -A` in a worktree the event hook has written to is the usual way that happens. Leave both paths alone and let endless commit them.

That same partition is why "auto-commits endless-managed modifications" in step 1 of `land` below is not a safety net for *your* files. If your changes are still uncommitted, `land` refuses:

```
worktree for E-<id> has uncommitted user changes; cannot land.
```

The omission surfaces under three different names, all meaning "you never committed": `worktree check` reports the files as a handoff anomaly, `task unsettled` reports the worktree `modified`, and `land` refuses. Commit **before** you flip the task to `unverified` — work that exists only in a dirty working tree isn't reviewable, and the user can't land it without writing your commit for you.

### Landing the work

When the task is verified (or you're using `assume`):

```bash
endless worktree land <id>
endless worktree land <id> --dry-run        # preview without making changes
```

`land` performs:

1. Auto-commits endless-managed modifications (verbs.jsonl, ledger entries) — these auto-commit to main as global-config artifacts.
2. Rebases the task branch onto current `main`.
3. Fast-forwards `main` to the rebased tip.
4. Removes the worktree.

**Do not merge to main any other way.** `worktree land` is the single sanctioned path. The exception is global-config artifacts (verbs.jsonl, db-ledger entries) which auto-commit to main directly.

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
endless worktree drop <id> --force          # refuses modified/unlanded/foreign without this
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

The handoff is deliberately lean — it delegates the workflow rules to `endless guide` rather than restating them. It carries: the spawned task's id and title, the pointers to run `endless guide` and `endless task show <id> --text`, and the drive-to-completion rules (flip to `unverified` with how-to-test; don't `worktree land`/`drop` without asking; file drive-by work as separate tasks with `--cleans-up <id>`).

To change what every spawned session is told, edit the template — see [Customizing handoff templates](#customizing-handoff-templates). There is no per-task prompt to maintain.

Every handoff's closing `Final message` line follows one discipline: **report only what `endless session status` can't already show.** For git state it defers to `endless worktree check`, which prints one line per genuine anomaly and stays silent when the worktree is clean — so a spawned session relays whatever that command prints and otherwise says nothing about git (a branch ahead of main and the absence of stray files are not anomalies). Beyond git it surfaces state outside endless (CI, services) only when actually in play, plus the how-to-test. It must **not** recap the task's status, phase, or relationships (`session status` renders those already), and must **not** confirm the negative ("no stray files", "nothing to report") — both are duplication that adds to the information overload Endless exists to reduce.

### `endless task spawn`

```bash
endless task spawn <id>                           # foreground: new tmux window
endless task spawn <id> --bg                      # background: headless supervised agent
endless task spawn <id> --attach <id>             # open a tmux window onto an already-running bg agent
endless task spawn <id> --permission-mode plan    # override the spawned session's permission mode (default: auto)
endless task spawn <id> --model <model>           # pass a --model through to the spawned claude (optional)
endless task spawn <id> --worktree <path>         # cd to <path> instead of the spawn-created worktree
endless task spawn <id> --reopen                  # reopen a terminal-status task before spawning
endless task spawn <id> --force                   # allow spawn on a done-ish task (demotes status)
```

Foreground flow:

1. Validates tmux is running (fails otherwise).
2. Refuses if the task is in a done-ish status (`unverified`/`confirmed`/`declined`/`obsolete`/`assumed`/`completed`) without `--force` or `--reopen`, or if another live session already owns the task.
3. **Pre-claims the task**: flips status to `underway` (emitting `task.status_changed`) and creates the per-task worktree at `.endless/worktrees/e-<id>/`.
4. Renders the handoff from the template and writes it to a temp file.
5. Launches Claude as the tmux window's *command* through the `endless-go spawn-window` launcher: the launcher creates a window named `<project>_<slug>[E-NNNN]` at the spawn-created worktree (or `--worktree <path>`), sets the window variables `@endless_spawned_by`, `@endless_task_id`, `@endless_project_id` in-process **before** exec, then execs `claude --permission-mode auto` with the handoff as its positional prompt argument. The handoff text never touches a command line or the session environment, and there is no send-keys, no readiness sleep, and no plan-mode step.
6. The spawned Claude's `SessionStart` hook reads `@endless_spawned_by` and records the session→task binding (no status flip — spawn already did it). Because the launcher sets the window options before exec, this read no longer races the launch.

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

`spawn --bg` returns immediately; the agent runs on its own. To watch or steer it afterward, use an attach verb below.

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

## Verification suites & the one-command handoff

How a task proves itself before it lands, and how a session hands that proof back without burying you in instructions.

### One suite per task

Every task carries **one verification suite** — a single, self-contained proof that the change does what it claims. Today that suite is realized as a bash script at `tests/tasks/e-<id>-verify.sh`. A good suite:

- Builds its own isolated environment (temp `HOME`/`XDG`, a throwaway fixture dir) so it never touches your real config or the ledger.
- Prints pass/fail per check, then a summary that ends in `ALL PASSED`.
- Exits `0` when everything passes and non-zero on any failure.

Copy the shape from any existing `tests/tasks/e-*-verify.sh` — the section/report/summary helpers are the same across them. The suite folds in the task's own unit tests as a first, fail-fast check, so the one script is a complete proof for that task **at land time**.

### A verify suite is a land-time gate, not a standing regression suite

A verify suite proves *one* task before it lands. Running it is a **one-shot, land-time gate**: whether it still runs — or passes — after that task lands is undefined, and nothing re-runs it for you. It is not the project's regression suite. So:

- **Don't run another task's already-landed verify suite** to check your work. A failure in it after land is meaningless — its fixtures and assertions were pinned to that task's moment, and the code around it has since moved on.
- **Don't edit a landed task's verify suite.** It records what was true when that task landed; retrofitting it to a later change rewrites that history. If your change alters a string or behavior a landed suite asserted, leave the suite alone.
- **Coverage that must survive belongs in the project's own test suite** (what `just test` / `go test` exercises), not only in a verify suite. If a verify suite is the *only* place a behavior is checked, that behavior is unprotected the moment the task lands — mirror it into the durable suite.

### The `verify.toml` manifest

The settled form a suite takes as the runner matures is a per-task manifest — a **pointer to native test runners**, never a re-description of the tests. It lives at `.endless/tasks/<id>/verify.toml`:

```toml
schema = 1
task   = "E-1607"

# preconditions, all optional; run provision(needs) -> setup -> seed -> checks -> teardown
needs    = ["postgres"]            # substrate the checks require
setup    = ["go build ./..."]      # prepare the project (build/install/migrate/codegen)
seed     = ["fixtures/base.json"]  # load state
teardown = ["scripts/cleanup.sh"]
tiers    = ["sandbox"]

[[check]]
runner = "gotest"                  # first-class runner: gotest | pytest
tests  = ["TestDiscover"]          # structured selection Endless translates to the native filter
paths  = ["./internal/verify/..."]

[[check]]
runner  = "bats"                   # any other runner is "raw"
command = "bats tests/cli.bats"    # the literal command a bare clone runs
format  = "tap"                    # native result stream: gotest-json | pytest-json | tap
```

A first-class `runner` (`gotest`, `pytest`) takes a structured `tests`/`paths` selection and Endless infers its `format`; any other runner is raw — you give it a literal `command` and declare its `format` (default `tap`). A project-level `.endless/verify.toml` (same directory, one level above the per-task suites) carries shared `setup`/`teardown`/`seed`/`needs` that compose beneath every per-task manifest, so a project states its common substrate once. Discovery is purely by convention: `.endless/tasks/<id>/verify.toml` per task, plus the project-root `.endless/verify.toml`.

### The runner

`endless task verify <id>` runs a task's suite under an isolated temp working dir and env, normalizes the native result streams to a single report, prints a pass/fail summary, and exits `0` on all-pass. (With no id it verifies the current session's task.) Endless's own suites are still bash scripts pending migration to a manifest, so for those, run the script directly.

### The one-command handoff contract

This is the load-bearing policy. When a session hands a finished task back for verification, it must:

1. **Run the project-wide regression itself** — the full test suite, lint, build — and **report the outcome in prose.** Never hand the user a list of commands to run to check the work; the session runs them and states the result.
2. **Hand the user exactly ONE verification command** — `esu && ./tests/tasks/e-<id>-verify.sh` — and nothing more.
3. **Fold the task's own tests into that one suite** as a fail-fast check, so the single command is a complete proof.
4. **Never enumerate a manual checklist.** No "to test: run A, then B, then check C." One command, or nothing.

The point is that verification is *dense*: one line the user runs, one prose sentence on regression, done. A handoff that lists five things to try by hand has failed the contract even if every item is correct.

### Forthcoming

The `just verify` wrapper for Endless's own suites, the runnability modes (how a suite declares what substrate it can run against), and the sandbox/mise tier ladder are in progress. Until they ship, realize the convention with the per-task script.

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
