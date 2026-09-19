# What landed, and where it differs from the description

## The fix, both halves

**Product.** Every `new-window` Endless issues now names the session it lands
in, and the session it names is the one holding the pane that asked for it:

- `internal/spawnlaunchcmd/tmux_driver.go` — `newWindowArgs` takes a `target`
  as its first parameter, so the argv cannot be built without one. A new
  `spawnerSession()` reads `$TMUX_PANE`, resolves it with
  `display-message -p -t <pane> '#{session_id}'`, and returns `$N:`. Session id
  rather than name (a name can be renamed or duplicated); read from the pane
  rather than from `$TMUX`'s third field (which records the session a client
  was attached to at process start and goes stale when a pane is moved).
- `src/endless/session_cmd.py` — `_spawner_session_target()` does the same for
  `session goto --resume`'s `new-window`.
- An unresolvable target is refused on both sides, never a silent fall back to
  an untargeted `new-window`. Landing in an unknown session is the defect, so a
  spawn that cannot say where it belongs creates nothing.

**Tests.** `tests/conftest.py` points `TMUX_TMPDIR` at an empty per-test
directory. Deleting `$TMUX`/`$TMUX_PANE`, which conftest already did, only stops
code that BRANCHES on those vars; the tmux CLI reaches the running server
through its default socket regardless. With `$TMUX` gone, tmux derives its
socket from `TMUX_TMPDIR`, so every tmux subprocess in the suite now resolves to
a socket with no server behind it. `tests/test_tmux_isolation.py` pins it.

## Grown scope, folded in

The layout builder ran `display-message -p -t <window-name> '#{pane_id}'` three
lines after the `new-window` — the same unqualified-target bug one call later,
since two sessions can each hold a window named `E-1705` and tmux answers with
whichever it considers current. Rather than qualify the name, `new-window` now
carries `-P -F '#{pane_id}'` and reports the pane it just created;
`buildSpawnLayout` takes that id and `panePaneIDArgs` is gone. One fewer round
trip, and no name resolution left to get wrong.

## Correction to this task's description

The description names
`tests/test_task_claim_worktree.py::test_claim_binds_sibling_claude_session` as
the proof, and says the suite opens real tmux windows during `just test`. On the
current tree neither holds:

- That test makes **zero** tmux subprocess calls. Verified by running it with a
  logging `tmux` shim on PATH.
- Across the whole suite there is exactly **one** `new-window`, from
  `tests/test_task_reopen.py::test_spawn_force_still_demotes_but_warns`, and it
  reaches no server: that test sets `TMUX=fake`, and tmux takes `$TMUX`'s first
  comma-field as its socket path, so the call dies with
  `error connecting to fake`. Every other tmux call in the suite carries an
  equally unopenable placeholder from the resolver fixtures. Logged the socket
  each call would have used, over a full suite run, to establish this.

So the suite's protection was real but **accidental** — it rested on one test's
placeholder `$TMUX` value happening to be an unopenable path, not on any
isolation. The `TMUX_TMPDIR` change makes it structural, which is what the
analysis asked for.

The product defect is real and was reproduced directly: on a private tmux
server with sessions `spawner` and `operator`, with `operator` most recently
active, an untargeted `new-window` run from a pane in `spawner` created its
window in `operator`. `.endless/tasks/e-2125/verify.sh` reproduces that setup
and asserts the fixed binary lands in `spawner` instead.

## Rebase onto main (E-2106 + E-1957 landed underneath)

The first land attempt hit a semantic conflict; rebased in place and
re-derived the change on main's shape rather than replaying my side of the
hunk.

- **E-2106** extracted the pane layout into `internal/spawnlaunchcmd/layout.go`
  as `buildLayoutAround(anchor, cwd)`, shared by spawn, both resume paths and a
  shell `task claim`, leaving `buildSpawnLayout` as a two-line wrapper whose
  only job was the window-name → pane lookup. Since new-window now reports its
  own pane, that wrapper had nothing left to do: `runSpawnWindow` calls
  `buildLayoutAround` directly and `buildSpawnLayout` is gone, along with
  `panePaneIDArgs`. Its E-2106 unit test
  (`TestBuildSpawnLayout_ResolvesTheWindowsOnlyPane`) asserted the lookup this
  task removes, so it is replaced by a note in `layout_test.go` saying why it
  went and where the property is pinned now. `spawnerSession` goes through
  E-2106's `tmuxRunOut` seam rather than calling `runTmuxOut` directly.
- **E-2106** also added `--new-transcript` to `_resume_new_window_pane`, so the
  target resolution sits after that branch and applies to both commands it can
  build.
- `internal/projectstatuscmd/window.go` carried a doc reference to
  `spawnlaunchcmd.buildSpawnLayout`; it now names `buildLayoutAround`.
- `tests/test_transcript_gone.py` (new on main) has a tmux fake that needed the
  same `#{session_id}` answer the other two fakes got.

One false alarm worth recording: two `tests/test_land_conflict.py` tests failed
during the rebase, and against a snapshot of pristine main as well. Both were a
stale `bin/endless-go` predating E-1957 — `worktree ledger-orphans` did not
exist in that binary, so `ledger_orphans()` returned None and the classifier
fell through to `auto-file-only`. Rebuilding the binary makes both pass. Main
was never red; the two tests skip on a MISSING binary but not on a stale one.
