E-1956 made a supersession render inline with the status on three surfaces: `task show`, `task list` and `session status`.

Confirmed present before E-1891's changes, so this is a pre-existing regression from later work, not a new break. Found because E-1891 fixed two stale greps in tests/tasks/e-1956-verify.sh that were masking the check behind an earlier fail-fast.
