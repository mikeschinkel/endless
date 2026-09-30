Python CLI shells out to Go for all event writes — single implementation, no format divergence. Writes to .endless/events/events-{node_id}-{seq}.jsonl. Rotation when segment exceeds threshold.

DB remains authoritative in Stage 1.

Enables sqlc from day one.
