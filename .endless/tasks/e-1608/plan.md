
## Settled during implementation (Mike, 2026-09-28)

- **The seeding hook is a new `.endless/hooks/seed-sandbox.sh`**, not post-worktree-create.sh — that hook copies main's bin/endless-go over the worktree's, so running it every verify would verify main's build. Endless's own post-worktree-create.sh no longer seeds; worktree creation runs `sandbox reset` after the create hook. The seed hook is discovered in the MAIN checkout, like every project hook (a worktree copy may be missing or stale); it runs with cwd=worktree, $1=worktree, $2=sandbox.
- **Endless's standard contents are the self-ignoring .gitignore only.** The self_dev endless.db is not PRODUCT behavior; Endless's own seed-sandbox.sh seeds it.
- **Surface: Go `endless-go sandbox reset` + thin Python `endless sandbox reset` shim** (src/endless/sandbox_cmd.py) until E-1063 merges the binaries.
