# Where the rebuild work moved (2026-08-25)

This epic held the April five-stage design. The rebuild-reliability half of it
is owned by **E-1935**, filed in August for exactly that, and the measured state
of `rebuild-db --confirm` — the full transitive FK closure, the four gaps and
their owners, and the ordering constraint on `sessions.task_id` — lives in
E-1935's analysis rather than being duplicated here.

E-1041, E-2062 and E-1728 moved to E-1935 with it. E-894 and its subtree moved
to E-1486, the port epic, being port work rather than event-sourcing work.

What remains here is the design-stage work: E-806, E-809, E-810, E-813, E-814,
E-816, E-817, plus E-910 and E-914.
