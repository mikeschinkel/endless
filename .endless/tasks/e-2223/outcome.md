# Synthesis: cutting session monitor idle CPU to ~0%

## The reframe

The seed framed this as a shared hub: one process computes state, and panes are thin clients. The brainstorm landed somewhere simpler. **A monitor nobody can see should do nothing.** Mike can see one session's monitor at a time (plus an optional `project monitor` in a second terminal). If hidden monitors sleep, 30 or 100 panes cost about the same as one, with no new process to start, discover or recover. That's what the hub was buying.

Two observations shaped the plan:

- **The idle baseline is wrong on its own terms.** 100–250 wake-ups per second per pane is hundreds of times what a 2-second ticker should cause, so something in each process spins regardless of git or database work. Visibility gating doesn't fix that. A hidden pane is only free if its idle loop truly sleeps, so profiling comes first.
- **`fseventsd` load probably comes partly from `git status` rewriting each worktree's index.** `--no-optional-locks` stops those writes. Mike is wary of depending on `fseventsd` at all (Time Machine leans on it heavily), which also argues against file watchers for change detection.

## Decisions

**ED-1606: gate monitor work on tmux visibility instead of sharing work through a hub.**
- Visible means the active window of an attached client. macOS app focus is irrelevant, and several visible monitors are fine.
- A hidden monitor blocks. On becoming visible, it recomputes once, then stays fresh using cheap change detection: SQLite `PRAGMA data_version` for the database, and index/HEAD modification time plus `git status --no-optional-locks` for each worktree.
- Visibility signal: tmux hooks as the primary signal, plus a slow per-pane poll (15–30s) as a safety net that also checks the hooks are still installed.
- Errors: a failing hook, or hooks found missing, goes to `endless errors list`, never silent. A single missed hook is only logged; it corrects itself within one poll interval.
- Visible monitors fire the job runner. That describes today's trigger and **is not a prohibition**: another job trigger may be added later without fighting this decision.
- Rejected:
  - shared hub (deferred, not rejected; see below);
  - elected snapshot leader (handoff, and followers still wake);
  - cheaper-but-still-live panes (the ceiling moves but doesn't go away);
  - tmux control mode (an attached client corrupts `session_attached`, fires client hooks, can affect sizing, and streams every pane's output; Mike ruled it out);
  - polling leader (needs a notify channel, which makes it a small hub).

**ED-1607: run Endless on its own tmux server, layered over the user's tmux config.**
- `tmux -L endless`, started by `endless start` (alias `endless tmux start`), with a config that sources the user's tmux config and then adds Endless's layer, including the hooks.
- The user's other tmux sessions are not Endless-managed. That's a benefit, not a cost.
- Hooks defined in the config file come back on every server start, which removes the "a restart drops runtime-installed hooks" failure mode.
- Inside a pane, `$TMUX` already targets the right server. Callers outside a pane (plain terminal, background process, web server) must pass the socket, which the multiplexer driver handles in one place.
- Rejected: runtime hooks in the user's default server, which can clobber or be clobbered by their config.

## Jobs

When no monitor is visible, jobs don't run. Mike considers that acceptable and preferable: jobs automate task review and session work, not general automation. `task spawn` always starts a `session monitor`, so `session monitor` isn't optional in practice. `project monitor` is optional, for when many tasks are in flight. Triggering jobs from other entry points was discussed and **not filed** (it isn't needed now); it's recorded as a note on E-1848.

## The hub

Not in the backlog. Build it only if, after E-2228 and E-2229 land, idle panes still fail the acceptance test. E-1848 (background job daemon) is its home, and carries a note saying so. Mike expects it won't be needed.

## Acceptance test

- Measured: under 0.1% CPU per idle pane, and no child processes spawned on an idle tick.
- Mike's manual check: 30+ windows open with the machine above 90% idle.

## Follow-ups

Epic **E-2227**, "Cut session monitor idle CPU to about zero", with two children:
- **E-2228**, "Make an idle session monitor actually idle": the stopgaps (`--no-optional-locks`, skip the tmux resize when the height hasn't changed), the profile, and fixing the wake-up source.
- **E-2229**, "Gate monitor work on tmux window visibility": for both monitors. Blocked by E-2228, E-1850 and E-2086.

Folded into existing tasks (ED-1550: no new filings where an owner exists):
- **E-1850** (moved to now): `~/.config/endless/tmux.conf`, the layered Endless tmux server, `endless start`, and curating Mike's tmux.conf into Endless vs. personal. This is the clone-and-run path for a PRODUCT user.
- **E-2086** (moved to now): `endless setup` installs that config; `endless tmux install` is an alias.
- **E-1809**: socket awareness in the multiplexer driver (`$TMUX` when set, otherwise `-L endless`).
- **E-1848**: the deferred-hub note and the other-job-triggers alternative.

Spun off:
- **E-2230** (brainstorm, phase next), "Explore how findings-lane tasks signal they are done": the monitor treats worktree settledness as "done", which doesn't fit research and brainstorm tasks. Do those tasks need worktrees and sandboxes at all? How should the monitor surface `unreviewed`? Is a spike a task type or a flag on research?
