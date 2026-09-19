# Synthesis — run candidate build from a bare worktree shell

## Problem landed on
From a bare (non-Claude) tmux pane inside a self-dev worktree, `endless` (Python) and bare
`endless-go` silently run MAIN's code, not the worktree's — defeating the point of the
worktree (testing its own changes). Concretely: `endless` is a uv-tool console script whose
venv is editable-installed from the MAIN checkout, so it imports main's `src/` regardless of
cwd (new flags like `session next --tree` error `No such option`); bare `endless-go` resolves
the `/usr/local/bin` symlink to main's build even though the worktree-create hook already
copied a candidate to `<worktree>/bin/endless-go`. DB routing (which ledger) was solved by
E-1368/E-1281/E-1513 self-detection; this is the parallel gap for **which code executes**.

## Decisions (interview, 2026-07-01)
1. **Automatic cwd self-re-exec is the default; `esu` still works.** A plain `cd`-into-worktree
   pane must reach the candidate with zero ceremony.
2. **Both CLIs in scope** — Python `endless` and bare `endless-go` — one symmetric mechanism.
3. **Brief stderr notice on re-exec** (e.g. `endless: candidate build (e-NNN)`) so it's never
   unclear which code ran; stderr keeps stdout clean for piping.

## Approach agreed (detail carried into the follow-up task)
A pre-parse self-re-exec in each CLI entry point, gated on the existing self-dev predicate
(`config.project_is_self_dev` + the worktree-path regex) and loop-guarded via an env var —
the code-routing analog of E-1368's DB self-detection. Python re-execs the same venv python
with `PYTHONPATH=<worktree>/src` prepended (before Click parses, so new flags work); Go
`syscall.Exec`s `<worktree>/bin/endless-go`. Known limitation deferred: a stale copied Go
binary re-execs stale code (Python has no analog since its source is live).

## Follow-up spawned
- **E-1704** (`cleans_up E-1687`) — implementation to-do task carrying the full design,
  symbol anchors, and verification spec.
