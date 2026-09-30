lane add/update/delete/reorder; task add/update/move/promote/demote/reorder/delete (delete --lane for clearing).

Each command wraps work in BEGIN IMMEDIATE TRANSACTION and emits one event in project_next_events.
