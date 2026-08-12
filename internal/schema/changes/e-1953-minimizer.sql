-- E-1953: turn the relay checkpoint into the minimizer's corpus row, and give
-- `sessions` the three fields the Stop gate needs to reason about ONE turn.
--
-- Two groups of columns, on two tables, for two different lifetimes.
--
-- session_gates (permanent, one row per reported turn). E-1901 already stores
-- `sanctioned_text` — the exact text the session owes the user as its final
-- message — so the row is already two thirds of the eval corpus triple that
-- E-1952's design calls for. Adding `raw_draft` and `user_prompt` completes it
-- (prompting user message, raw draft, minimized output) without a capture
-- pipeline: the raw draft has to be persisted anyway to back `--raw`, the
-- minimized text is the command's own stdout, and the user message is already
-- in the hook's hands at UserPromptSubmit.
--
-- `raw_draft` is what makes the minimizer safe to be aggressive. Nothing it cuts
-- is destroyed, only hidden, so the failure mode of an over-cut is a `--raw`
-- away rather than lost work.
--
-- `label` / `label_text` land LATER than the row they annotate — the user types
-- `$CUT you dropped the verify command` on the turn AFTER the one being judged,
-- so these are nullable and written by a second statement. `task_id` is
-- attribution only: the corpus keys on the session, which always exists, while
-- the task id does not (E-1953 allows an id-less report so an unclaimed
-- quick-question session is still covered).
--
-- sessions (per-turn, reset when the user speaks again):
--
--   last_user_prompt — the prompting message, staged here at UserPromptSubmit so
--     the report command can copy it into the corpus row. The command runs as a
--     subprocess with no access to the turn's prompt otherwise.
--   report_bounces  — the loop guard for the NEVER-CALLED case. E-1901's
--     `session_gates.bounces` cannot serve: it counts on the checkpoint row, and
--     the whole point of this case is that no such row exists. Without a counter
--     we own, an agent that does not understand the instruction livelocks.
--   report_exempt   — the `$FULL` one-turn license. Set at UserPromptSubmit,
--     consumed at Stop. It must survive the gap between those two events, and a
--     process-local flag cannot: each hook firing is a separate process.
--   report_runs     — how many times `task report` has produced output this
--     turn. Bounds the appeal at one: an unbounded appeal is a second bite the
--     agent will always take, so the first run is the report and the second is
--     the appeal, and a third is refused.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and records this change's _schema_version marker after the statements below.
-- Runs once, at land time, against the populated real DB. The sandbox and tests
-- build from schema.sql (which also declares these) and never apply change files.

ALTER TABLE session_gates ADD COLUMN raw_draft TEXT;

ALTER TABLE session_gates ADD COLUMN user_prompt TEXT;

ALTER TABLE session_gates ADD COLUMN task_id INTEGER REFERENCES tasks(id) ON DELETE SET NULL;

ALTER TABLE session_gates ADD COLUMN label TEXT;

ALTER TABLE session_gates ADD COLUMN label_text TEXT;

ALTER TABLE sessions ADD COLUMN last_user_prompt TEXT;

ALTER TABLE sessions ADD COLUMN report_bounces INTEGER NOT NULL DEFAULT 0;

ALTER TABLE sessions ADD COLUMN report_exempt INTEGER NOT NULL DEFAULT 0;

ALTER TABLE sessions ADD COLUMN report_runs INTEGER NOT NULL DEFAULT 0;
