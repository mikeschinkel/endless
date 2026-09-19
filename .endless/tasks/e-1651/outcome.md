# Diagnosis: why the endless status row vanishes on /bg + claude attach

## Root cause

The row is **not** removed. `status` stays `2` and `status-format[1]` stays the endless line
through the whole flow (live capture: the after-attach step was identical to before except `$TMUX`
was **empty**; `@server_uuid` and the `$TMUX` server PID were unchanged — no tmux restart, no
config re-source). The failure is in **pane → session → task resolution**:

1. `/bg` moves the session to a **detached Claude daemon** (`claude daemon run --origin transient`)
   as a `--bg-pty-host` child. After `claude attach`, the session's hooks fire from that daemon with
   **no `$TMUX` / `$TMUX_PANE`** — there is no tmux pane anywhere in the process lineage. (The
   viewing pane belongs to the separate `claude attach` *client* process.)
2. The endless hook keys all pane identity off `os.Getenv("TMUX_PANE")`
   (`internal/hookcmd/claude.go:161` TouchSession; also :975/:1005/:1049/:1260). With it empty,
   `TouchSession`'s `NULLIF(?, '')` records the reattached session with **`process=NULL`** — no pane
   binding — and it has no task.
3. The status-line printer (`status-format[1]`) is invoked by tmux with the real focused pane id and
   resolves pane→session→active_task via `sessions.process` (`internal/monitor/tmux_lookup.go`
   `GetActiveTaskForPane`). The paneless daemon session is unreachable that way.
4. The row keeps rendering only while the **prior** in-pane session row still maps that pane→task.
   Once that stale row is invalidated (collision invalidation when a later event carries the pane,
   `ReapDeadTmuxPanes`, or it ends), the pane resolves to the paneless/taskless reattached row →
   placeholder/hint → the row "vanishes and does not re-render" (every later bg event still has an
   empty `$TMUX_PANE`).

Live evidence: parent in-pane session `81f9df34` (task E-1564, `process=%38`) vs. the reattached
daemon session (paneless, taskless). The printer still showed `[E-1564]…` only because the stale
parent row coincidentally still held `%38`.

**Both originally-considered fixes are invalid:** re-running `endless tmux apply` is a no-op
(`status`/`status-format[1]` are intact), and recovering the pane from PID/TTY ancestry is
structurally impossible (a `/bg` session has no tmux pane to recover).

## Verified facts about the Claude bg/daemon model

- `/bg` mints a **new session UUID** (`ec7cb9e1` → `24126b24`); reattach keeps the new id. No
  recorded old→new link in job dir, transcript metadata, or env.
- bg sessions **do** fire endless hooks and get DB rows (paneless, taskless).
- bg session env signals: `CLAUDE_JOB_DIR=~/.claude/jobs/<bg-short-id>` and
  `CLAUDE_ENABLE_STREAM_WATCHDOG=1`; transcript entries carry `CLAUDE_CODE_CHILD_SESSION=1` and a
  `sessionKind` field (the child knows it is a child, not who its parent is).
- The `claude attach <short-id>` client runs in a tmux pane (has `$TMUX_PANE`, id in argv) but does
  **not** fire an endless hook. Its parent is interactive `-zsh`, so Mike's `claude` shell function
  intercepts it (wrapper reach confirmed).

## Key discovery — the automatic parent→child bridge

`/bg` **copies the full parent conversation into the child transcript with message uuids preserved**,
then appends new ones (measured: all 13 parent uuids present in the child, + 11 new). endless already
persists these in `session_messages(session_id, message_uuid)` with `message_uuid` globally
`UNIQUE` — so a bg child's parent is identifiable by uuid lookup, with **no schema change**. This is
what makes automatic task/epic inheritance across `/bg` possible (see the bug task's fix plan).
