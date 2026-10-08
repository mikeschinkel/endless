Decisions (Mike, 2026-10-07): Q1 C, Q2 C (one incident per fingerprint, plus how broadly it was raised), Q3 B, Q4 C, Q5 A, Q6 A.

1. Who raised it — resolution (internal/faults, wired in cmd/endless-go like the project resolver).
   - Session: resolved through events.EmittingActor, and kept only when the actor's harness is set (an agent ran the process). A fault raised from a person's shell gets no session, so it is never routed to an agent.
   - Task: explicit from the producer if given; else the resolved session's sessions.task_id; else the task of the worktree the process runs in (.endless/worktrees/e-NNNN).
   - Injected as a resolver func at Bind time, like resolveFaultProject, so faults keeps no dependency on monitor or events.

2. faults.Fault gains TaskID and SessionID. A producer that knows the one task its fault is about sets TaskID (and SessionID if it has one); they override the resolved values. A fault about several tasks (WARN-0031) leaves them empty and keeps naming the tasks in Fields.

3. Storage.
   - errors gains nullable task_id and session_id: the LATEST occurrence's attribution, rewritten on every upsert (the session to route to is the one that raised it most recently).
   - New table errors_sources (error_id, session_id, task_id, first_seen_at, last_seen_at, occurrences), unique on (error_id, COALESCE(session_id,0), COALESCE(task_id,0)), upserted alongside the incident in the same statement batch. One row per distinct raiser, so it grows with breadth, not frequency. Unattributed occurrences share one (NULL, NULL) row.
   - Every detail-log line carries task and session, so the full per-occurrence history stays where it is today.
   - Schema change through the project's migration path.

4. Display.
   - errors list: a BY column with the latest raiser (`ES-1299 (E-2259)`, `E-2259` when there is no session, `-` when unattributed), plus a breadth marker when more than one raiser exists (`+2`).
   - errors show: "Raised by" lines, one per errors_sources row: session, task, occurrences, first and last seen. The detail log's lines show their own task/session.

5. Tests: an agent-run fault records the session and its task; a person-run fault records no session; an explicit TaskID overrides the resolved one; the worktree fallback fills the task when there is no session; a second session raising the same fingerprint updates the latest columns and adds an errors_sources row without a second incident; a repeat from the same session only bumps its row; errors list/show render the BY column, the breadth marker and the Raised-by lines.
