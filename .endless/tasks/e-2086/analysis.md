Verified on 2026-08-30 while finishing E-2081.

The gap, concretely:

- The setup group in cli.py offers prompt-hook, claude-hook, shell-helpers and output-style. There is no tmux member, and setup.py has no tmux code at all.
- runInit in the tmuxcmd package says in its own doc comment that it is the target for a 'set-hook -g session-created' entry in the user's tmux conf. Nothing writes that line. Mike's server has it because his personal tmux conf directory carries it by hand.
- The Tmux integration section of the reference guide lists only apply, status-line and task, and states that apply is ephemeral and survives until tmux restarts. init is not documented anywhere a user would look.
- Net effect: the integration is invisible until a user discovers apply, and disappears again on the next server restart.

This was a deliberate deferral, not an oversight. E-1236's plan says the narrow scope — no permanent install, no companion file, no tmux-conf editing — was intentional, to 'ship the ephemeral path first, validate the UX, defer the install-mode design until users with hand-crafted tmux configs actually exist.' That condition has been met, so the deferral has expired.

Relationship to E-1850: that brainstorm maps how tmux configuration can ride along with a clone, scoped to the three-pane layout that session monitor needs. This task is the other half of the same problem — the status row, the menus and the session-created hook — and both are answered by whatever mechanism is chosen for getting Endless tmux config onto a user's machine. Not modelled as blocked_by, because E-1850 is explicitly a no-verdict option map and so cannot unblock anything on its own; but its output should land first and this task should adopt whatever shape it recommends rather than inventing a second one.

The design question this has to answer, and the reason it was deferred: users have hand-crafted tmux configs. Appending to a user's tmux conf is intrusive and hard to undo cleanly; a sourceable Endless-owned conf file the user includes with one line is less so; an idempotent 'endless setup tmux' that manages a marked block is the shape the existing setup verbs already use for the shell rc file and Claude settings, which argues for consistency.

Whatever is chosen must keep apply working standalone. E-2081 left a transitional cleanup in the tmuxcmd package that clears the stale focus-change hooks E-1682 installed, and it only runs from apply — which is currently the only verb a PRODUCT user ever invokes.