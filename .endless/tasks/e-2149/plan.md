# Seed / framing

`endless project monitor --tmux` is how `project monitor` opens today, and
three things are wrong with it.

**The flag is unintuitive.** `project monitor` is a standing surface you open once and
leave. Reaching it requires remembering a flag on a subcommand, and the bare
verb does something different (renders in the current pane). Mike's sketch:
`endless monitor` doing what `project monitor --tmux` does now.

**It hijacks a live session.** Run with `--tmux` from inside an attached tmux
session, it `switch-client`s the caller to the monitor session — replacing the
user's windows with `project monitor`. Recoverable, but it takes over a workspace the
user did not offer.

**There is no way to open it somewhere new.** What `project monitor` actually wants is
its own terminal window, not a tmux session grafted onto the caller's client.
`open -a Terminal` was tried as a proof of concept and restarted Terminal.app,
dropping both tmux sessions — not viable.

# What to explore

- What the verb should be, and what it does when run bare versus with a target.
  Is `endless monitor` a new top-level verb, an alias, or a replacement?
- How a board gets its own window without commandeering the caller's. On macOS
  with Terminal.app an `osascript` can open a window and run a command in it.
  What is the equivalent for iTerm2, WezTerm, Ghostty, kitty, Alacritty, tmux
  alone, a bare TTY, and a remote session over SSH?
- Mike's proposal: a pluggable launcher. A config setting names the terminal,
  the setting selects a script, Endless ships scripts for the terminals people
  ask for, and a user can drop in their own. What is the script's contract —
  what is it given, what must it guarantee, what does Endless do when it fails
  or when no script matches?
- Whether the launcher belongs to `project monitor` at all, or is general: `task spawn`
  already builds a tmux window, and a second thing that opens terminals would
  be two mechanisms for one job.
- What happens with no window system: SSH, a CI shell, a detached daemon. The
  board must still be reachable, and refusing to open is a legitimate answer if
  it says what to run instead.
- Whether the tmux path stays at all once a real window launcher exists, or
  becomes one script among several.

# Constraints

Judge as a product, not as this machine (PRODUCT). The design cannot assume
macOS, cannot assume Terminal.app, cannot assume tmux, and must degrade sensibly
for someone on a fresh install in a single terminal. Say explicitly what it does
in `self_dev` mode versus for a project merely using Endless as a tool.

The current `--tmux` behaviour is a shipped bug, not merely a design smell: a
session-hijack is worth fixing regardless of where this lands.



# Already settled, and out of scope here

The display half of this brainstorm was run with Mike on 2026-09-15 and
resolved into E-2156. What `project monitor` shows, how it ranks, what a row
carries and what truncates are decided; do not reopen them here. This task is
now only about the verb that opens it and the window it opens into.
