Today "needs attention" is signalled only by red color and an asterisk on tmux
window tabs, which does not scale to ~70 concurrent sessions (all one project at
present). This display is the answer to managing that.

Unverified sorts first because unverified work is what accumulates unseen — 57
unverified tasks in `endless` (75 machine-wide, 71 of them 30-90 days old)
accumulated precisely because there was no way to keep it in view, worsened by
two tmux crashes that took it out of sight.

Stalled sessions: auto-spawn's cap (E-1814) deliberately counts a session paused
on a permission prompt against itself, on the reasoning that a prompt is a claim
on the user's attention and the cap measures claims on attention. That is only
safe if this display makes the stall loud. This is therefore load-bearing for
auto-spawn, not polish.

tmux topology: its own tmux session, two panes — the monitor, plus a bare shell
for running `endless` commands against what it shows. (The bare shell is
agentenv `unknown`; the CLI fails open and the hook no-ops there, which is the
desired behavior.) Auto-spawned session windows deliberately do NOT land here —
they land interleaved in the user's own tmux session (E-1814), because a
separate review-batch session is out of sight, out of mind, which is the exact
failure mode that produced the unverified sediment.

Auto-spawn on/off: this window fires the E-698 fire-once runner, so auto-spawn
(E-1812/E-1814) runs only while it is open. Closing it is a zero-code kill
switch. Deliberate for v1 — surviving a closed window is the point at which a
daemon is being chosen, and that should be a later explicit decision.

Out of scope, named in E-1815 and not filed: collapsible tree view, jumping to a
session in another terminal from this window, auto-verify/auto-land, stall
detection.
