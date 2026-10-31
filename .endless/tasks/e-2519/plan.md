# Refuse to resume a Claude session that is already running

Builds on E-2358, which makes session lookups by id search every project
(`list-live --all-projects`).

## Change

### One check before every resume launch

A function in `session_cmd` (e.g. `_refuse_if_running`) runs immediately
before each `claude --resume <uuid>` launch: `session resume` (exec in the
current pane), `session goto --resume` (new window, `_resume_new_window_pane`)
and `goto --resume --background` (`_resume_in_background`). It refuses when
either holds:

1. The target's Claude session UUID belongs to a session that reads `live`
   among live sessions in every project.
2. A running `claude` process has `--resume <the target's UUID>` in its
   arguments (a process listing). This catches a resume started by hand,
   which liveness does not track.

`--new-transcript` opens a new transcript, so it is not checked.

### What each command says

- `session resume` and `goto --resume --background` refuse: the session is
  already running; run `endless session goto <ref>`.
- `session goto --resume` on a running target fails rather than quietly
  focusing it: the session is already running; drop `--resume`, i.e. run
  `endless session goto <ref>`.
- A target that reads `unknown` or `unbound` stays resumable unless the
  process check finds it.

### Sessions on servers that predate socket recording read dead

`session_liveness` reads `unknown` for a session whose tmux server was not
reached and is not proven gone. Endless now records a server's socket on the
first hook on it (`recordTmuxServer`), but servers that died before that
recording existed have none, so their sessions read `unknown` forever.
Liveness already scans every directory tmux puts sockets in
(`observeTmuxServers`: the default socket directory and Endless's). Add the
rule: a session whose server has no row in `tmux_servers` and was not seen
in that scan reads `dead`. No session rows are written; every machine's old
rows read correctly.

## Verify

- Resuming a session live in another project refuses and names
  `endless session goto <ref>`.
- `goto --resume` on a live session fails and says to drop `--resume`.
- With a `claude --resume <uuid>` process running outside any pane Endless
  knows, resuming that uuid refuses.
- Sessions on unrecorded, unobserved servers read `dead`; `list-live` no
  longer lists them.
