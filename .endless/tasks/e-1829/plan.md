# Goal

Make Endless safe and ergonomic for more than one developer to work the same project concurrently, sharing state through git (GitHub push and pull) rather than a shared live database.

## Short-term milestone

Two developers collaborate on an Endless-tracked project via GitHub push and pull, including rebuilding the local SQLite projection from the shared db-ledger. This is the first concrete deliverable and the bar the rest of the epic is measured against.

## Known workstreams (children and linked)

- Collision-free task IDs — today's monotonic E-NNN clashes when two developers file concurrently. A brainstorm child evaluates the candidate approaches.
- db-ledger rebuild robustness — the projection must rebuild deterministically from a ledger authored by multiple people. Relevant existing work: rebuild-db FK and UNIQUE failures, the event-upcasting pipeline, and neutralizing committed test-fixture ledger events.
- Pluggable task backends — external trackers (Beads, JIRA, GitHub Issues, Notion) as synced mirrors; a pluggable ID-minting service is a subset of this.