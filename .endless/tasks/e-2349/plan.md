# Bring a registered project up to Endless usage

All code is in the `endless` repo. Python owns the CLI and the file work
(`src/endless/`); Go owns the database (`internal/monitor` holds the queries,
`internal/projectcmd` dispatches the `endless-go project <verb>` verbs), and
Python reaches Go through `event_bridge.project_registry`.

Where a choice is the user's, this plan names a recommended default and the
task carries the question (listed under Open questions); an answer other than the default changes only the
section that names it.

## The bring-up command: `endless project init`, re-run

Recommended default: no new verb. `project init` (alias `register`) is already
idempotent — `register_project` merges `.endless/config.json`,
`scaffold_gitignore` appends only missing lines, `scaffold_suite_rules` never
overwrites — and it is the command a user already knows. This task extends it so
that running it on a project registered at any earlier time brings that project
fully up to date, and running it again changes nothing.

`register.register_project`, in order:

1. **Refuse a worktree** (new, before anything is written): refuse when the
   target is a git linked worktree (`git rev-parse --git-dir` differs from
   `--git-common-dir`) or lies under a registered project's
   `.endless/worktrees/`. The refusal names the owning project. This does not
   depend on the row's `identity` column, which E-2217's duplicate check needs
   and older rows may lack.
2. Existing steps, unchanged: ignore marker, identity, duplicate and name
   checks, the config merge, the DB row.
3. **Gitignore** (`scaffold_gitignore`): `GITIGNORE_ENTRIES` gains
   `.endless/logs/`, where `internal/monitor/session_notices.go`
   `AppendNoticeLog` writes `session-notices.jsonl`.
4. **Suite rules** (`scaffold_suite_rules`): unchanged.
5. (Withdrawn per EQ-84: `_harness.sh` and `_guard.sh` are not installed in other
   projects; `verify.toml` is the suite form for PRODUCT, and E-2419 moves the
   advice to it. Updating an existing project's `.endless/tasks/CLAUDE.md` is
   E-2421's (Update an unedited suite-rules copy on project init).)
6. **Agent-instructions pointer** (new, `register.scaffold_guide_pointer`): if
   the project root has neither `CLAUDE.md` nor `AGENTS.md`, create `CLAUDE.md`
   holding a short block that tells an agent to run `endless guide` first. If
   either exists and neither mentions `endless guide`, append the same block,
   under a marker line, to `CLAUDE.md` if present, else `AGENTS.md`. A file
   that already mentions `endless guide` is left alone.
7. **Commit** (new; rule per EQ-83). Before step 2 runs, record
   `git status --porcelain` for the candidate paths:
   `.endless/config.json` and `.endless/tasks/CLAUDE.md` (Endless-owned), and `.gitignore`, `CLAUDE.md`, `AGENTS.md` (user-owned).
   After step 6, the commit set is:
   - every Endless-owned candidate path that is now modified or untracked,
     whatever its state before the run (this commits the legacy, never
     committed `config.json`);
   - every user-owned candidate path the run changed, but only if it had no
     uncommitted change before the run: not modified, not staged, and not an
     untracked file the user had already made. A file the run itself created
     (e.g. a new `CLAUDE.md`) had no prior change, so it is committed.
   If the checkout is not on its default branch, the commit set is empty.
   Commit the set with one pathspec commit (`git commit -- <paths>`, so
   nothing else the user staged is swept in; message
   `Endless: bring project up to Endless usage`), then print every changed
   path left out and why. An empty set commits nothing.

Idempotence follows from each step writing only what is missing or different,
and step 7 committing only what changed.


## Land does not mistake Endless's runtime files for user work

The task's notes ask for this, and E-2363, which asked for it alone, was
superseded into this task: without it, the first land after bring-up in a repo
whose `.gitignore` predates an entry is refused. `worktree_cmd._git_status_partition`
gets a third class, Endless runtime paths, taken from `GITIGNORE_ENTRIES`
(shared, not copied), which land neither commits nor counts as user work.

## A clean way to drop a mistaken registration

`internal/monitor` gains `DeleteProjectRow`, which deletes a project's row by
path; `internal/projectcmd` gains a `delete` verb that calls it; Python reaches
it through `event_bridge.project_registry("delete", path)`. A new `project`
verb in `cli.py`, `project forget` (EQ-86), calls it. It writes nothing to the
project's `.endless/config.json` and adds nothing to the ignore list, unlike
`project unregister`, which does both. A row that has tasks (EQ-87): by default `forget` refuses, naming the task
count and pointing at `project ignore`, which hides the project and keeps its
history without writing to its `config.json` (unlike `unregister`).
`--with-tasks` removes the project's tasks first, through the existing
`task.deleted` event that `task remove --cascade` emits, so a rebuild from the
ledger does not bring them back. Removal marks a task row removed and never
deletes it (ED-1547), so those rows still reference the project; what happens to
the project row then is an open question (EQ-98).

## Apply it to go-cfgstore

After it lands, run `endless project init` in go-cfgstore's main checkout so it
gains `.endless/logs/` in `.gitignore`, and the commit.

## Fold-in

`task_cmd._report_gate_on`'s docstring says the minimizer defaults on; E-2373
made the default off. Correct it.

## Out of scope

- The minimizer block: since E-2373 a missing block means off, so bring-up has
  nothing to write.
- Porting a Makefile to a justfile: that is the user's repo policy, not
  something Endless knows how to do for an arbitrary project. It is done per
  repo (go-cfgstore's was done in E-2206).
- Health checks and reporting: `endless doctor` (E-2287) owns those.

## Open questions (asked on the task as EQ-81..EQ-88 and EQ-98)

The defaults above are recommendations, not decisions; each is asked:

- EQ-81 (answered: an endless worktree): where the work happens — this task belongs to `endless`, but its
  worktree was cut in go-cfgstore.
- EQ-82 (answered): `project init` re-run; `doctor --fix` later calls the same steps.
- EQ-83 (answered): Endless-owned files always; user-owned only when clean before the run.
- EQ-84 (answered): the harness is not installed in other projects; `verify.toml` is the PRODUCT suite form.
- EQ-85: append the guide pointer to an existing CLAUDE.md/AGENTS.md, or only
  create a missing file.
- EQ-86: the drop verb's name.
- EQ-87: what the drop verb does with a row that has tasks, and what it points
  at instead of `project unregister`.
- EQ-88: who removes the two temporary lines in go-cfgstore's
  `.git/info/exclude`.

## Tests

- `tests/test_register.py`: gitignore entries; the guard that every
  uncommitted runtime path under `.endless/` that Go writes is in
  `GITIGNORE_ENTRIES`; the worktree refusal writing nothing; the guide pointer
  in each of its three cases; the commit rule (clean default branch commits,
  dirty or non-default reports); a project registered the old way brought up
  to date, then a second run changing no file and creating no commit.
- A worktree-land test for the runtime-path class.
- `internal/monitor` Go test for `DeleteProjectRow`, `internal/projectcmd` test
  for the verb, and a Python CLI test for `project forget`.
- The task's `verify.sh` runs these plus one end-to-end bring-up of a fixture
  repo registered the old way, run twice.
