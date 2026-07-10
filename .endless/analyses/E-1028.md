The CLI's TaskIDType (src/endless/cli.py:10) accepts both 'E-1027' and '1027'. The hook's regex matchers in DEFAULT_MATCHERS (src/endless/matchers.py:75,79) only match bare digits — 'endless\\s+task\\s+start\\s+(\\d+)'. So 'endless task start E-1027' typed in a Bash tool is silently a no-op for the session-state side effect: StartWorkSession never fires, sessions.active_task_id stays NULL.

Discovered while verifying E-1027 (companion worktree_path population): my own session showed active_task_id=NULL despite running 'endless task start E-1027' earlier. Root cause traced to the regex.

Fix: relax both regexes to accept an optional E- (or e-) prefix. Capture group still extracts only digits so claude.go's strconv.ParseInt is unaffected:

  start:    endless\\s+task\\s+start\\s+(?:[Ee]-)?(\\d+)
  complete: endless\\s+task\\s+complete\\s+(?:[Ee]-)?(\\d+)

Two places need updating:
1. src/endless/matchers.py DEFAULT_MATCHERS — fixes future seeds.
2. Existing machine configs (~/.config/endless/config.json) — needs a one-shot migration or manual edit. The seed function only writes DEFAULT_MATCHERS when 'matchers' key is missing; it never updates. Decide: ship a tiny migration in the seed function, or document a one-time manual edit.

Verification:
- Run 'endless task start E-1027' in a Bash tool. sessions.active_task_id is set in DB (was NULL before). Companion file's worktree_path populates if a worktree exists.
- 'endless task start 1027' (bare integer) still works (backward compat).
- 'endless task complete E-1027' likewise.

Out of scope: any other CLI/hook gap where E- prefix is silently dropped. File separately if found.

Origin: E-1027 verification revealed the gap.