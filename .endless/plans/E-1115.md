# E-1115 — Fix `endless session use` sibling-pane lookup in same tmux window

## 2026-09-01 update — what this branch actually delivers (ED-1550)

The substantive fix for this bug **already landed via E-1395** on 2026-05-17
(commit `e21242288`, "Fix E-1395: scope `_tmux_window_pane_ids` to
`$TMUX_PANE`'s window"). E-1395 is recorded as `Duplicates: E-1115`.
`src/endless/session_cmd.py`'s `_tmux_window_pane_ids` has been passing
`-t "$TMUX_PANE"` since that commit; every caller of the shared helper
(`_current_endless_session_id`, `_find_sibling_claude_session`,
`session_cd_resolve`) inherited the fix.

What was still missing from main after E-1395: direct unit tests for the
helper. Existing coverage only monkeypatched `_tmux_window_pane_ids` at
its callers; nothing asserted that the subprocess call passes `-t` or
that a missing `TMUX_PANE` returns `None` rather than silently guessing.

Per **ED-1550** ("Agents must close more than they file; ... fold a
finding into an OPEN task as evidence"), those three regression tests
belong on this task, not on a new one. This branch's landed contribution
is therefore the tests in `tests/test_session_cd.py`:

- `test_tmux_window_pane_ids_scopes_by_calling_pane` — the subprocess
  call includes `["tmux", "list-panes", "-t", "$TMUX_PANE", ...]`.
- `test_tmux_window_pane_ids_returns_none_outside_tmux` — `TMUX` unset
  → `None`.
- `test_tmux_window_pane_ids_returns_none_when_pane_unset` — `TMUX` set
  but `TMUX_PANE` unset → `None` (refuse to guess).

No src/ changes on this branch; a duplicate copy of the fix was carried
on the pre-reset history and dropped when the branch was reset to main
before landing.

---

## Original 2026-05-02 plan (superseded by E-1395's fix)

# E-1115 — Fix `endless session use` sibling-pane lookup in same tmux window

You are starting work on E-1115. Begin with `endless task show E-1115` to confirm the task state.

## Spawning session (manual lineage — E-1128 will automate this)

You were spawned from a Claude Code session in tmux:

| field    | value                                  |
| -------- | -------------------------------------- |
| session  | `active`                               |
| window   | `*endless:user-hook[E-1126]` (current name; may be renamed) |
| pane     | `%74` (stable across renames)          |
| tty      | `/dev/ttys069`                         |
| task     | E-1074 (in `verify`)                   |

To return when you are done:

```bash
tmux select-window -t '%74'      # most reliable; survives window renames
# or
tmux select-window -t '*endless:user-hook[E-1126]'
```

If you spawn or relate further work, link it back to the spawning session by mentioning E-1074 in the task body so Mike can correlate.

## Repro

On 2026-05-02 from a tmux pane in the same tmux window as the Claude Code pane Mike was talking to:

```
$ eval "$(endless session use)"
No sibling Claude pane in this tmux window. Run `endless session list` to see all candidates project-wide.
```

A sibling Claude pane was demonstrably running in the same window. The lookup failed.

## What to investigate

1. The `endless session use` implementation — find the pane-discovery code path. Likely in `src/endless/session_cmd.py` or similar; grep for `sibling`, `tmux`, or the error string.
2. How does the lookup identify a "Claude pane"? Probably one of:
   - process tree scan (Claude Code's binary path)
   - tmux pane env (e.g. `CLAUDE_*` env vars in `tmux show-environment -t <pane>`)
   - tmux pane title or window option
3. Is the heuristic comparing the wrong tmux scope (window vs session vs all)?
4. Is the comparison case-sensitive when it shouldn't be, or string-vs-int mismatched?

## What to do

This is `needs_plan` but the task may be small enough to fix directly after diagnosis. Use your judgment:

- If the fix is a one- or two-line correction with obvious behavior change → fix it on a worktree, write tests, set status to `verify`.
- If the heuristic itself is flawed or needs design discussion → write a plan to `.endless/plans/E-1115.md`, attach via `endless task update E-1115 --text .endless/plans/E-1115.md` (flips status to `ready` per `feedback_plan_attach_promotes_to_ready`), and exit plan mode for Mike's review.

## Files to read first (Explore agent, in parallel)

1. `src/endless/session_cmd.py` (or equivalent) — `endless session use` implementation
2. `src/endless/cli.py` — Click registration for `session use`
3. Anything in `src/endless/` matching `tmux` or `pane` discovery
4. `internal/monitor/` for any Go side of session/pane tracking (sessions table is in `internal/monitor/db.go`)

## Worktree setup (manual, before any code change)

```bash
git -C /Users/mikeschinkel/Projects/endless worktree add -b task/1115-session-use-pane-lookup \
    /Users/mikeschinkel/Projects/endless/.endless/worktrees/e-1115 main
cd /Users/mikeschinkel/Projects/endless/.endless/worktrees/e-1115
just go-work-init
endless task start E-1115
endless task update E-1115 --status in_progress
```

Do NOT run `just install` from the worktree (memory `feedback_no_just_install_from_worktree`).

## Constraints

- Apply Mike's house rules (memory references): no Python SQLite reads in new code; ClearPath/doterr/go-dt for Go; no legacy shims.
- If you discover an out-of-scope concern, file it via `endless task add` (memory `feedback_flagging_means_filing`); don't bundle.
- One task per worktree (memory `feedback_one_task_per_worktree`).

## When done

- Both `just build` and `just test` pass cleanly on the worktree.
- Commit on `task/1115-session-use-pane-lookup`; do not merge to main.
- Set status to `verify` (Mike sets `confirmed` after review).
- Report what was found, what was fixed, and any deviations.
- Tell Mike how to switch back: `tmux select-window -t '%74'`.
