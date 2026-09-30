Renames: sessions.active_goal_id→active_task_id, sessions.tmux_pane→process, conversations.pane_a→process_a, conversations.pane_b→process_b, conversations.channel_id→conversation_id, messages.channel_id→conversation_id. Rename Go structs: SessionInfo.ActiveGoalID→ActiveTaskID, ChannelInfo.PaneA→ProcessA/PaneB→ProcessB/ChannelID→ConversationID, MessageInfo.ChannelID→ConversationID. Rename Go functions: SetTmuxPane→SetProcess, BackfillTmuxPane→BackfillProcess, GetTargetPane→GetTargetProcess, SessionIDForPane→SessionIDForProcess. Update Python dict key access.

NOT renamed: MCP protocol field channel_id in cmd/endless-channel, CLI arg channel_id, TMUX_PANE env var.

Files: sql/schema.sql, internal/monitor/db.go, src/endless/db.py, internal/monitor/session.go, internal/monitor/messaging.go, src/endless/channel_cmd.py, cmd/endless-hook/claude.go, internal/web/queries.go.
