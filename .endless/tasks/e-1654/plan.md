# Guard `rebuild-db --confirm` against a lossy projection

## Symptom / risk
`endless-go event rebuild-db --confirm` replaces the `tasks` table with a
from-scratch ledger projection. Right now that projection is **lossy**: a dry-run
replays to ~**714 tasks vs 947 live** — it would silently destroy ~**233 tasks
(~25%)**. It's a live data-loss footgun; only the dry-run-by-default behavior and
luck have prevented a wipe.

## Why the replay loses tasks (observed in the dry-run)
Replaying historical events from scratch hits constraints the incremental live
writes tolerated:
- `FOREIGN KEY constraint failed` — children created before their parent in event
  order (e.g. tasks 910–912, 1061).
- `UNIQUE constraint failed: tasks.id` — the same id created by multiple
  `task.created` events (e.g. 1034–1039, id 1).
- Bad payloads — `invalid phase "foo"`.

## Near-term fix (this task)
Make `--confirm` **refuse** (or require an explicit second force flag) when the
projected task count is materially below the live count, and surface the warning
summary prominently (count of failed inserts, by reason). Goal: it is impossible
to silently shrink the DB. This is a guardrail, not the cure.

## Long-term
The actual cure — a faithful, replayable ledger (deterministic ordering, idempotent
inserts, event upcasting) — is the research task E-1652. This bug only ensures the
current broken replay can't nuke data in the meantime.

## Relations
- Relates to E-1652 (the long-term replay/migration design).
