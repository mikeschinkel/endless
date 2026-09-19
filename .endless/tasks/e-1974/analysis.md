# Analysis

## Symptom

`esu` refuses to resolve with "Multiple sibling Claude panes in this window",
listing the SAME pane twice — identical session id, pane, uuid and worktree.
`session-query list-live` returns 114 rows for 57 sessions. Entering a worktree
by session is blocked.

## Measured

    tmux list-panes -a -F '#{pane_id}' | wc -l        -> 442
    tmux list-panes -a -F '#{pane_id}' | sort -u | wc -l -> 245

Every pane appears exactly twice. `tmux list-panes -a` lists a LINKED window's
panes once per tmux session the window is linked into; nothing in the output
marks the repeat.

## Root cause

`monitor.refreshLiveness` inserts one `live_processes` row per pane returned,
and that TEMP table declares no uniqueness:

    CREATE TEMP TABLE IF NOT EXISTS live_processes (
        kind_id INTEGER NOT NULL, server_uuid TEXT,
        address TEXT NOT NULL, command TEXT)

`session_liveness` LEFT JOINs it on (kind_id, server_uuid, address), so a pane
recorded N times multiplies its session's row N times. Every consumer of the
view inherits the multiplication.

Not the servers: only one socket answers `show-options -gv @server_uuid`, so
`observed_servers` holds one row. The multiplier is purely the pane snapshot.

## Why it was missed

E-1898 states this exact identity for the DURABLE table and explains why the
plain form is insufficient — `processes_identity` indexes
(kind_id, ifnull(server_uuid,''), address) precisely because SQLite treats each
NULL as distinct. The observation table expresses the same identity and does not
enforce it. The reasoning was written down and then not applied one table over.

The tests could not catch it: every fixture seeds `live_processes` through
`SetTestTmuxObservation`, which builds panes from a Go map — inherently unique.
No test ever presented a duplicate address, because no test could.

## Fix

Give `live_processes` the same expression-based unique index as the durable
table, and insert with OR IGNORE:

    CREATE UNIQUE INDEX IF NOT EXISTS live_processes_identity
        ON live_processes (kind_id, ifnull(server_uuid,''), address)

A pane seen twice in one snapshot is one pane; the index says so in the same
shape as `processes_identity`, which also covers any future populator (cmux,
herdr) rather than just the tmux path. Deduping in `parsePaneList` alone would
fix today's symptom and leave the invariant unstated.

Apply the same index to `observed_servers` on (kind_id, ifnull(server_uuid,''))
for the same reason — cheap, and removes the sibling failure mode before it
appears.

## Regression test

Seed `live_processes` with the SAME address twice and assert `session_liveness`
still yields exactly one row per session. It must bypass
`SetTestTmuxObservation`'s map (which cannot express a duplicate) and insert
directly, or the test reproduces the blind spot rather than closing it.
