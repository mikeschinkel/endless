Line-delimited JSON. Initial task/dep/session/project event kinds including revisit status and tags. Events stored in segmented files per node: .endless/events/events-{node_id}-{seq}.jsonl.

Each machine writes only to its own segments; git merge adds files, never line conflicts.
