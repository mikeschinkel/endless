Settled 2026-10-25 by Mike: add a dir field directly on [[check]], naming the directory the check runs in, honoured by every runner. It means the same for every runner, so under ED-1635 (accepted) it belongs directly on [[check]].

Considered and set aside: a runner-specific field such as module on gotest. Under ED-1635 a field whose meaning differs by runner lives in a [check.<runner>] sub-table; dir covers this need without one.

Decided without a question (2026-10-29), each having no cost against its alternative:
- dir is relative to the repo root, the directory checks already run from; relative to the suite's own directory every value would start with ../../...
- A dir outside the root, absolute, or missing is refused at manifest validation rather than skipped, so a typo cannot pass as a clean run.
- paths are relative to dir, because the runner's process runs there.
- pytest suites are prefixed with dir because pytest names files relative to its own root, so two subprojects collide in the merged report; Go package names are already unique.
- Setup and teardown stay at the root: they are project-wide, not per check.

ED-1635 postdates the existing tests and paths keys, which only structured runners accept. Moving them under [check.<runner>] is not part of this task.
