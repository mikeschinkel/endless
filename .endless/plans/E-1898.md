
Task Detail
───────────
ID:         E-1898
Title:      Stop the dead-pane reaper from nulling live pane bindings project-wide
Project:    endless
Type:       todo
Phase:      urgent
Status:     revisit
Created:    2026-08-06 12:11 am by ES-1026 (E-1851)
Updated:    2026-08-13 6:36 pm
Landed:     2026-08-13 5:40 pm  7746e8f
Analysis:   12944 chars (--analysis to display)
This task:
- Relates to:     E-1900 [obsolete]
- Relates to:     E-1930 [obsolete]
- Cleaned up by:  E-1936 [unplanned]
- Cleaned up by:  E-1941 [assumed]
- Cleaned up by:  E-1974 [obsolete]
Touched by:
- Revisited:      ES-1034 (E-1898) [idle]
- Revisited:      ES-1046 (E-1859) [idle]
- Revisited:      ES-1054 (E-1926) [ended]
- Surfaced:       ES-1026 (E-1851) [ended]

— Description —
One root cause, one visibility gap. ROOT: `ReapDeadTmuxPanes` NULLs `process` for panes it cannot see, deciding that from an ambient $TMUX it never validates — so a caller under a foreign tmux server nulls every session row in the project, and TouchSession's COALESCE (correct, added by E-1426) is left with nothing to protect; rows then read alive with no pane, forever. Observed 2026-08-05: 27 orphaned panes, 59 of 61 status lines blank. VISIBILITY: the status line collapses every failure into one placeholder, so it ran for hours untraced, and a not-ended row with no process is incoherent and must fail loudly. Umbrella: absorbs E-1895, E-1468, E-1408 (stale) and E-1642 (idle-timeout must fill the gap that guarding the reaper opens).

— Text —

Task Detail
───────────
ID:         E-1898
Title:      Stop the dead-pane reaper from nulling live pane bindings project-wide
Project:    endless
Type:       todo
Phase:      urgent
Status:     unverified
Created:    2026-08-06 12:11 am by ES-1026 (E-1851)
Updated:    2026-08-10 7:20 am
Analysis:   11134 chars (--analysis to display)
This task:
- Relates to:     E-1900 [obsolete]
- Relates to:     E-1930 [obsolete]
- Cleaned up by:  E-1936 [unplanned]
- Cleaned up by:  E-1941 [submitted]
Touched by:
- Revisited:      ES-1034 (E-1898) [idle]
- Revisited:      ES-1054 (E-1926) [ended]
- Surfaced:       ES-1026 (E-1851) [idle]

— Description —
One root cause, one visibility gap. ROOT: `ReapDeadTmuxPanes` NULLs `process` for panes it cannot see, deciding that from an ambient $TMUX it never validates — so a caller under a foreign tmux server nulls every session row in the project, and TouchSession's COALESCE (correct, added by E-1426) is left with nothing to protect; rows then read alive with no pane, forever. Observed 2026-08-05: 27 orphaned panes, 59 of 61 status lines blank. VISIBILITY: the status line collapses every failure into one placeholder, so it ran for hours untraced, and a not-ended row with no process is incoherent and must fail loudly. Umbrella: absorbs E-1895, E-1468, E-1408 (stale) and E-1642 (idle-timeout must fill the gap that guarding the reaper opens).

— Text —
# E-1898 — liveness is observed, never inferred

## The one-sentence change

Delete the reaper. Give every pane binding an identity that is unique across
tmux server lifetimes, observe liveness into a temp table per unit of work, and
JOIN. No code path may conclude a session is dead by looking at tmux and then
writing that conclusion to the `sessions` table.

## Why the previous plan was replaced

The prior plan guarded the destructive write with an escalating series of gates
(server-uuid gate, peer-server enumeration, ordering guarantees, forced
override). Every gate was an inference about whether the pane set in front of
the process was the right one to judge by, because no row recorded which tmux
server it was bound to. Stacking gates narrows the window; it cannot close it,
and each gate added edge cases and un-mockable tmux tests.

Recording the binding's server identity removes the need for every one of those
gates. This plan is shorter than the one it replaces because the complexity was
compensation for a missing column, not essential difficulty.

## Invariants — these are the acceptance criteria

**I1 — No inferred destructive write.** No code path writes `state='ended'`, or
clears a pane binding, based on an observation of tmux. Only reported facts (a
`SessionEnd` hook) and explicit operator action close a session. There is no
sweep, no reaper, no mass UPDATE.

**I2 — Bounded blast radius.** A failed or incomplete observation may affect at
most the rows it actually observed. `unknown` is NEVER converted to `dead` by
any consumer. An unreachable server costs zero rows.

**I3 — Identity unique across server lifetimes.** A tmux pane id reused by a new
server can never match a binding made against the old one.

**I4 — Tests need no tmux server.** Liveness is a JOIN against a table; tests
seed that table directly. No private tmux servers, no socket enumeration, no
seam counters.

Verification drives each invariant explicitly. I1 and I2 are the ones this task
exists for; a change that satisfies I3 but not I1 has not fixed anything.

## Model

Multiplexer-agnostic. tmux is one populator; cmux and herdr add their own
without touching consumers.

```
process_kinds(id, slug)          -- tmux | pid | ...   (mirrors session_kinds)

processes(                       -- DURABLE identity. A fact, kept forever.
  id            INTEGER PRIMARY KEY,
  kind_id       INTEGER NOT NULL REFERENCES process_kinds(id),
  server_uuid   TEXT,            -- tmux @server_uuid; NULL for kind=pid
  address       TEXT NOT NULL,   -- tmux pane id '%414'; or the pid as text
  first_seen_at TEXT NOT NULL,
  last_seen_at  TEXT NOT NULL,
  UNIQUE(kind_id, server_uuid, address)
)

sessions.process_id -> processes.id     -- replaces sessions.process

TEMP live_processes(kind_id, server_uuid, address, command)
TEMP observed_servers(kind_id, server_uuid)   -- which servers we actually reached

UNIQUE(kind_id, ifnull(server_uuid,''), address)   on live_processes
UNIQUE(kind_id, ifnull(server_uuid,''))            on observed_servers
```

`processes` is append-mostly: a row is created the first time a session binds to
an address on a server, and is never deleted by observation. A tmux restart
mints a new `@server_uuid`, therefore a new `processes` row, therefore I3 holds
structurally — not by a guard that could be wrong.

## The identity rule applies to the OBSERVATION tables too

Added 2026-08-13, after this shipped without it.

`processes_identity` indexes (kind_id, ifnull(server_uuid,''), address), and the
plan explains why the ifnull() is required: SQLite treats every NULL as
distinct, so a plain UNIQUE would not bind. That reasoning was written down for
the durable table and then not applied to the observation tables one section
below, which expressed the same identity and enforced nothing.

It was not theoretical. `tmux list-panes -a` lists a LINKED window's panes once
per tmux session the window is linked into — measured on the real machine hours
after land, 442 rows for 245 distinct panes, every pane exactly twice. Since
session_liveness LEFT JOINs live_processes, a pane recorded N times multiplied
its session's row N times: `list-live` returned 114 rows for 57 sessions, and
`esu` refused with "Multiple sibling Claude panes in this window", blocking
worktree entry by session entirely.

Both observation tables now carry the same expression-based unique index as the
durable one, and the snapshot inserts with OR IGNORE — a repeat within one
snapshot is the same observation, not a conflict. `observed_servers` gets it too
even though only one socket answers @server_uuid today: it had the identical
gap and merely happened not to be exercised.

**The rule, stated once so it is not re-derived per table:** a table that
expresses this identity enforces it, durable or temporary.

## Liveness — three-valued, derived at read

```sql
CASE
  WHEN o.server_uuid IS NULL  THEN 'unknown'  -- server not reached
  WHEN l.address IS NULL      THEN 'dead'     -- reached; address absent
  ELSE                             'live'
END
FROM sessions s
JOIN processes p             ON p.id = s.process_id
LEFT JOIN observed_servers o USING (kind_id, server_uuid)
LEFT JOIN live_processes  l  USING (kind_id, server_uuid, address)
```

Expose as a view (`session_liveness`) so no consumer re-derives it.

Liveness is now PURELY OBSERVATIONAL: "is this address present on a server we
actually reached?" There is no remaining inference in it. That is the property
that makes I1 and I2 checkable rather than argued.

`unknown` is the safe branch: render last-known, condemn nothing. Per I2, no
consumer may map it to `dead`. The site that needs care is the spawn/claim
ownership guard — under `unknown` it must assume STILL OWNED and refuse. A
wrong refusal is an annoyance; the inverse steals a live worktree.

### Why the shell test is NOT part of liveness

The previous plan's death test was "address absent OR the pane runs a known
shell", to catch a killed harness whose pane survives. It is dropped from
liveness, because it is an inference and it can be wrong about a LIVE session:

- Verified 2026-08-09: a live Claude pane reports its version string
  (`2.1.220`) as `pane_current_command`, even mid-Bash-tool-call. So it does
  not collide with the shell set during normal operation.
- But **Ctrl+Z** puts the shell back in the foreground. A suspended-but-alive
  Claude session's pane reports `zsh`. Under the shell test that session reads
  `dead`, which drops its status line to the placeholder and — the part that
  matters — lets the spawn/claim ownership guard treat its task as free and
  hand the worktree to another session.

Cost of dropping it: a harness killed with its pane still open (SIGKILL, no
SessionEnd) leaves a stale-`live` row until the pane closes. That is a wrong
read in the SAFE direction — a refusal, never a theft — and E-1807's actual
case had a dead pane, which address-absence still catches.

`live_processes.command` is still collected, but is used ONLY for the cosmetic
"pane is running Claude but has no session row" status-line hint. An inference
is acceptable for a hint that misleads nobody and owns nothing; it is not
acceptable for liveness. That line is the rule, and the code must not blur it.

## Snapshot population

Refresh at the start of each **unit of work** — a CLI/hook invocation, an HTTP
request, a monitor repaint — inside the transaction that reads it, so a single
statement never sees two different observations.

For tmux: list sockets in `dirname $(tmux display-message -p '#{socket_path}')`;
for each, read `@server_uuid` and, when present, one
`tmux -S <sock> list-panes -a -F '#{pane_id} #{pane_current_command} #{pane_pid}'`.
Each successfully-read server contributes a row to `observed_servers`.

Enumeration stays, but is no longer load-bearing for safety: a server we cannot
reach (`tmux -S` outside the socket dir, relocated `TMUX_TMPDIR`) yields
`unknown` for its rows, not a verdict. This is the entire difference from the
previous plan's peer-enumeration gate, which had to be complete to be safe.

## Binding: where server_uuid comes from

The hook runs inside the pane. It reads `@server_uuid`; if absent it generates
and stamps one, exactly as `tmux init` does. Every binding therefore carries a
real server identity, and the previous plan's "refuse when uninitialized" gate
is unnecessary — identity is always present rather than sometimes checked.

## Deleted

- `monitor.ReapDeadTmuxPanes` and `internal/monitor/reap.go`, including the
  `len(alive) == 0` mass-UPDATE that ended every tmux row in one statement.
- `runInit`'s `runReset(nil)` call — the documented trigger for the 37-row batch.
- `endless tmux reset` ENTIRELY — the Go verb (`internal/tmuxcmd/reset.go`,
  its `tmux.go` dispatch), the Python wrappers (`cli.py` `tmux_reset`,
  `tmux_cmd.py` `run_reset`), the remedy line in `task_cmd.py:3304` that tells
  users to run it, and the assertion on that line in
  `tests/test_check_task_ownership.py:91`. No read-only replacement is
  introduced: `endless session list` and `endless sql` already read the view,
  and the ownership guard can no longer refuse over a gone pane — the snapshot
  shows it gone — so the remedy it advertised has nothing left to remedy.
- `TouchSession`'s collision invalidation. A reused pane is a different
  `processes` row, so the pane-reuse case cannot arise; a genuine same-server
  collision leaves both rows and the reader orders by `last_activity`.
- From the previous plan: D1, D2, D5, D6, D9 and their tests. D3's insight is
  kept as a read-time classifier; D7 and D8 survive as below.

## Kept from the previous plan

- **D7** — `runInit` no longer reaps (now: there is nothing to reap).
- **D8** — `runStatusLine` records a fault instead of silently rendering the
  placeholder on error. One correction to that decision: because
  `faults.Bind` is wired to `monitor.DB` (main.go:144) and `faults.Record`
  swallows its own DB failure by contract, this covers the non-DB fail-closed
  exits, not all of them. Worth doing; do not claim full coverage.
- The **ledger-safety assertion** in the verify suite.

## Migration

1. Create `process_kinds`, `processes`, add `sessions.process_id`.
2. Backfill, in TWO steps.

   2a. One `processes` row per distinct non-null `sessions.process`, with
   `server_uuid = NULL` (the binding server is not knowable retroactively) and
   `kind` derived from the string shape (`%N` -> tmux, `pid:N` -> pid).

   2b. ADOPT what is observable: for every pane live on the running tmux
   server, repoint the non-ended session bound to that address onto that
   server (`monitor.AdoptPaneBindings`).

   **Step 2b was missing from the first cut, and its absence took the board
   down on 2026-08-10.** This plan claimed the NULL-server rows would
   "self-heal on the session's next hook" and that live sessions would
   "recover within seconds without any repair verb". That is true only for
   sessions that FIRE hooks. An idle window fires none, and idle is the normal
   state, so every pre-existing session became unresolvable at once and STAYED
   unresolvable — the pane lookup matches on (current server uuid, address),
   and a NULL-server row matches no server at all. Measured on the real ledger
   when it bit: 64 sessions bound, 1 healed (the one firing hooks), 63 dark.

   Adoption is an observation, not a repair heuristic: a pane in the live set
   exists on the named server NOW. Two guards keep it honest — it refuses any
   address claimed by more than one non-ended session (that ambiguity is
   exactly what E-1898 exists to stop resolving by guesswork), and it creates
   a NEW row rather than stamping a server onto the shared NULL-server record,
   which the backfill deduplicates by address and which therefore carries
   history from every server that ever issued that pane.

   Panes that are not live, and every binding when no tmux server is
   reachable, keep `server_uuid` NULL and read `unknown` — never `dead`. On
   the measured ledger that left exactly one row: a genuine leftover from a
   dead server, which is precisely the case where `unknown` is the true
   answer. No repair verb is needed, but the reason is adoption, not healing.
3. Convert the ~35 call sites comparing `process` to a raw pane id. The type
   changes, so a missed site is a COMPILE ERROR — the property that made this
   safe to do where string-encoding `<uuid>:<pane>` would not have been.
4. Drop `sessions.process`.

`channels` is explicitly OUT OF SCOPE and keeps its string key; see E-1936.
It becomes the last consumer of the old key, deliberately, pending that
brainstorm's decision on whether it should exist at all.

## Tests — `tests/tasks/e-1898-verify.sh`

**Layer A — liveness truth table (FAIL-FAST, pure SQL fixtures, no tmux).**
Includes duplicate-observation coverage: the same address seeded twice must
still yield one row per session, and likewise a repeated server.

These INSERT directly rather than going through `SetTestTmuxObservation`, and
that is load-bearing. The seam takes a `map[string]string` of address ->
command, which cannot hold one address twice, so every fixture built on it was
structurally incapable of reproducing the defect. The seam that makes liveness
exhaustively testable without tmux is the same seam that hid this. A test
written through it would restate the blind spot instead of closing it.

Seed `processes`, `sessions`, `live_processes`, `observed_servers` directly and
assert every cell: server unobserved -> `unknown`; observed + address present ->
`live`; observed + address absent -> `dead`. Same pane id under two different
`server_uuid`s resolves independently (I3). Assert a pane running a shell reads
`live` while its session row stands — pinning that the shell inference is NOT in
the liveness path and cannot be reintroduced without failing this test.

**Layer B — invariants as tests, not as prose.**
- **I1:** grep the tree for any write of `state='ended'` or a binding clear
  outside the SessionEnd path; assert zero. Then drive a full hook cycle against
  an EMPTY `live_processes` (the exact 2026-08-05 shape: observer sees nothing)
  and assert **zero rows changed**. This is the regression test for the incident.
- **I2:** with `observed_servers` empty, assert no row reads `dead` and the
  ownership guard refuses rather than freeing.
- **I4:** the whole of A and B runs with `tmux` absent from `$PATH`.

**Layer C — one E2E** against a private tmux server (`-f /dev/null`, throwaway
`XDG_CONFIG_HOME`) proving the populator fills the snapshot from a real server.
Exactly one, because the logic under test is now SQL, not tmux plumbing.

**Layer D — project-wide regression:** `go test ./...`, `just test`, `just build`.

**Ledger-safety assertion (mandatory).** Count non-ended sessions holding a
binding in the REAL database before and after the suite; they must be equal.

## Out of scope

- `channels` and the `endless-channel` MCP server (E-1936).
- Idle-timeout / session-duration truthfulness. Positive observation covers
  "Claude died, pane lives" without inferring death from silence.
- Non-tmux (`pid:`) liveness. The model has a slot for it; no populator ships here.
- Repairing rows damaged on 2026-08-05. Repaired by hand; migration step 2
  heals the rest on the next hook.




