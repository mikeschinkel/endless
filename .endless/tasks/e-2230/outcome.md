# E-2230 synthesis: how outcome-producing tasks signal they're done, where they work, and the experiment, prototype and tool types

## The problem, restated

Mike can tell a task is finished when its worktree is settled. That works only for tasks with a **landed** result. For tasks whose deliverable is an **outcome**, settled means nothing. `unreviewed` already means "the agent thinks it's done," but the monitor doesn't put it first. And accepting the work isn't one step: `task complete` demands `--outcome` even when the task already has one, so the user has to write another prompt asking the agent to complete the task. Brainstorms make it worse because filing follow-ups swamps the finish.

## 1. Behavior keys off properties, not type names

Endless code should test a property that each type declares, never the type's name ("todo and bugfix tasks", "research and brainstorm tasks"). The properties identified so far:

- **Deliverable:** one or more of `landed` (a branch merged to main), `outcome` (text on the task), `artifact` (code kept but not merged into the project).
- **Isolation:** what claim creates under `.endless/work/e-NNNN/`. Either a **project worktree** (a checkout of the project), or an **empty repo** (`git init` on an empty directory). A task that runs code (a project worktree, a prototype, a tool) gets a sandbox, which at minimum is a subdirectory. Research and brainstorm don't: they run no code and default to the main database (open: Mike to confirm).
- **Default database:** which database `endless` commands use inside the task directory. For a task whose only deliverable is an outcome, the default is main, so no `--db main` is needed.
- **Interaction:** plan-driven or collaborative. This affects the handoff template and probably more. Mike vetoes ever offering a collaborative todo.
- **Branch pattern:** for example `task/NNNN` or `experiment/NNNN`.
- **Retention / land process:** merge to main; keep the branch and never merge; copy the work into a named directory on main; or nothing to land.
- **Done signal and acceptance:** every lane has an "agent done" step that the "actually done" step must follow. Today these are `unverified` then `confirmed`/`assumed` for landed work, and `unreviewed` then `completed` for outcome work.

Being settled is a gate on accepting `landed` work, not a "done" signal in itself.

Graduation (an experiment or prototype becoming project code, a tool becoming an Endless feature) stays an ad-hoc process between the user and the agent. It only becomes a workflow if the ad-hoc version proves both needed and tedious.

## 2. Where a session works: `.endless/work/e-NNNN/`

- Every task's directory lives under `.endless/work/e-NNNN/`, inside the repo (gitignored). "Work" is shorter than "worktrees" and covers both kinds of directory.
- Outside the repo is rejected because it is a nightmare for IDE users. A plain directory inside main's tree is rejected because `git` run there finds main's repository.
- The **empty-repo** form gives research, brainstorm, prototype and tool tasks a working directory where `git` is harmless. A prototype's or tool's code sits at the top of that directory, not deep inside a copy of the project.
- Land never removes a task directory. The reaper removes it once no session is in it, exactly as it does for worktrees today.

## 3. Three new types

| | experiment | prototype | tool |
|---|---|---|---|
| What it is | an approach tried on the project's own code | a standalone proof-of-concept (for example, vector search or TOML in Go) | a standalone utility to keep and run again (for example, crash or transcript recovery) |
| Isolation | project worktree | empty repo | empty repo |
| Branch | `experiment/NNNN` | n/a | n/a |
| Lane | findings: `unreviewed` → `completed` | findings | verification: `unverified` → land → `confirmed` |
| Completion | `--keep` or `--discard` required. Keep preserves the branch; it is never merged | `--keep` or `--discard` required. Keep copies the directory to `.endless/prototypes/e-NNNN/` on main | Land copies the directory to `.endless/tools/e-NNNN/` on main |
| Tests | n/a | not required | its own test suite, which lives in the tool's directory and must pass before land; never part of the project's test suite |

Common to prototypes and tools:
- They are **self-contained**: they depend only on external packages. Anything that needs the project's own code is an experiment.
- Each needs a `README.md` with YAML front matter: task id, title, the question it answers or the problem it solves, how to run it, and (for a tool) how to run its tests. Land refuses without it. The front matter is how the test command is found in any language.
- Directories are named `e-NNNN`, never `e-NNNN-<slug>`; slugged worktree names became a mess before. They are found through the task list (`task list --type tool`), and by browsing via generated indexes.
- A periodic job regenerates `INDEX.md` from the front matter in `.endless/tools/`, `.endless/prototypes/` and `.endless/tasks/`, and commits it to main the way the ledger's auto-record commits are made.

"Spike" is dropped as a name: average users know "experiment" and "prototype," and few know "spike."

## 4. Signalling done, and accepting in one step

- **Agent done** is a status the agent sets with a command (`unreviewed` or `unverified`). The monitor shows it first, as "ready for you."
- "Is it waiting for me?" matters less. The tmux tab asterisk already covers it. A session-state cue can come later.
- **Accepting** should be one user command. For outcome work that is `endless task complete E-nnnn`. Every task that needs an outcome must have one, but when the task is `unreviewed`, the agent has already declared its outcome final, so `complete` uses that outcome and does not ask for another.
- Follow-ups become part of the deliverable. The agent **drafts** them through the same gates as filing (a `--draft` mode that validates without writing). The drafts are embedded in the outcome as YAML blocks, each naming a domain operation rather than a CLI command line. `task complete` re-validates and files them. The agent makes any edits to the drafts that the user asks for.
- Once filing is part of `complete`, the session has nothing left to do, so Endless does not need to message it to close. That rules out a mod, `claude -p`, or tmux send-keys; tmux send-keys was already ruled out for anything load-bearing.

## Plan questions this resolves

- **Q1:** new types `experiment`, `prototype`, `tool`, chosen through the property model.
- **Q2:** every task gets a directory under `.endless/work/`. Research and brainstorm get an empty repo, not a project worktree, and use the main database by default.
- **Q3:** drop ◆ as the "done" cue where the deliverable isn't `landed`. Show "agent done" (`unreviewed`/`unverified`) first.
- **Q4:** both in-place (experiment) and standalone (prototype, tool), as separate types.
- **Q5:** `.endless/prototypes/e-NNNN/` and `.endless/tools/e-NNNN/`, committed to main. No `.endless/experiments/`: an experiment's code is a diff and lives on its branch.
- **Q6:** prototypes and tools are built at the top of their empty repo and copied into their directory on main at land. Experiments are kept as a branch.
- **Q7:** keep or discard is required at completion for experiments and prototypes. Tools are verified and landed.

## Open, left to follow-ups

- Moving the existing `.endless/worktrees/` to `.endless/work/` is a migration: the reaper, gitignore, hooks and path checks all refer to it.
- The README metadata format: standard `---` front matter, which GitHub shows as a table above the title; YAML inside an HTML comment, which is hidden but needs a custom parser; or a separate metadata file.
- Whether a copy-on-land keeps the empty repo's commit history in any form.
- Copy-on-write worktrees (git-cow-worktree, git-sprout) were mentioned as a separate, later idea.
