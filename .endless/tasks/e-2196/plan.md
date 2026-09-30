# Resume every restored tmux window's Claude session

## Command

`endless session resume --tmux-session NAME` (or `--tmux-session=NAME`), with
no REF, resumes every window in the named tmux session. `--all-tmux-sessions`
does every tmux session. There is no default session: this is a rare,
deliberate recovery command, and a required name keeps `--tmux-session` an
ordinary option that always takes a value (as on `session monitor --restart`).
REF together with either scope flag is a usage error, and so are both scope
flags together. `--dry-run` prints, per window, what it would do and does
nothing.

## Per window

1. **Task.** Parse the window name: a task id `E-NNNN`, optionally preceded
   by the user's attention marker (`*`, with surrounding whitespace, e.g.
   ` *E-2187`). Anything else: skip, and say so in the summary. The name is
   never changed.
2. **Already running Claude.** If any pane in the window is a live session's
   `pane_id` (the live-session query, not process-tree walking): skip.
3. **Kept pane.** Choose a pane at a shell prompt, preferring the one already
   in the task's worktree (keeps its scrollback), else the first. No pane at a
   shell prompt: skip, and say why in the summary.
4. **Clear the rest.** Kill every other pane in the window.
5. **Resume.** Type the resume command into the kept pane with `send-keys`
   (clearing any partial input first), so it runs exactly as if typed there:
   its output and errors print in that pane, and quitting Claude returns to
   the shell.

       endless session resume E-NNNN --rebind

   `--rebind` because tmux-resurrect does not restore `@endless_*` window
   options; resume rewrites them. Siblings were killed in step 4, so
   `--no-sibling-panes` is not needed.

**The window name is authoritative.** Resume changes into the task's
worktree itself (`os.chdir(worktree)`), so a pane restored in the main
checkout or in another task's worktree (seen after the 2026-09-29 crash:
panes in `e-2157` inside windows for other tasks) is not an error; the
directory is simply corrected.

**Dropped worktree** (the task landed and its worktree was reaped): add
`--review`, which rebuilds a detached, read-mostly tree without changing
status.

**Settled tasks** need nothing: plain `session resume` never changes a task's
status, which is the crash-recovery behavior wanted.

**Failures** in one window print in that window's pane, from resume itself,
and the run continues with the next window. The summary, printed where the command ran,
lists windows resumed, skipped (and why) and dispatched-but-unconfirmed.

## The single-window wrong-task bug

In some windows `session resume <task>` resolves a different task than the
one named, and `session goto <task> --resume` is needed instead. No pattern
is known yet. First step: reproduce it, against a window restored from a
crash, recording the window's `@endless_*` options, the pane's cwd and what
`--dry-run` resolves. Fix it in resume itself, so `--tmux-session` (which only types
`session resume` into panes) inherits the fix. Suspect first:
`_require_window_claim` and `_current_pane_task` reading stale `@endless_*`
options from a restored window.

## Where the code goes

The orchestration (enumerating windows, parsing names, the live-pane check,
choosing and clearing panes, `send-keys`) is Go: a new `endless-go`
subcommand. Python gets only the Click options on `session resume` and a
passthrough. The wrong-task fix goes wherever the bug is.

## Tests

- Unit: name parsing (bare id, ` *E-NNNN`, non-task names); pane choice
  (worktree pane preferred, shell-only rule); the per-window decision
  (skip/resume/review) as a pure function over window facts.
- Verify suite, against a throwaway tmux server (`tmux -L <socket>`) with a
  stub `endless` on PATH that records its argv: build windows shaped like a
  restore (3 panes; one in the worktree, others in the main checkout or
  another worktree; a single-pane main-checkout window; a non-task name; a
  window with a live session pane), run `--tmux-session <test-session>`, assert which panes
  were killed and what was typed where.
- The wrong-task fix gets a regression test reproducing the case found.

## Acceptance

- After a tmux crash and restore, one `endless session resume --tmux-session active` brings back every task window's Claude session and monitor
  layout, with no window touched by hand.

## As built (2026-09-30)

- **Wrong-task bug, reproduced and fixed at the root.** After a crash tmux
  reissues pane ids, and `list-live` kept reporting a pre-crash session's bare
  `%N` (its server is gone, so liveness `unknown`, still listed as an owner).
  Python matched that against `$TMUX_PANE`, so a restored pane "was working" the
  old session's task: `session resume` refused and pointed to `goto --resume`.
  On the live server 17 restored panes collided this way (e.g. window E-2135,
  `%45` → ES-1173 / E-2105). Fix: `monitor.ListLiveSessions` reports `pane_id`
  only for bindings on the tmux server this process reaches; the row stays
  listed. One Go change fixes every Python pane-id comparison. With it gone,
  there is no case where `resume` fails but `goto --resume` succeeds, so there
  is no goto fallback (Mike: use goto only if it can work where resume fails).
- **Typed line** (Mike's choice): `cd '<project root>' && endless session resume
  E-NNNN --rebind [--review]`. A pane restored inside a self-dev worktree would
  otherwise be refused for want of `--db`, and `--db main` is refused in
  non-self-dev projects.
- **Unresolvable task** (Mike): still dispatched, so resume's error shows in
  that window; the summary names the error too.
- The window running the command is skipped. `--tmux-session` /
  `--all-tmux-sessions` are refused under a sandbox DB context and with any
  single-REF flag. Focus never moves.
- Go: `internal/resumewindowscmd` (`endless-go resume-windows`). Python:
  options on `session resume`, passthrough `resume_tmux_windows`.
