execTaskClaimed (internal/events/executor.go) sets only active_task_id on claim, never active_epic_id;

That blocks the E-1571 status-line [E-EEEE:E-CCCC] prefix and the endless agents (E-1621) interactive auto-resolve.
