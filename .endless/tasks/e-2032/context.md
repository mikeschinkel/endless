templatecmd resolves a template as <project_root>/.endless/templates/<name>.local.tmpl, then <name>.tmpl, then the Go embed.

That is per-project only: a user who wants the same handoff or guide wording across every project they work on must copy the override into each one, and re-copy it whenever a project is added.

Surfaced from E-2030, where gating the guide on report_gate raised the question of where a user edits shipped text.
