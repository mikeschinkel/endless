# Plan — E-2106

Design rationale is in this task's analysis. Spawn is NOT touched: it is for
never-before-worked tasks, and `_check_prior_claim` is the guard that keeps an
existing task from being restarted as if it were new. It gets no exemption here.

The two resume verbs divide by WINDOW, not by freshness:
`session resume` takes over the CURRENT tmux window; `session goto --resume`
opens a NEW one. Everything below preserves that split.

## 1. Locating a transcript

New helper in the session module. Given a Claude UUID, glob the per-project
directories under the Claude home for `<uuid>.jsonl` and return the first match,
or None.

Glob rather than derive the slug from the worktree path: a session that ran `/cd`
has its transcript filed under the slug of wherever it ENDED, not where it
started, so slug-derivation reports a false "missing". Observed live while
recovering E-1934.

## 2. A missing transcript is an ERROR, on both paths

Not a warning, and never warn-then-launch-anyway. A file that is merely missing
is often still recoverable — a backup, a snapshot, another machine — and that
window closes quietly. Stopping hands the recovery decision to the user instead
of spending it for them.

- `session resume <ref>`: check before `_require_claude`, before any pane
  identity is bound, and before the exec.
- `session goto <ref> --resume`: check in the CURRENT pane, BEFORE the new
  window is created. A message inside a window that immediately dies is the
  defect this task was filed for.

The error names the uuid, says where it looked, says it may be recoverable from
a backup, and names the give-up route.

## 3. `--new-transcript`

The flag for "I have given up on recovering that transcript." On BOTH verbs, so
the window choice stays independent of the give-up choice.

It launches a plain `claude` in the task's worktree — no `--resume`, and NOT
spawn's handoff, because delivering the handoff is what re-reads a task as if it
were new. The new session then claims the task; re-claiming is legitimate per
claim's own contract, and without it the work has no ownership record, which is
the exact gap that produced this task.

## 4. Window shape

`session resume` refuses unless the current tmux window holds exactly ONE pane.
Taking over a pane in a populated window destroys panes the user arranged.

Both verbs then build the same layout `task spawn` builds, by extracting the
layout builder in the spawn-launch package and calling it from the resume paths
rather than reimplementing it.

The layout that builder already produces is correct and is not being changed:
split horizontally off the claude pane into an EMPTY shell pane, split
vertically off THAT pane to run `session monitor`, then return focus to claude.
Settled with Mike — what is implemented for spawn is right, and the resume paths
adopt it unchanged.

## 5. `task show`

A session in the `Touched by:` block whose transcript is gone renders a marker,
so the loss is visible while reading the task rather than only when a resume
fails. Not `session list` — that would cost a stat per row.

## 6. Tests

Suite at this task's verify path, sourcing the shared harness; read the tasks
CLAUDE file first. Cases: transcript found by glob when the session ended under
a different directory slug; missing transcript errors on both verbs and the exit
is non-zero; `goto --resume` creates NO window when the transcript is missing;
`--new-transcript` launches without `--resume` and claims the task; resume
refuses a multi-pane window; and the layout builder is called once per verb.
