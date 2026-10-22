## Facts from Claude Code's documentation (code.claude.com/docs/en/settings-reference, claude-directory, settings), checked 2026-10-22
- `cleanupPeriodDays`: default 30, minimum 1, `0` fails validation, no documented maximum and no "keep forever" value; the docs suggest 3650 for long retention.
- Any settings file may set it (user, project, local, managed; managed wins). The deletion pass covers every project under Claude's projects directory and runs in the background after any session starts. Which file's value it uses when files disagree is not documented.
- No environment variable or CLI flag sets it.

## Decisions taken while planning (with the user, 2026-10-22)
- Write the user's Claude settings file only (Q1 A). A committed project file would not protect a teammate, because a session they start in any other project can still delete with their own value.
- Apply it from `endless setup claude-hook` (Q2 A), the step every user already runs.
- Both a doctor check (when E-2285's registry exists, and so on its periodic run) and a SessionStart warning. Transcript deletion can be catastrophic, and the warning only shows while the value is too low.
- 3650 days is effectively forever. A larger undocumented value is avoided: an invalid value could leave Claude unable to read the settings file, which would also break the hooks in it.
- Config key `claude.transcripts.retention_days`: a `transcripts` object is the grouping most likely to gain siblings, for example E-2416's transcript capture.
- `--yes` for scripting, since setup asks first.

## Decided without asking
- Endless never lowers Claude's value; a higher one is reported and left.
- A lower managed value is reported, not fought.
- Setup honors `CLAUDE_CONFIG_DIR` for Claude's home, as `session_cmd` already does.
