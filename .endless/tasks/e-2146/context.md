E-1956's gate refuses `obsolete` on shipped work while `declined` is reachable FROM a shipped status.

Three failures follow: the gate's remedy `task replace <old> --by <new>` has no id to name when work is DELETED rather than superseded (E-2142 removes E-1434/E-1437, both `assumed` with real landings); `declined` as the workaround writes a false record, asserting nobody decided to do work that was built and landed; and the rule is unenforced on epics, whose status is derived in Go and bypasses the Python gate, so E-1421 -- landed twice -- is already `obsolete`.
