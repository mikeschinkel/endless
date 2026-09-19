Reframe the `land` description in the using-endless-in-sessions guide (docs/guide/ source, then regenerate).

## Problem
The guide presents `endless worktree land` as "merge your worktree branch into main" — a code-merge framing. That invites an agent to infer "a research/brainstorm task has no code, so nothing to land," which is false: completing any task records db-ledger (JSONL = DB WAL) entries that must reach main. Every task type lands; only `epic` (a container) has no worktree of its own.

## Change
- Lead the `land` description with the ledger: land brings the worktrees recorded ledger entries (and any code changes) into main. Code is one payload; the ledger is always a payload.
- State the invariant explicitly: every task type — todo, bugfix, research, brainstorm — lands; `epic` is the only type worked through its children rather than its own worktree.
- Keep it brief and within the existing land section; regenerate the guide cross-reference after editing the source.

## Verify
`tests/tasks/e-NNNN-verify.sh` (e-1577 model): assert the rendered `endless guide` land section contains the ledger-first framing and the every-type-lands statement (grep for the invariant phrases); hand off `esu && ./tests/tasks/e-NNNN-verify.sh`.