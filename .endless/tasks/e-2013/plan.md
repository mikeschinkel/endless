# E-2013 plan: run `land`'s post-merge phase in a fresh process

## Context

`endless worktree land` is a Python process running from the main checkout's
editable install. Step 5 ff-merges main, which **replaces that process's own
source on disk**. Modules already in `sys.modules` keep the pre-merge code;
modules imported for the first time afterwards are read from the post-merge
source. A branch that renames a symbol therefore breaks any post-merge lazy
import.

Observed on E-2011, which renamed `endless.project_path.normalize` to
`resolved`:

```
Landed E-2011 (task/2011-…) into main: main was advanced, but recording the
landing failed:

cannot import name 'resolved' from 'endless.project_path'
```

`_record_landing` (`worktree_cmd.py:2130`) does `from endless.event_bridge
import emit_event` inside the function. Post-merge, `event_bridge` had not been
imported yet, so it loaded from the NEW source — whose `emit_event` references
`resolved` — while `endless.project_path` was already cached from the OLD
source. Main advanced, no `task_landings` row, branch tip unmerged.

**Not specific to that rename.** Every post-merge step is exposed: Step 5.5
`_apply_branch_schema_changes`, Step 6 `_record_landing`,
`_run_post_land_script`, `_check_post_land_residue`, `_reap_stale_worktrees`.
Which ones happen to import something new is an accident of what the process
touched before the merge, so the failure is order-dependent and will not
reproduce reliably.

**Scope.** This can only happen when the running `endless` package lives inside
the repository that just advanced — i.e. endless landing endless. A downstream
project's land never replaces endless's own code and is untouched by this plan.

## The decision: cut after the ff-merge, hand the rest to a fresh process

A fresh process loads the post-merge source throughout, which is exactly why
re-running `just land` already recovers. Make that the normal path instead of
the remedy.

**Cut point: immediately after Step 5's successful ff-merge, before Step 5.5.**
Everything from 5.5 to the end of the function runs in the child.

### Why not narrower (record the landing in a subprocess and keep the rest)

`_record_landing` is where it bit, but `_check_post_land_residue` and
`_reap_stale_worktrees` are equally exposed and equally in-process. Fixing the
one observed symptom leaves the class open and makes the next occurrence look
like a new bug.

### Why not re-exec the whole command from the top

Superficially attractive — the land is already documented as re-runnable — but
it silently weakens two checks, because both depend on state that only exists
BEFORE the merge and whose own code comments say so:

- Step 4.5 `schema_changes` (`worktree_cmd.py:2374`): "After Step 5 they are the
  same commit and the three-dot diff is empty, so computing it later would
  silently skip every change."
- `ignored_before` (`worktree_cmd.py:2250`): the pre-land ignored snapshot. Taken
  after the merge, `_check_post_land_residue` compares against nothing and
  passes vacuously.

So the handoff must carry pre-merge state explicitly rather than recompute it.

### Rejected alternatives

- **`importlib.reload` / `invalidate_caches`.** Reloading a package graph in
  place leaves every already-bound reference pointing at the old module objects.
  Unreliable in a way that fails silently, which is the failure mode being
  removed.
- **Pre-import everything before the merge.** The set of modules the tail will
  touch is not enumerable, and any new lazy import silently re-opens the hole.
- **Gate on the `self_dev` config flag.** `self_dev` is a proxy. The condition
  that actually matters is "the running `endless` package is inside the repo
  that just advanced", which is directly testable and costs two lines.

## Implementation

### 1. Extract the tail — `_post_merge_tail(...)`

Move Steps 5.5 → end of `land_worktree` into one function taking exactly what
those steps use today:

`main_root`, `worktree_path`, `branch`, `base_branch`, `canonical`, `item_id`,
`proj_name`, `merge_sha`, `endless_go_bin`, `schema_changes`, `ignored_before`.

Both the in-process path and the resumed child call it, so there is exactly one
implementation of the tail and the two paths cannot drift.

### 2. The handoff test — `_land_source_is_self_replacing(main_root) -> bool`

```python
pkg = Path(endless.__file__).resolve().parent
return pkg.is_relative_to(resolved(main_root))
```

`resolved()` is E-2011's accessor — `main_root` comes from `projects.path` and
is now stored `~/…`. True for endless landing endless; False for every
downstream project, which keeps its land byte-identical to today.

### 3. The handoff payload

A JSON temp file (not argv — `ignored_before` is an unbounded set of paths)
holding the eleven fields above. Written under the system temp dir, deleted by
the child on success and left in place on failure so a post-mortem can read it.

### 4. `--resume-post-merge <path>` on `worktree land`

Hidden option (`hidden=True`) in `cli.py` beside `--record-only`; `land_worktree`
dispatches to `_post_merge_tail` from the file and returns, before any git work —
the same shape as the existing `record_only` early return at
`worktree_cmd.py:2210`.

`--record-only` (E-1719) is deliberately NOT reused: it records a landing whose
worktree is gone and covers Step 6 only, which is the narrow fix rejected above.

### 5. Launching the child

Add `src/endless/__main__.py` (three lines, calling `cli.main`) so the child is
`[sys.executable, "-m", "endless", "worktree", "land", <id>,
"--resume-post-merge", <path>]`. Going through `sys.executable` rather than
`sys.argv[0]` makes the launch independent of how endless was installed (uv
tool, pipx, editable venv).

`subprocess.run` with inherited stdio, then exit with the child's status.
Preferred over `os.execv` for one reason: a failure to *start* the child is
then distinguishable, and that case must still tell the user main was advanced.

### 6. Error wording is already correct

Everything the child runs is by definition post-merge, so `_record_landing`'s
existing "main was advanced, but recording the landing failed … the ff-merge is
idempotent, re-run" message stays accurate and needs no change.

## Tests

- `_land_source_is_self_replacing` — true when the package is under `main_root`,
  false otherwise, and correct when `main_root` is stored `~/…` (the E-2011 tie-in).
- The mechanism itself, without git: import a module from a temp package, rewrite
  its source on disk, import a second module referencing the new name, and assert
  the `ImportError`; then assert a subprocess of the same work succeeds. This
  pins WHY the fix is needed, which no plumbing test can.
- Dispatch: with the handoff active, `land_worktree` runs no post-merge step
  in-process and invokes the child once with the expected payload; with it
  inactive, it calls `_post_merge_tail` directly and spawns nothing.
- `_post_merge_tail` round-trips its payload through JSON unchanged (the set →
  list → set of `ignored_before` is the part that can quietly rot).

## Verification — `tests/tasks/e-2013-verify.sh`

Fail-fast on the unit suites, then end to end in a throwaway repo + temp
`XDG_CONFIG_HOME`:

1. A land in a fixture project (endless NOT inside it) spawns no child and still
   records the landing — the downstream path is unchanged.
2. `endless worktree land <id> --resume-post-merge <payload>` against a fixture
   whose merge already happened writes the `task_landings` row — the child alone
   completes the tail.
3. The residue check still fires from a resumed tail: seed a file that was
   ignored pre-land and is untracked after, and assert the failure — proving
   `ignored_before` survived the handoff rather than arriving empty.

## Not fixed here, deliberately

If the child fails and the user re-runs `just land`, Steps 1–5 are no-ops and
`schema_changes` / `ignored_before` are recomputed post-merge — empty and
vacuous respectively. That is today's behavior on any re-run after a partial
land and is unchanged by this plan; making a re-run resume with the original
pre-merge snapshot is a separate question about persisting land state.
