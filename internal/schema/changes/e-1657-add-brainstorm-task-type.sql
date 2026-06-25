-- E-1657: add `brainstorm` as a task type (ED-1516). brainstorm is the
-- requester-led sibling of research: research pulls information IN from
-- external sources (agent-led); brainstorm draws it OUT of the requester via
-- interview (requester-led). Both produce information-shaped artifacts
-- (text = seed/request, outcome = synthesis + spawned follow-ups); they divide
-- on direction of information flow.
--
-- The Go TaskType enum (internal/tasktype/tasktype.go) is the source of truth
-- per ED-1506; this row mirrors it into the task_types table for FK
-- enforcement and queryability. The VerifyIntegrity startup check fails closed
-- on any drift between the enum and this table.
--
-- The apply-change dispatcher wraps this file in a BEGIN IMMEDIATE transaction
-- and records the _schema_version marker after the statement below. Runs once,
-- at land time (`just land`), against the populated real DB. The sandbox and
-- tests build from schema.sql, which already declares the post-migration shape
-- (the seed row is added there too), and never apply change files.

INSERT OR IGNORE INTO task_types (id, slug, label) VALUES
    (5, 'brainstorm', 'Brainstorm');
