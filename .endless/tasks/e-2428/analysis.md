## Why aliases and not rewriting rows

Rewriting `processes.server_uuid` from the old ID to the new one would fix
today's case, but loses the ID each session was actually bound under, and does
nothing for a later re-stamp. Reusing the old ID at stamp time does not fix
today's case: the server already carries 0ae20eaa, and switching back would
make every session bound since 2026-10-19 read dead.

## Why the start time decides

A tmux server reuses pane ids (`%22`) across restarts, so an ID stamped on an
earlier server must stay a different identity: that is what E-2300's proof
relies on. `#{start_time}` is when the running server process started. An ID
first seen at or after it can only have been stamped on this process. Both
times come from this machine's clock.

## Evidence

The running server started 2026-10-17 00:12:57Z. Its `@server_uuid` changed from
a6d29860 to 0ae20eaa at about 2026-10-19 03:57Z, right after a read failed with
"no server running". On 2026-10-24 ES-1339 (E-2206) was still running in pane
%22 and read `tmux:dead`. Checked on tmux 3.8: `show-options -gv @unset` exits 1
with "invalid option"; `-gqv @unset` exits 0 with no output; `-gqv` against an
unreachable socket exits 1.
