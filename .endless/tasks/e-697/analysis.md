Architecture: channel table (beacon/connect pattern), message queue table, delivery via hook additionalContext. MVP requires tmux: when Session A sends a message, the hook uses tmux send-keys to inject a prompt into Session B's pane, triggering UserPromptSubmit which picks up the queued message. Human stays in Session A — approves actions, sees replies.

No terminal switching needed.

MVP commands: endless msg beacon, endless msg connect, endless msg send.
