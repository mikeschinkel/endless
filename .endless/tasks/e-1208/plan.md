# E-1208 — Record verbs.jsonl changes immediately on endless verb add

## Context

Today `endless verb add <name>` (and the auto-registration path from `task_cmd.validate_title`) writes a new entry into `<project>/.endless/verbs.jsonl` but does **not** commit. The file is left dirty in the working tree until either someone manually commits or the next `endless worktree land` bundles it into the auto-record commit.

Both outcomes are wrong:
- Manual-commit creates a coordination tax across sessions.
- Bundling at land couples a global-config event (registering a verb) to an unrelated worktree-land timing.

Per E-987's resolution, **write-time auto-commit** is the correct pattern for endless-managed files. Today's dirt was visible in this session: `.endless/verbs.jsonl` is modified on main because the in-flight `task add` calls auto-registered the verbs `narrow` and `ensure`.

The task description (and E-1199's global-config exception) authorize **direct commit to main, bypassing the task/worktree association**: the verb registry is global config, and the commit travels with main regardless of which working tree the calling session was in.

## What's actually being changed

Two behavior changes ride together:

1. **Route the project verbs.jsonl write to main's path.** Today `matchers.project_verbs_path()` walks up from cwd, so a worktree session writes to the worktree's `.endless/verbs.jsonl`. The replacement walks the registered-project record (the same path resolution `worktree_cmd._project_root()` already does) so writes always target main's tree, regardless of cwd.
2. **Commit the file immediately after a successful write.** Single-file `git add` + `git commit` on main's repo. Skipped if the write was a no-op (verb already present). Machine-layer writes are not touched — `~/.config/endless/verbs.jsonl` is not git-tracked.

Together these mean: a verb registration from any session lands as one commit on main with a message like `Endless: register verb 'foo'`, and the next session in any worktree picks it up via the read path (which also routes to main).

## Code surface

**Files to modify:**
- `src/endless/matchers.py:113` — `project_verbs_path()`. Change resolution from cwd-walk-up to registered-project-path lookup. Use the same pattern as `worktree_cmd._project_root()` (`_resolve_project(None)` → DB lookup → `path` column). Returns `None` when no registered project, preserving existing `None`-handling at callers.
- `src/endless/matchers.py:858` — `add_verb()`. After a successful project-side `_add_verb_to_file` (returns `True`), call a new private helper `_commit_project_verbs(verb_value)`. No commit when `wrote_project=False` (no-op) or when no registered project.
- `src/endless/matchers.py` (new helper) — `_commit_project_verbs(verb_value: str)`. Resolves main's registered path, then runs a single command: `git -C <main> commit -o .endless/verbs.jsonl -m "Endless: register verb '<verb_value>'"`. The `-o` (`--only`) flag tells git to stage **just** the working-tree content of that one path and commit, leaving the rest of the index untouched. Other dirt on main (staged or unstaged) is preserved as-is and does not block our commit. On subprocess failure, raises `RuntimeError` with the git stderr. **Imports** `_project_root` and `_git_run` from `endless.worktree_cmd` (no cycle: worktree_cmd doesn't import matchers).
- `src/endless/verb_cmd.py:15` — `add_verb`. Wrap the `matchers.add_verb` call to catch `RuntimeError` and re-raise as `click.ClickException` (preserves non-zero exit semantics per Mike's failure-handling answer).
- `src/endless/task_cmd.py:299` — auto-register path. **No catch added.** The existing `except ValueError: pass` catches validation errors only; a `RuntimeError` from a commit failure propagates naturally, surfacing as a `task add` failure (non-zero exit). Per Mike: he wants to see what kinds of errors actually occur before deciding which to soften.

**Tests to update or add:**
- `tests/test_verb_gate.py:63` — existing `test_verb_add_persists_in_verbs_file` uses `machine_only=True` and doesn't exercise the new path. Keep as-is.
- New test (`test_verb_add_commits_to_main`): with a temp git repo and registered project, call `verb_cmd.add_verb` for a new verb. Assert (a) `.endless/verbs.jsonl` has the entry, (b) `git status` is clean for that file, (c) HEAD's commit message matches `Endless: register verb '<v>'`.
- New test (`test_verb_add_no_commit_on_noop`): add the same verb twice. Assert HEAD advances exactly once.
- New test (`test_verb_add_machine_only_no_commit`): with machine_only=True in a git repo, assert HEAD does NOT advance.
- New test (`test_verb_add_commit_failure_raises`): simulate commit failure (e.g., make .git read-only after the file write, or stub `_git_run` to raise). Assert `ClickException` from `verb_cmd.add_verb` and that the file write persisted.
- New test (`test_auto_register_propagates_commit_failure`): from `task_cmd.validate_title`, with auto-register triggering and the commit failing, assert the `RuntimeError` (or the wrapped exception) propagates — `task add` itself fails. Verify the file write happened first.

## Failure handling

The file write happens first, then the `git commit -o` for that one path. Decisions:

| Failure                                              | Response                                               |
|------------------------------------------------------|--------------------------------------------------------|
| No registered project (project_verbs_path() → None)  | Machine-only write succeeds; no commit attempted; no error. |
| `git commit -o <path>` fails (any reason)            | `matchers.add_verb` raises `RuntimeError` with the git stderr included. Both call sites — the CLI (`verb_cmd.add_verb`) and the auto-register path (`task_cmd.validate_title`) — surface the failure as a non-zero exit. The file write persisted (verb is in `verbs.jsonl`), but the working tree is dirty and the caller is informed. Per Mike: fail loudly everywhere until we know what failure modes look like in practice. |
| Concurrent writer raced us between write and commit  | Acceptable: `commit -o <path>` only stages and commits that one path. Another writer concurrently adding a different verb does its own `commit -o`. Both commits land back-to-back (or git's ref-update lock serializes them). |
| Dirty main at commit time (user has other changes)   | Not a failure mode. `commit -o` does not consult or disturb other files' index/working-tree state. The user's in-flight work is preserved. |

No rollback on commit failure: the file has the new verb. The next CLI invocation may surface the dirt; the existing land-time fallback (`worktree_cmd`'s auto-commit step) covers the gap until E-1272 lands.

## No-registered-project case

If `project_verbs_path()` returns `None` (no registered project — typical at first install or in a non-project directory), the behavior is unchanged: machine-layer write only, no project file touched, no commit. The new commit step is conditional on the project write actually happening.

## Verification

1. **Happy path (main, clean)**: From a clean main checkout, `endless verb add ponder --definition "to deliberate over"`. Expect: `.endless/verbs.jsonl` has the new entry, working tree is clean for that file, `git log -1 --format=%s` shows `Endless: register verb 'ponder'`.
2. **Happy path (main, dirty)**: With unrelated dirt on main (e.g., a touch src/foo.py change), run the same command. Expect: same as #1 — verbs.jsonl is committed cleanly, the unrelated dirt remains exactly as it was (preserved by `commit -o`).
3. **Happy path (from worktree)**: From inside `.endless/worktrees/e-1208/`, `endless verb add musing --definition "..."`. Expect: main's verbs.jsonl (NOT the worktree's) has the new entry; main's HEAD has the new commit; the worktree's `.endless/verbs.jsonl` is unchanged on disk until the next rebase.
4. **No-op**: Same verb twice. Second call: no commit, no file change, exits with "already present" message.
5. **Machine-only**: `endless verb add foo --machine-only --definition "..."`. Expect: machine layer updated, no commit on main, project file unchanged.
6. **Auto-register propagates failure**: Stub out git so commit fails (e.g., make `.git` read-only). Run `endless task add "Mull over the design"`. Expect: task add fails non-zero with the git stderr surfaced; verbs.jsonl has the new entry persisted on disk; HEAD didn't advance.
7. **Tests**: `just test` — new tests cover scenarios 1–6 with monkeypatched temp git repos. Existing tests unaffected.

## Out of scope

- Removing the `_dedup_worktree_verbs_against_main` helper. Once E-1208 ships, the worktree should never have a divergent verbs.jsonl (writes go to main directly). The dedup helper becomes dead code, but removal lands with E-1272 (the bundled `land` change) where the entire auto-commit step is dropped.
- Reading from main when in a worktree (for non-verbs files). Out of scope; this is just verbs.
- File-write atomicity (lock-then-write). The existing read-modify-write pattern is unchanged; merge=union backstops textual races during rebase.
