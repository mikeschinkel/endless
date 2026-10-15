The spawn handoff tells the agent to pass `--db main` on every `endless` command that must reach the main database. In a project without self-dev, such as go-cfgstore in E-2206, every one of those commands is refused: "--db only applies to projects with self-dev enabled". Mike reports the same problem with `esm`, and possibly with many other commands, prompts and guide sections that assume the Endless repo.

PRODUCT: every project other than Endless itself is single-database, so every spawned agent outside Endless starts with wrong instructions.
