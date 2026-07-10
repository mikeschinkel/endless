# Fix plan — restore the status row's task/epic for /bg'd + reattached Claude sessions

Root cause is diagnosed in the research child task's outcome. Summary: a `/bg`'d session runs under
the Claude daemon with a new session id, no `$TMUX_PANE`, and no recorded link to its pre-`/bg`
self, so endless (pane-keyed) can't map it to the viewing pane or carry its task/epic across — the
status row blanks and never recovers.

Fix in two independent halves.

## P1 — pane → bg session (needs a write-time signal; the bg hook has no pane)

Wrap `claude attach` in the `claude` shell function: when the subcommand is `attach` and
`$TMUX_PANE` is set, call a new tmux-agnostic verb before exec'ing the real client:

    endless session bind <SESSION_REF | --task <id>> [--process <val=$TMUX_PANE>]

- `<SESSION_REF>` is positional (one positional type), accepting an endless integer id or a Claude
  UUID prefix — reuse the existing resolver behind `session show`/`use`/`cd`. The id `claude attach`
  passes (e.g. `24126b24`) is an 8-char UUID prefix, so it resolves as-is.
- `--task <id>` is the alternative selector: resolve the live session via `sessions.active_task_id`
  (`state != 'ended'`, most-recent wins). Exactly one of `<SESSION_REF>` / `--task`.
- `--process` defaults to `$TMUX_PANE` when omitted, so the wrapper can call simply
  `endless session bind <uuid>`. The column is `process`; its value happens to be a tmux pane.

It upserts `sessions.process=<pane>` with TouchSession-style collision invalidation. The status-line
printer then resolves pane→bg-session; `TouchSession`'s COALESCE keeps later paneless bg hooks from
clobbering the binding.

Fallback for any path that bypasses the shell function (e.g. some `claude agents`-initiated
attaches): the printer reads the attach client's argv from the pane (`ps -o args=` on the pane pid)
to recover `<id>` at read time — costlier per refresh, but robust.

## P2 — bg session → task/epic (fully automatic, NO schema change)

On the bg child's SessionStart (it carries `CLAUDE_JOB_DIR` + `transcript_path`), if it has no
active_task_id: read the child's transcript head for its early message uuids (the parent's, copied
by `/bg`), then `SELECT session_id FROM session_messages WHERE message_uuid = ?` → the owning
session is the parent; copy its active_task_id/active_epic_id onto the bg session. Read uuids from
the transcript file, not the child's session_messages rows — the global-UNIQUE constraint means the
child's INSERT OR IGNORE skipped the shared uuids, so they remain owned by the parent's rows, which
is exactly what the lookup needs. Relies on the parent having been parsed (always true for a real
working session).

Together: the viewing pane shows the task/epic the bg session inherited from its pre-`/bg` self —
same as any foreground session, including the retained epic.

## Implementation scope

- Propagate pane identity via a single shared helper used at all five `os.Getenv("TMUX_PANE")` hook
  sites (`internal/hookcmd/claude.go:161/975/1005/1049/1260`), so status line, task auto-bind,
  worktree-lock identity, and sibling-UUID publish are fixed consistently. (No reusable helper
  today; `channelcmd.processID()` only falls back to `pid:N`.)
- New verb `endless session bind <SESSION_REF | --task <id>> [--process <val=$TMUX_PANE>]` (Go),
  reusing the `SESSION_REF` id|uuid resolver from `session show`/`use`/`cd`.
- Shell-function wrapper for `claude attach`.
- P2 parent-inheritance logic in the SessionStart hook path.

## Open items

- Whether `claude agents`-initiated attaches always run through the shell function (gates wrapper
  reach for that entry point; the argv-parse printer fallback covers bypasses).
- Separate idea (out of scope): a bespoke agent-management TUI, since `claude agents` can't filter
  by epic — possibly a pre-existing `later` task.
