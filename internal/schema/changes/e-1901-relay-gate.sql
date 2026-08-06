-- E-1901: add the 'relay' gate kind and its two per-kind subject columns on
-- session_gates, backing the verbatim-report-relay Stop gate.
--
-- gate_kinds mirrors the GateKind Go enum (ED-1506: const-in-code is the source
-- of truth, the table exists for FK enforcement + queryability). The startup
-- integrity check (monitor.DB -> gatekind.VerifyIntegrity) fails closed on
-- drift, so this seed row and the GateKindRelay constant must land together.
--
-- The 'relay' kind reuses session_gates rather than introducing a table because
-- the table is already "a pending interception for a session" with a kind_id
-- discriminator and named per-kind subject columns. It differs from 'revisit'
-- only in WHICH hook consumes it: revisit blocks the next tool call at
-- PreToolUse, relay blocks turn end at Stop.
--
-- sanctioned_text holds the exact report text the session owes the user as its
-- final message. bounces counts Stop blocks and is the loop guard: Claude Code's
-- stop_hook_active flag is undocumented, so the cap is enforced on a value we
-- control. Both are nullable/defaulted so existing revisit rows are unaffected.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and records this change's _schema_version marker after the statements below.
-- Runs once, at land time, against the populated real DB. The sandbox and tests
-- build from schema.sql (which also declares these) and never apply change files.

INSERT OR IGNORE INTO gate_kinds (id, slug, label) VALUES
    (2, 'relay', 'Relay');

ALTER TABLE session_gates ADD COLUMN sanctioned_text TEXT;

ALTER TABLE session_gates ADD COLUMN bounces INTEGER NOT NULL DEFAULT 0;
