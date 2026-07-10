# Replace /tmp with project-local .endless/tmp for scratch content — PLAN

## Context

Guides, docs, MEMORY, and spawn-prompt templates tell agents to author content at
a `/tmp/...` path, then load it (`endless task update <id> --text-file <path>`).
`/tmp` is system-global and ephemeral: if content is written there and only
*referenced* from the endless DB (the corruption class E-1744 addresses), or the
worktree is dropped before the content is loaded, it is lost to the ether — and
`/tmp` is wiped on reboot, off in a system dir no one thinks to check.

Fix: sanction a **project-local** scratch dir `.endless/tmp` and recommend it
everywhere `/tmp` is currently recommended. Co-located with the work, survives
reboot, and easy to find if content needs recovering before a worktree is dropped.

This is the **suspenders** (belt-and-suspenders with the multiline-mirror task):
mirrors preserve content that reached the DB; `.endless/tmp` keeps a
forgotten-but-not-yet-loaded scratch file recoverable instead of vaporized. Mike
wants both layers so a single forgotten step never loses important content.

## Changes

### 1. Gitignore — scaffold in `endless register`, not just this repo
`endless register` (`src/endless/register.py::register_project`; CLI wrapper
`cli.py::register`) is the project-init command, but today it writes only
`.endless/config.json` — it does NOT touch `.gitignore`. The existing `.endless/*`
ignore lines in this repo were added by hand, and no code writes `.gitignore`
(`purge`'s "ignore list" is endless's *discovery* ignore, i.e. `config.add_ignore`,
not git). So:
- **Add gitignore-scaffolding to `register_project`**: idempotently ensure the
  project's `.gitignore` contains the endless ignore set (append only missing lines
  under a clearly-marked `# endless` block; create `.gitignore` if absent). This
  makes every newly-registered project get `.endless/tmp` automatically.
  - **Definitely include**: `.endless/worktrees/` and the new `.endless/tmp/`.
  - **Uncertain — do NOT bake on a hunch**: `.endless/worktree.json`,
    `.endless/worktree.lock`, `.endless/sessions/`. Mike suspects these are
    obsolete, but the code still references them (`worktree_lock.go` writes
    `worktree.lock`; `worktree.json` companion still written/read; `session_cmd.py`
    reads `.endless/sessions/`). Their currency is a confirm-and-prune task under
    the `project` command epic — settle it before freezing the canonical set here.
    For now, scaffold the two definite entries; leave the existing three as-is in
    already-set-up repos.
- **Backfill this repo now**: add `.endless/tmp/` to the endless project's own
  `.gitignore` by hand (register won't retroactively touch already-registered
  repos; re-running `register` on an existing project should also be safe/idempotent
  and could be the backfill path for the ~40 registered projects).

### 2. Docs / guides / MEMORY / prompt sweep
`grep` for `/tmp` recommendations and rewrite to `.endless/tmp`:
- `endless guide` source + `docs/guide*` markdown.
- Spawn-prompt templates (the handoff prompts that say "Author the plan at a temp
  path").
- Any `CLAUDE.md` / project-rules snippets.
- The E-1202 hook block message and the E-1744 gate/lint messages say "author at a
  temp path" — point them at `.endless/tmp`.
- User MEMORY entries that recommend `/tmp` (memory is reinforcement; the shipped
  guides/prompts are the real fix).

Be surgical: only rewrite `/tmp` uses that are **scratch-for-endless-content**
recommendations. Leave unrelated `/tmp` references (e.g. genuine OS-temp examples,
test fixtures that must use the system temp) alone — enumerate each hit and judge.

### 3. Ensure the dir exists when recommended
`.endless/tmp` must exist (or be created on demand) so the recommendation isn't a
"no such directory" trap. Options (pick at implementation): create it in the
worktree bootstrap (`post-worktree-create.sh` / `create_task_worktree`), or have
the guide instruct `mkdir -p .endless/tmp`. Prefer bootstrap creation so agents
never hit a missing dir.

## Deliverable — Generate the verify script (do NOT hand-verify)

The implementing session GENERATES `tests/tasks/e-1748-verify.sh` (self-contained,
shape/output per `tests/tasks/e-1577-verify.sh`) and commits it with the
implementation. It must assert:
- `.endless/tmp/` is gitignored (`git check-ignore .endless/tmp/x` → match) and a
  file written there does not appear in `git status`.
- `endless register` on a throwaway temp git repo writes the canonical endless
  ignore set — including `.endless/tmp/` — into that repo's `.gitignore`; re-running
  `register` is idempotent (no duplicate lines).
- No `/tmp` scratch-for-content recommendation remains in `docs/`, the guide/help
  source, or prompt templates (grep clean; intentional OS-temp uses, each
  justified, may be allow-listed).
- A fresh worktree has `.endless/tmp` present (if bootstrap-created).

## Verification (handoff to Mike)

On completion, set `unverified` and tell Mike to run exactly:

```
esu && ./tests/tasks/e-1748-verify.sh
```

## Notes
- Complements E-1744 (flag gate + repair) and the multiline-mirror task
  (durability). E-1744's absolute-path gate (Rule 2) already blocks `/tmp` (and any
  absolute path) inside flag content; this task owns the convention change + docs +
  gitignore so agents author scratch in `.endless/tmp` instead of `/tmp`.
