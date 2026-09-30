Open question: project-specific feature, or general 'lessons' infrastructure all endless-managed projects could use?

Considerations / things to weigh:
- Where do learnings live? .endless/learnings/<topic>.md? .endless/conventions.md? Inline in quick-start guide? Repo-level CLAUDE.md? Multiple?
- Mike's pushback (2026-05-01): 'CLAUDE.md does not work because everyone's CLAUDE.md may be different.' Repo-rooted CLAUDE.md is shared, but users may override or omit it; can't rely on it as authoritative.
- Auto-propagation vs. manual: should there be tooling (e.g. 'endless lesson add') that captures a rule and writes it to the repo? Or just a convention agents follow?
- How does an agent/user discover existing lessons? Surface in 'endless quick-start'? A new 'endless lessons' command?
- Distinguish project-level rules (apply to all who work on this project) from personal rules (apply to this user's preferences). The latter still belongs in personal memory only.
- Migration path: rules already in personal memory that should also be in the repo — how do they get there?

Out of scope for this design task:
- Implementing the chosen strategy (file follow-up once design lands).
- Migrating existing personal-memory rules into the repo (separate task).
