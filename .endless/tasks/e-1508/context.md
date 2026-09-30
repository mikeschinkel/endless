Closes a friction gap that pushes agents (and humans) toward ad-hoc /tmp DB copies when the on-design path is the worktree's sandbox.

Today, seeding the sandbox requires 'cp ~/.config/endless/endless.db ~/.cache/endless/sandboxes/worktree-e<id>/endless/endless.db' — exact path with the right task id and subdir.

Origin: surfaced 2026-05-29 when verifying E-1378 — the ad-hoc /tmp approach defeated the E-1429 gate and the friction of manual sandbox seeding pushed toward the wrong tool.
