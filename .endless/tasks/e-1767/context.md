session_tasks captures 'somehow interacted with' a task, not just 'worked on' it (Mike, 2026-07-10).

Today only ten content and lifecycle task events record a touch via upsertSessionTask (internal/events/executor.go): created and imported as surfaced, claimed as goal, and status_changed, fields_updated, moved, deleted, bulk_cleared, landed, released as revisited.

The relation edits (task link, block, unblock, unlink) emit task_dep.created and task_dep.deleted (internal/events/event.go), which have no executor in that touch path, so linking or blocking a task never enrolls it in session_tasks.
