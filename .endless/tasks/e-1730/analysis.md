## Why this is blocked by E-894 (Mike, 2026-08-27)

Not for the reason the pairing suggests. The excision itself does not need the
Go port: it edits `.endless/db-ledger/*.jsonl` by hand, and the read side
(`events.ReadAllEvents` → `ProjectToTempDB`) is already Go.

What blocks it is the second half of this task's own description — "remove
them, then re-derive + validate". `endless-go event rebuild-db --confirm` is
refused on purpose (E-2062), so re-derive can only be a dry-run comparison
today. Repairing it is E-799, which Mike has sequenced behind the port of DB
access to Go because it needs his own focused attention. So this task waits on
the port by way of the rebuild, not by way of the ledger.

Scope, measured 2026-08-27 (full evidence in E-1732's outcome): 1,071 foreign
lines in the real ledger, unchanged since 2026-07-02, so nothing is still
accruing. Under ED-1527's per-line rule the split is 1,047 clearly bug-written
(11 of them occupying real ids 1034-1039), 18 clearly legitimate — node 9000's
16 lines are real 2026-04-27 rollout history, and node 86eb carries two real
E-1542 landings among its fixtures — and 6 needing a call (node abcd's five
E-1628 landings, node b745's one session status). Node-id excision is unsafe:
node 3b27 is a confirmed collision between a 2026-06 writer and sandbox e-1853,
created 2026-08-06.
