# E-1470 implementation plan

## Root cause (confirmed in code + transcript)
Internal headless `claude -p` calls inherit the caller's `TMUX_PANE`. Each gets a fresh Claude
session UUID and fires `endless-hook` → `TouchSession(new_uuid, inherited_pane)`. The collision
`UPDATE` (`internal/monitor/session.go:271-283`) then sets `state='ended'` on every OTHER session
sharing that pane — i.e. the live CALLER. Repro: `endless task add` titled "Adopt…" spawned the
'adopt' verb-check (`_check_verb_via_haiku`, `src/endless/task_cmd.py:257`); its headless session
(`a27e5710`, transcript shows the "Is 'adopt' a verb?" prompt) ended live session 510. While
`ended`, the caller is invisible to the session resolver (filters `state != 'ended'`), breaking
`endless session id` / `endless task add` until the next Stop hook revives it.

## Fix
1. New env marker `ENDLESS_NO_HOOKS=true`. `endless-hook` (`cmd/endless-hook`) checks it first
   thing and exits 0 immediately — no `TouchSession`, no collision, no `SessionEnd` side effect.
   Hook-specific name (not a generic "internal" flag) so hook-suppression stays separable from
   any future internal-call concerns.
2. Route ALL internal `claude -p` calls through one helper (e.g. `_run_internal_claude(prompt,
   model)`) that runs:
       claude --model <m> -p <prompt> --effort low --tools "" \
              --mcp-config "" --strict-mcp-config --no-session-persistence
   with `env={**os.environ, "ENDLESS_NO_HOOKS": "true"}` (full env + marker — never a bare dict,
   which would strip PATH/auth and break `claude`).
   - `--tools ""` also removes PreToolUse/PostToolUse hook surface.
   - `--no-session-persistence` avoids littering `~/.claude/projects` with throwaway transcripts.
   - `--mcp-config "" --strict-mcp-config` spawns no MCP servers (Mike verified the empty-string
     parse is accepted).
   - `--effort low` keeps the trivial call cheap.
3. Convert both current internal call sites to the helper:
   - verb-check: `src/endless/task_cmd.py:257` (`_check_verb_via_haiku`).
   - recap: `src/endless/session_cmd.py:~728` (`claude -p`). Recap has the same latent bug —
     `session_cmd.py:252` only HIDES recap sessions from `session list`; once they stop
     registering at all, that band-aid filter can be retired.

## Considered and rejected
- `claude --bare`: skips hooks, but forces Anthropic auth to `ANTHROPIC_API_KEY`/apiKeyHelper
  only and never reads OAuth/keychain. This environment has no API key (OAuth login; verified
  `ANTHROPIC_API_KEY` unset), so `--bare` would fail to authenticate and silently break verb
  auto-registration. `ENDLESS_NO_HOOKS` preserves OAuth auth.
- `--session-id`: not a safety net (rejection accepted by Mike). If hooks fire (i.e.
  `ENDLESS_NO_HOOKS` failed), passing the caller's UUID means the headless call's `SessionEnd`
  hook → `EndSession(caller_uuid)` still ends the caller, just via a different path; a
  fresh/throwaway UUID just reproduces the collision. Only `ENDLESS_NO_HOOKS` closes both the
  collision and `SessionEnd` paths.

## Defense in depth (optional, can be its own task)
- Harden the collision rule (`session.go:271-283`) to confirm the prior pane occupant is actually
  stale before invalidating, rather than ending on a bare pane-string match.

## Related (split candidate)
- The CLI session resolver (`src/endless/task_cmd.py:1976-2017`) ignores `CLAUDE_CODE_SESSION_ID`
  entirely; add a UUID-first authoritative lookup (`sessions.session_id` is UNIQUE), and fix that
  `ENDLESS_SESSION_ID` accepts only the integer PK while its error message implies any id (a UUID
  is silently ignored).

## Verification
- From a live Claude+tmux pane, run `endless task add` with a never-before-seen verb; confirm the
  caller session's `state` is unchanged (NOT flipped to `ended`) and `endless session id` still
  resolves it during and after the verb-check.
- Confirm no new transcript file appears under `~/.claude/projects/` for the verb-check.
- Same checks for `endless session recap`.
