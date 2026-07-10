Today 'endless session use' emits 5 env vars (ENDLESS_SESSION_ID, ENDLESS_HARNESS_SESSION_ID, ENDLESS_HARNESS, ENDLESS_PROJECT_ROOT, ENDLESS_WORKTREE_PATH) plus a cd. Only the integer id is canonical; the rest are derivable from it via DB. Snapshotting them into env saves an 'endless' lookup per consumer, but at the cost of staleness — exactly the class of bug E-1015 verification surfaced when the worktree was removed mid-turn.

Mike's call: minimal is right. A static value can never be stale if it's never written.

Activation block becomes:

  cd <target>
  export ENDLESS_SESSION_ID=<id>

Where <target> is the worktree path if its directory exists at lookup time, else cwd. Validation is at the point the cd line is emitted — narrow window, no value persisted.

Consumers that need other fields call 'endless session show <id> --json' (already exists, returns the full picture). One process spawn per lookup; trade for guaranteed freshness.

Extension contract changes:
- .endless/extensions/use.sh now receives only ENDLESS_SESSION_ID in its env.
- Extensions that need worktree path / project root / etc. call 'endless session show \$ENDLESS_SESSION_ID --json' and parse with jq.
- Documented as the contract; existing extensions need updating (none in the wild today).

Verification:
- 'endless session use 247' emits exactly two lines: cd and ENDLESS_SESSION_ID export.
- worktree_path at lookup time exists -> cd to it. Doesn't exist -> cd to cwd. Same logic E-1037 wanted.
- Extension's env contains ENDLESS_SESSION_ID, nothing else ENDLESS_* prefixed.
- 'endless session show \$ENDLESS_SESSION_ID --json' returns same fields as before.

Supersedes E-1037 (worktree_path validation alone is too narrow; the larger fix is the env minimization).

Origin: E-1015 verification + Mike's design call: 'It will never fail for a stale value.'