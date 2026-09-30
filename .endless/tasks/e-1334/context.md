Verbs validated via Claude API while a worktree sandbox is active are written to <sandbox>/endless/verbs.jsonl (sandbox machine layer) instead of ~/.config/endless/verbs.jsonl (real user machine cache).

Future runs in any other project re-validate the same verbs and re-burn tokens.
