Mike, 2026-10-14, after reading docs/research-2026-10-14-claude-hooks-and-mods-across-platforms.md.

Endless assumes every Claude session working on a task runs where its Claude Code settings hooks fire: a local CCC session on the user's machine, normally in an Endless-controlled tmux server. The research report shows several ways Claude can work on an Endless-managed repo without those hooks:

- Cloud sessions (started from Claude on the web (CW), Claude mobile (CM), Claude Code Desktop (CCD) Cloud or **Open in > Cloud**, `claude --cloud`, `/autofix-pr`, routines) run on an Anthropic Ubuntu VM. They never read the user-level settings file or `.claude/settings.local.json`, read the repo's committed `.claude/settings.json` only in single-repo sessions, and install no repo-declared plugins. A committed hook that calls the Endless binary fails with a *non-blocking* error by default, so the session carries on unmanaged.
- CCD local sessions are documented to read the same settings files, but users report settings hooks silently not firing there (issues #96496, #97977).
- Server-mode slots of `claude remote-control` opened from the phone have been observed not firing settings hooks.
- Silent off-switches: `disableAllHooks`, `--safe-mode`, `--bare`, `allowManagedHooksOnly`, multi-repo cloud sessions, and others.

The report (from Claude Research) recommends local-only execution with Remote Control for viewing and steering, PreToolUse/UserPromptSubmit command hooks with `onFailure: "block"` (Claude Code v2.1.295+), a committed guard that makes cloud sessions on Endless-managed worktrees fail loudly, and an out-of-band SessionStart heartbeat/nonce check so Endless can tell a managed session from an unmanaged one.

Mike wants a review of Endless's own code, decisions and tasks to determine whether Endless needs hardening so that using Claude in ways incompatible with CCD, CW and/or CM cannot cause problems — for example, Claude working on a task while no hooks are engaged.

Mike, 2026-10-14: the report came from Claude Research; its recommendations are not his. It is a starting point for discussion, and he expects follow-up questions and possibly further research once the findings are in. Evaluate its recommendations; do not treat them as adopted.
