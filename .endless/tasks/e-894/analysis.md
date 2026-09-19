Absorbed E-1481, E-1482 and E-1484 on 2026-08-25 (ED-1550). They were phase decompositions of a plan this task already carries in full — its own text sets 'Decoupling scope: full (Phases 1-3)' and describes each phase, including 'Phase 3 is now pure deletion of Python's bootstrap'.

  E-1481 = Phase 1, Go read subcommands returning task-display data as JSON.
  E-1482 = Phase 2, query_bridge.py routing every Python task-display read
           through them, with byte-for-byte output parity.
  E-1484 = Phase 3, removing Python's schema bootstrap so Go owns DB creation;
           subsumes the E-1116 empty-DB crash.

Each child carried its own 2-3KB plan refining its phase. Those are NOT lost:
an obsoleted task keeps its text, so 'endless task show E-1481 --text' still
returns the Phase 1 detail. Read them alongside this plan at pickup rather than
treating this task's text as the only source.

Splitting into three landable units is still available to the implementor — one
task does not mean one commit. It just no longer costs three backlog rows to
keep the option open.