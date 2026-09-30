discarding those corrupts the DB WAL.

Surgical trigger: the gate fires ONLY when the dropped range includes a ledger commit, never on every destructive op, to avoid crying-wolf.

Hard block for now, agent-scoped only; revisit to warn-with-override if noisy.

On completion: prune memory file feedback_never_discard_auto_record_commits.md + its MEMORY.md index line — the lesson lives in this gate.
