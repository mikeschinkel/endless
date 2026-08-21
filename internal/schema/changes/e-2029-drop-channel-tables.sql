-- E-2029: drop the three inter-session-channel tables.
--
-- The channel surface is gone: `endless channel` and its subcommands, the
-- endless-channel MCP server (internal/channelcmd), monitor/messaging.go, and
-- the Register/Unregister/LookupChannelPort helpers were all removed by this
-- task. Nothing reads or writes these tables any more.
--
--   channels      — the MCP plugin's per-process HTTP port registry. Keyed on
--                   the same non-unique sessions.process string behind E-1898's
--                   identity incident; this was its last consumer.
--   conversations — the beacon/connect pairing between two live sessions.
--   messages      — the queue of undelivered messages for a conversation.
--
-- Order matters: messages carries a FOREIGN KEY to conversations, so it is
-- dropped first. (SQLite tolerates either order with foreign_keys off, but the
-- dependency should be explicit rather than incidental.)
--
-- NOT to be confused with `session_messages` — the transcript-derived session
-- history table and its FTS5 index — which is unrelated and stays.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and records this change's _schema_version marker after the statements below.
-- Runs once, at land time, against the populated real DB where the tables still
-- exist. The sandbox (`endless-sandbox init`) and tests build from schema.sql,
-- which no longer declares them, and never apply change files — so there is no
-- "table absent" path to guard.

-- The pre-E-742 names are dropped too. db.py's _migrate_v2 used to rename
-- msg_queue -> messages and msg_channels -> conversations; that rename went
-- with the channel surface, so a DB old enough to still carry the original
-- names would otherwise keep them forever.

DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS msg_queue;
DROP TABLE IF EXISTS conversations;
DROP TABLE IF EXISTS msg_channels;
DROP TABLE IF EXISTS channels;
