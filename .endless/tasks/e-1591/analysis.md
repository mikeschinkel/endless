These helpers only ever wrap 'session use/cd/forget', which operate on the real session ledger, never a per-worktree sandbox — so the wrapper should always target --db main.

Likely fix: have _endless_run pass '--db main' on the 'uv run --directory' path (covers esu/esp/esf, since all session commands target the real DB).
