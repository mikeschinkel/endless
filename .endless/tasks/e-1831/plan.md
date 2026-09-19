# Problem

Endless mints monotonic E-NNN IDs from a single local counter. Two developers filing concurrently both mint the same next number, so ledgers collide on merge. A distributed-safe identifier scheme is required before two people can share a project through git.

# Candidate approaches (to explore, not yet rank)

## 1. Human-initials suffix
Append a per-developer suffix to the number, e.g. E-123-abc vs E-123-xyz.
- Each developer omits their own suffix when typing their own IDs but must type others' suffixes.
- Cheap; no new infrastructure. Cost: humans type and remember initials; the suffix namespace is manually managed.

## 2. Content-hash paired identifier (already filed as E-816)
A local human-friendly id plus a content-hash global id (SHA-256 prefix), mapped one-to-one in the projection; cross-repo deps use the global id.
- Git-hash-like; no coordination needed. Cost: global ids are not human-memorable; needs a stable local-to-global mapping story on merge.

## 3. Central ID-dispensing service
A pluggable CreateTaskId service hands out globally-unique ids on demand.
- A subset of the pluggable Task-backend service (E-800). Removes collisions by coordination. Cost: requires availability of the service; offline filing needs a fallback.

## 4. Git-as-CAS dispenser (added from E-1926, 2026-08-09)
Use the team's existing git remote as the coordination primitive instead of standing up a service. To mint an id: read the id file from a dedicated repo or Gist, increment it, commit, and push. A successful push IS the allocation; a rejected (non-fast-forward) push means someone else took that number, so pull, take the next one, and retry. Classic compare-and-swap, with the git host providing the serialization.

- Zero new infrastructure — no service to write, deploy, host, or keep up.
- Availability equals the availability of the git host the team already cannot work without, which answers the "what if the dispenser is down" objection against #3 (if GitHub is down, the team is not collaborating anyway).
- Ids stay short bare integers — nothing extra to type, unlike #1 and #2.
- Self-hostable end to end: an open-source user's provider can be their own LAN git server.
- Cost: latency is a push round-trip per `task add` (acceptable — this is an interactive command), and contention retries grow with team size, though a team small enough to share one Endless project will not contend meaningfully.
- Same shape as #3 from the caller's side: both are just an `allocate()` behind the pluggable provider interface, so choosing #4 does not preclude adding #3 later. #4 is arguably the reference implementation of #3 that ships without a server.

# Constraints any candidate must satisfy

## No id is ever reused (from E-1926, locked 2026-08-09)
A freed id that can be re-minted reintroduces the whole orphaned-row bug class: FK-free rows keyed on a task id (`session_tasks`, `session_hidden_tasks`, `session_notices`, `project_next_tasks`/`_pending`) deliberately outlive their task, and a reused id makes them resurrect against unrelated work. E-1915 hit this for `task_deps`; E-1926 found four more tables and settled the policy: **no entity id is ever reused, for any entity type.** Whatever scheme wins here must make re-minting a spent id impossible, not merely unlikely. Gaps in the sequence are legal and permanent.

## An id is spent by the ledger, not by the table (from E-1926)
The SQLite DB is a projection rebuilt from `.endless/db-ledger/*.jsonl`. Allocation that reads only the live projection will re-free ids across a rebuild unless the rebuild replays each removal as a retained, `removed=1` row. Any scheme that mints from local state must therefore mint against the append-only record's high-water mark, not against `MAX(id)` over live rows. (Coordination-based schemes #3 and #4 sidestep this — the dispenser is itself the high-water mark.)

# Cross-links

- E-816 is the concrete content-hash option.
- E-800 is the pluggable task-backend feature; the central dispenser is a subset of it.
- E-1032 (E-NNN to ET-NNN prefix migration) and E-1123 (session-worktree branch collisions) are adjacent ID concerns to keep consistent with whatever is chosen.
- E-1926 settled the no-reuse policy and the removed-flag mechanism that enforces it locally (removal keeps the row and sets `removed=1` rather than issuing a DELETE); it is the source of the two constraints above.

# Settled adjacent question: no cross-entity unification

E-1926 considered and rejected a universal id space in which tasks, sessions and decisions all draw from one sequence (so `12345` would be unambiguous and `E-12345` would preclude `ES-12345`). Rejected because (a) external providers cannot honor it — JIRA namespaces per project, GitHub shares one sequence between issues and PRs only — so the guarantee would evaporate exactly where the pluggable-provider roadmap leads; (b) sessions are the highest-volume entity and would inflate the scarce short-id space that both task and session ids are typed from by hand; and (c) ids are already embedded in immutable artifacts (commit subjects, branch names, worktree and sandbox paths, and the prose of every plan and ledger entry), so renumbering would make history lie. Per-entity-type id spaces stay, and the `E-`/`ES-`/`ED-` prefix remains part of the identity rather than display sugar.

# Output

A recommended identifier scheme with enough detail to file the implementation task(s) that unblock the two-developer milestone.
