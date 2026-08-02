## Source material for VISION.md

**The arc.** Endless began as a way for one developer to manage many concurrent
Claude Code sessions — first across terminal tabs, then across tmux windows. The
envisioned future is a substrate for many developers collaborating on the same
projects, each driving many AI-agent sessions.

**Core tenets to develop as narrative:**

1. **Humans managing ~2 orders of magnitude more concurrent work.** The human's
   job shifts from doing the work to steering many Claude Code sessions —
   research, coding, bug-fixing, and brainstorming.
2. **A task-centric loop.** The human has Claude Code file task(s), collaborates
   with it on a plan, then spawns each task into its own Claude Code session in a
   new tmux window with a task-specific git worktree and a config/DB sandbox. The
   session works the plan and hands back a verification script that gives the
   human confidence to land — merging the worktree back to `main`.
3. **Collaboration via git, not a server.** Several developers share one project
   by ordinary push/pull; each rebuilds their local view from the same committed
   history; no central authority; contributors never collide.
4. **The DB ledger as the foundation.** A JSONL write-ahead log is the source of
   record, with a rebuildable local DB and per-task isolation — behind-the-scenes
   plumbing that makes the collaboration above viable.

**Give headline billing to:** lowering the human's review burden. Agents report
in structured, skimmable form and don't make the human re-review what the tool
already computed. This is a core Endless philosophy and deserves a prominent place
in the narrative.
