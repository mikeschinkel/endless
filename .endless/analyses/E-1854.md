Endless tells THIS repo's agent to run `endless guide` only because the dogfooding repo's committed CLAUDE.md says "First: run endless guide". A downstream project that uses Endless as a product gets no such pointer: the Claude SessionStart / UserPromptSubmit hook (internal/hookcmd/claude.go, buildTaskContextInjection) injects the active task list and inter-session message banners but never mentions the guide, and setup.py never writes anything into a downstream CLAUDE.md. So product users' agents have no reliable way to learn the Endless workflow.

Ship the pointer in the product, not in per-machine config: add a one-shot "Run `endless guide` to learn the Endless workflow" line to the first-time SessionStart context injection (alongside the task-list injection, gated by the same HasInjectedContext one-shot marker so it is not repeated every prompt). This keeps README.md human-only (the guide is agent-formatted and is not routed to humans there) while making the guide reachable by every agent on every project that has the hook installed, regardless of that project's CLAUDE.md.

Surfaced while rewriting README.md (E-1832): the README dropped its human-facing `endless guide` block on the assumption that CLAUDE.md covers the agent path — true only for the dogfooding repo.

Pinned wording (inject verbatim, as the lead line of the first-injection context, before the task list):
New to this project? Run `endless guide` to learn the Endless workflow.
