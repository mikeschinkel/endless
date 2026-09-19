-- E-2148: re-prefix the warning-severity fault codes from ERR- to WARN-.
--
-- A code's prefix now states its severity: WARN-NNNN for a warning, ERR-NNNN
-- for an error. Seven of the fourteen catalog entries were severity warning
-- while carrying an ERR- prefix — ERR-0001 had said "error" and meant "warning"
-- since E-698 — so their ids change here and in internal/faults/codes.go.
--
-- NUMBERS DO NOT MOVE. ERR-0001 becomes WARN-0001, never WARN-0006. A number is
-- spent the moment it ships; renumbering would make every log line and bug
-- report that cites one refer to a code that now means something else, and no
-- migration reaches a log file. Only the prefix changes.
--
-- This is what keeps already-recorded incidents resolvable. `errors.code` is a
-- plain string, and faults.LookupCode resolves it by exact id, so a row written
-- before the change would fall back to printing the bare code with no title and
-- no remedy. One UPDATE per changed code, keyed on the old id.
--
-- Idempotent BY CONSTRUCTION, not merely by the _schema_version marker: every
-- statement matches the OLD id, which after a first run matches nothing. Running
-- the file twice leaves the same rows either way. (The dispatcher records this
-- change's marker and skips a second run, but a change file should survive a
-- hand re-run without needing it to.)
--
-- The `severity` column is NOT touched. It already holds the right value on
-- every row — that is precisely how we know which codes were mislabelled — and
-- rewriting it would risk changing a fact about what happened while fixing how
-- it is named.
--
-- `errors.jsonl`, the per-occurrence detail log, is NOT rewritten. It is an
-- append-only machine-local capture, never replayed, and a historical line
-- saying ERR-0001 is an accurate record of what the code was called when that
-- occurrence was captured.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and records this change's _schema_version marker after the statements below.

UPDATE errors SET code = 'WARN-0001' WHERE code = 'ERR-0001';  -- job-failed
UPDATE errors SET code = 'WARN-0004' WHERE code = 'ERR-0004';  -- job-scheduling
UPDATE errors SET code = 'WARN-0005' WHERE code = 'ERR-0005';  -- job-stuck-lease
UPDATE errors SET code = 'WARN-0006' WHERE code = 'ERR-0006';  -- test-warning
UPDATE errors SET code = 'WARN-0009' WHERE code = 'ERR-0009';  -- triage-failed
UPDATE errors SET code = 'WARN-0012' WHERE code = 'ERR-0012';  -- unlanded-cache-unwritable
UPDATE errors SET code = 'WARN-0013' WHERE code = 'ERR-0013';  -- turn-failed-transient
