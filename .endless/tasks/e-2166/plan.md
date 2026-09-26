# E-2166 — as built

Two pieces were scoped. Both landed, one of them shaped differently from the
description, and the pin's supporting machinery came with them.

## 1. The generator writes NO hooks block, rather than the installed path

The description said `claude-settings-init` should "name the installed
endless-go"; it also said the durable form is that "a worktree's settings should
name no binary path at all". Mike chose the second reading, and it is the one
that holds up:

- `endless setup claude-hook` writes the user-scope settings file
  (`src/endless/setup.py`, `CLAUDE_SETTINGS_PATH`) and carries **no self_dev
  gate**. That makes the user scope the PRODUCT hook location for every user and
  every tracked project today, and Claude Code applies it in every directory — a
  worktree included. A worktree therefore needs no hooks key to be hooked; the
  main checkout proves it, having none in either project-level file.
- The block the recipe used to write was a copy of those same entries with the
  binary path swapped. Writing the installed path instead would leave 120 frozen
  snapshots that contradict every later `setup claude-hook` repair — it adds
  missing hook events and corrects sync/async flags in place.

So the recipe emits `worktree.bgIsolation` and nothing else, and **strips** a
`hooks` key an earlier run left behind. That strip is the sweep's entire
mechanism: the sweep re-runs the recipe, so without it the 96 pinned worktrees
would stay pinned.

The recipe's dependency also inverted. It no longer reads the user settings to
copy hooks, so it now ASSERTS them instead: a user-scope file with no endless-go
hook is refused, naming `endless setup claude-hook`. The old check was
file-exists, which passed for a settings.json that defined no endless hook — and
that now means an unhooked worktree rather than a repointed one.

## 2. The sweep — `just claude-settings-sweep`

No sweep-specific logic: each worktree gets the very recipe
`.endless/hooks/post-worktree-create.sh` runs at birth, invoked the same way
(main's justfile, the worktree as working directory), so a swept worktree and a
new one are identical by construction rather than by two implementations
agreeing.

- **Fail-fast**, per Mike: the first worktree it cannot rewrite stops the sweep,
  names it, replays that run's output, exits non-zero, and leaves every later
  worktree untouched. A sweep that logged and carried on would leave an unknown
  number pinned to a stale binary while reporting success — the same silent
  degrade this task exists to fix.
- Worktrees come from `git worktree list`, not a directory glob, so an abandoned
  directory under `.endless/worktrees/` is neither swept nor a failure.
- Refuses to run from a worktree.

**It is a POST-land step, and cannot be otherwise.** It invokes MAIN's justfile
per worktree, so run before this lands it would call main's still-pinning
recipe. The verify suite asserts that property rather than leaving the ordering
a convention.

## Grown scope — the machinery that existed only to serve the pin

Deleted, with its two test files (`internal/hookcmd/claude_skip_test.go`,
`internal/monitor/foreign_hook_build_test.go`):

| Symbol | Why it had to go |
|---|---|
| `warnForeignHookBuild`, `monitor.ForeignHookBuild`, `monitor.WorktreeHookBinary` | Forced. The warning fires whenever a hook runs in a self_dev worktree from any binary but that worktree's own — after this change, every hook event in all 120 worktrees. It is an alarm for the state ED-1596 declares correct. |
| `shouldSkipForWorktree`, `shouldSkipForWorktreeAt`, `worktreeOverrideRegistered` | Mike's call, over keeping them as a backstop. This is the self-skip that made the pin an override rather than a double fire; with no pin anywhere it never fires, and the fail-fast sweep removes the "a worktree it missed" case that was the argument for keeping it. |

`hookcmd`'s shared worktree fixtures (`writeTestFile`, `makeWorktreeLayout`,
used by three other suites) moved out of the deleted file into
`internal/hookcmd/worktree_layout_test.go`.

## Also folded in — things the change made untrue

- `internal/faults/codes.go` ERR-0015 and `docs/errors.md` both named a stale
  worktree binary as the likely cause of a hook write failure, with E-2166 cited
  as the diagnosis. That cause is now closed for hooks, so both were rewritten to
  the real remedy (`just install` from main). `TestCatalog_RemediesMatchTheDocs`
  requires the two to agree, so this was not optional once either moved.
- Eight rows in `docs/research-2026-09-17-refusal-inventory.tsv` were anchored to
  the deleted symbols. Marked `RETIRED:` per E-2155's vocabulary;
  `tests/test_refusal_inventory_anchors.py` fails otherwise.
- Stale comments corrected: `post-worktree-create.sh` step 4, the `install`
  recipe's claim that a worktree-scoped install scopes hooks, and the
  `.gitignore` note.

## Durable coverage

Per `.endless/tasks/CLAUDE.md`, the behaviour lives in the project suite, not
only in the verify gate:

- `tests/test_claude_settings_init.py` — rewritten. Its argv contract shrank from
  five arguments to two, and it gained the pin-removal, announcement and
  hand-written-key cases. (Its docstring had also been stale since E-1457.)
- `tests/test_claude_settings_sweep.py` — new, end to end: builds a real git repo
  with three pinned worktrees and a fake `$HOME`, then asserts the sweep unpins
  all of them, reports its counts, stops on the first failure, refuses from a
  worktree, and ignores a stray directory.

## Known transition window

Between the land and the sweep, and for any worktree session that was already
live when the sweep ran, hooks fire TWICE: the session's cached settings still
name the worktree binary, while the rebuilt installed binary no longer
self-skips. Every affected handler is idempotent (activity rows are throttled,
the lock release and session end are both re-runnable), so the cost is noise.
Restart live worktree sessions after the sweep to close it.
