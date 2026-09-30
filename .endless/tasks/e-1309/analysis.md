Fix: event_bridge.emit_event always passes the project's main checkout (from projects table) so JSONL appends and git commits both target main; worktree branches stay ledger-free.

Same routing likely applies to verbs.jsonl and snapshots; verify each.

Doesn't apply when E-1281's sandbox is active — sandbox writes don't touch main at all.
