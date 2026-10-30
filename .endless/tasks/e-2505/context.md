Rules about how agents should work with Endless across projects have no Endless-managed home. On 2026-10-30 Mike confirmed a cross-project rule: work that would change another Endless project's repo is filed as a task in that project, not made from the current task. It was recorded only in one Claude Code project memory (gomion's), so it loads in gomion sessions only; the alternative, the user's global ~/.claude/CLAUDE.md, is the user's own file and not Endless's.

Mike's direction: since the rule is Endless-specific it should be managed by Endless, for example in ~/.config/endless/AGENTS.md, which `endless setup` links into ~/.claude/CLAUDE.md.

Claude Code loads an `@path` import line in CLAUDE.md into context at startup; a plain Markdown link is only text that an agent may never open. Agents other than Claude Code read AGENTS.md through their own mechanisms.
