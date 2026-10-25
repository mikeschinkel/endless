Settled 2026-10-25 by Mike: add a dir field directly on [[check]], naming the directory the check runs in, honoured by every driver (sh checks included). It means the same for every runner, so under ED-1635 it belongs directly on [[check]].

Considered and set aside: a runner-specific field such as module on gotest. Under ED-1635 a field whose meaning differs by runner lives in a [check.<runner>] sub-table (e.g. [check.gotest]); dir covers this need without one.

ED-1635 postdates the existing tests and paths keys, which only structured drivers accept. Whether to move them under [check.<runner>] is not part of this task.
