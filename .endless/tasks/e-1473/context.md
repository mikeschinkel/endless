`just test-go` runs only internal/kairos, internal/events, and cmd/endless-sandbox.

It omits internal/monitor (e.g. monitor.TaskText, the E-1445 plan-text reader) and the endless-session-query / task-text read logic, so that code has no automated Go test.
