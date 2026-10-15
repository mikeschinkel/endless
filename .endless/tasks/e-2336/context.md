Anthropic relaunched Projects inside Claude Code on 2026-09-17 (public beta on Pro and Max). A project is now one coordinator conversation that starts parallel worker threads. Threads run in the cloud on their own branches by default, or locally via Remote Control. A project has shared memory, instructions, a file Library, and can span several repos. It is used from claude.ai/code, the desktop Code tab and the mobile app, not the terminal CLI. A project requires github.com plus the Claude GitHub App, belongs to one user and cannot be shared, and cannot adopt a session started locally. No project ID is exposed to hooks, environment variables, `claude agents --json`, the SDK or any API.

This overlaps Endless on parallel isolated sessions, handoff and monitoring. It also collides with Endless's use of the word "project" at the opposite granularity: one Anthropic Project spans many repos.

Background material: docs/research-2026-10-14-claude-projects-and-endless.md, produced by Claude Web Research. Its recommendations (rename to "repo" now, Remote Control for spawned sessions, a WorktreeCreate hook, per-task auto memory, a claude.ai connector, the positioning statement) are not an approved direction. They are inputs to this discussion, not a work plan, and its facts should be checked before anything depends on them.

Questions to discuss:
- Leverage: what Endless should use from Projects and the surrounding Claude Code surfaces where applicable, and what it should refuse to depend on while there is no interface.
- Differentiate: whether to rename Endless "project" (to "repo" or something else), what that costs in the CLI, schema, ledger and docs, and how to describe Endless so it is not read as a clone of Projects or agent view.
- Value beyond a Claude account: what Endless offers someone who already has Projects and agent view, e.g. enforced task states and plan gate, human verification before landing, Git-ledger collaboration between people, local-first execution, repos not on github.com, other harnesses.
- Conflicts: Project threads that merge into an Endless repo without passing Endless's verify gate; worktree locations and shared auto memory across worktrees.
- PRODUCT: how each answer lands for a user on another machine managing a project that is not Endless.
