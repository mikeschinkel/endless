# E-1335 Analysis — endless-hook tolerance for sandbox DB gaps

## Observed symptoms (from the session that landed E-1281)

After running `endless sandbox bind` on the worktree where a Claude session was already alive, the hook started emitting these errors on subsequent events:

```
endless-hook: claude: refreshing companion file: looking up session: sql: no rows in result set
endless-hook: claude: looking up project for /Users/.../e-1281: connecting to database /Users/.../sandboxes/worktree-e-1281/...
```

The errors are non-fatal (hook continues), but they surface as Claude UI noise on every UserPromptSubmit / PreToolUse / PostToolUse, which is unacceptable for normal use.

## Why it happens

Two distinct windows where the sandbox DB legitimately doesn't have the rows the hook expects:

1. **Mid-flight binding.** Claude session started against the prod DB → SessionStart hook fired against prod → session row exists in prod. Then `endless sandbox bind` flips the worktree's `.claude/settings.json` env block so subsequent hook calls inherit `XDG_CONFIG_HOME=<sandbox>` and read the *sandbox* DB. The sandbox has no row for this session_id.

2. **Fresh-sandbox first event.** New Claude session starts inside a worktree that was bound but never used. SessionStart hook fires before anything else has touched the sandbox DB. Project lookup by cwd may race against auto-migrate / first-write initialization.

Both produce `sql: no rows in result set` style errors from `monitor.GetSession()` / `monitor.ProjectIDForPath()`.

## Proposed fix

In `cmd/endless-hook/claude.go` and `cmd/endless-hook/prompt.go`, distinguish "row not found" (errors.Is(err, sql.ErrNoRows) or equivalent) from "DB is unreachable":

- **Row-not-found from session lookup:** return silently or fall through to a "register this session" branch. The hook is already responsible for SessionStart-side registration; reuse that path on demand for stale references.
- **Row-not-found from project lookup:** fall through to auto-register or skip the project-scoped action. The hook's behavior degrades gracefully — no telemetry recorded for this event, but no error noise.
- **Genuine DB connection failure:** still log (real problem, not a sandbox-transition artifact).

Out of scope:
- Migrating the prod-DB session row to the sandbox at bind time (E-1281 explicitly went start-fresh on migration).
- Auto-restarting the Claude session when bind happens mid-flight.

## Verification

1. Bind a worktree while a Claude session is already running against prod; trigger a UserPromptSubmit; confirm no error message appears (hook silently skips the session refresh).
2. Spawn a fresh Claude session inside a sandbox-bound worktree; confirm SessionStart registers the session in the sandbox DB and subsequent events find it.
3. Cause a real DB connection failure (chmod 000 the sandbox DB file); confirm the hook still logs an error in that case — we're not silencing real problems.
