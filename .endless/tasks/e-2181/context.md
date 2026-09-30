endless task spawn never passes claude --name, so a spawned session falls back to Claude Code's default: the worktree basename plus a two-character disambiguator, e.g. e-1983-f2.

The whole path already exists and is used by the explicit --name flag; only the default is missing.

Matters because ListAgents and SendMessage address sessions by name, which is the transport E-1995 depends on, so the name is an address rather than a label.
