because that's the actual semantic — 'this project is endless self-development, so its worktrees should sandbox writes to avoid polluting the real DB.'

Scope: rename key in code (config loader, sandbox-init checks), docs (CLAUDE.md mentions worktree DB sandbox), Justfile recipes that reference the flag, and add a one-shot migration that rewrites old worktree_sandbox=true entries to self_dev=true on next config load.

The flag is internal/dev-only so a cryptic name is fine; clarity of intent beats consistency with the older terminology.
