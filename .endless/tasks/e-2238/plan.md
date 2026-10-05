# Plan — make the project monitor's shell pane act on the task-session tmux server

Agreed with Mike on 2026-10-04. Folds in what was first filed here as "route
spawns from the monitor server to the main tmux server".

## The setup

Two terminal windows side by side:

- one attached to the tmux server the task sessions run on, in the session
  where they live — `active` on Mike's machine;
- one attached to the monitor's own server (`tmux -L endless`, built by
  `endless project monitor --tmux`, E-2156), showing a window with two panes:
  the project monitor, and a shell prompt beneath it.

Commands typed at that shell prompt — mostly `task …` and
`session goto [--resume]` — must act on the task-session side BY DEFAULT.
Today they fail: a write refuses with "Cannot determine the Endless session for
this pane", and anything that opens a window would open it on the monitor's
server, in the monitor's session.

## Behaviour

1. **Recognising the pane.** Decided from where the pane runs — its tmux server
   is the monitor's server — never from an environment variable a process could
   export. Honoured only when no agent harness is detected. Never honoured in a
   pane on the task-session server, even in a window that has no Claude session
   at the moment.
2. **Writes** need no Claude session there, and record nothing in
   `session_tasks`. They are attributed to a new actor kind meaning "the user at
   the project monitor" (working name `project-monitor`, Mike's suggestion), not
   to `system`.
3. **Default target.** Anything that opens or finds a tmux window targets the
   task-session server, not the monitor's:
   - `task claim`, `task spawn`: Claude windows open in the target session.
   - auto-spawn fired by the project monitor lands there, even with target
     `monitor`.
4. **Target session** is a configured name, default `active`
   (`endless tmux start`, planned, will create a session of that name or
   whatever the user configures). Not "the most recently active client".
5. **`session goto ES-N` / `E-N`** from the pane makes that session's window the
   focused window in the terminal attached to the target session — e.g.
   `active:E-123` comes to the front in the other terminal window. `--resume`
   likewise.
6. **Views of "this pane's session"** (`session status`, `esu`, …) say the pane
   belongs to the project monitor and has no session, rather than guessing.

## Open questions

- **Naming.** Mike uses "monitor" for both the project and session monitors and
  did not agree to "console". A term for the monitor's server/session and its
  shell pane must be agreed (see E-2154) before code or docs carry one. Mike's
  idea to weigh: the separate server's session named `console`, each of its
  windows a different use — `projects` now, a planned window listing terms.
- **Which server is "the task-session server".** Today it is tmux's default
  server. If `endless tmux start` runs task sessions on a named server, this
  must follow that configuration rather than assume the default.
- **Classify every command**: which act on the target session (task-session
  side) and which on the monitor side, and what each does from the pane.
- If the focused window in the target session can be determined, whether
  "this pane's session" views should use it instead of item 6's refusal.
