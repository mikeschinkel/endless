Per-session AI memory (~/.claude/projects/<project>/memory/MEMORY.md and friends) only helps the same Claude instance with that memory directory. It does not reach: other Claude sessions on other machines, other AI harnesses (Codex, etc.), human users of endless-the-product, or future fresh sessions that haven't accumulated those rules.

Yet many learned rules are project-level — they describe how endless's workflow is meant to work, not personal user preferences.

Examples surfaced in 2026-05-01 conversation:
- 'Long-form task content goes in .endless/plans/E-NNN.md attached via --text; description stays a 2-3 sentence pitch.'
- 'The session that creates a plan file owns committing it.'

Both are about endless's plan workflow.

(User's global ~/.claude/CLAUDE.md already has a pattern: 'After ANY correction from the user: update ~/.claude/LESSONS.md with the pattern.' That's user-scoped. Project-scoped equivalent would be e.g. .endless/lessons/ or project-LESSONS.md.)

Origin: 2026-05-01 conversation. Mike: 'create a task for another session to work on that come up with a strategy to write learnings to the endless repo in parallel with memory. This may be unique to the endless project itself, or maybe it is a general lessons feature that all projects could use.'
