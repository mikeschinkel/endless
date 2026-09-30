This task is BIG and needs its own planning session: scope includes how a sandbox is provisioned (temp dir? in-memory SQLite? cloned event log?), how it is selected (env var? CLI flag? context manager?), how event-sourcing invariants are preserved within the sandbox, and what the developer/agent ergonomics look like.

No implementation until plan is approved.
