Settled 2026-10-25 by Mike: add a dir field directly on [[check]], naming the directory the check runs in, honoured by every driver (sh checks included). It means the same for every runner, so it belongs on [[check]] under the decision on runner-specific check fields.

Considered and set aside: a runner-specific field such as module on gotest. Under that decision, a field meaning differs by runner would live in a [check.<runner>] sub-table (e.g. [check.gotest]); dir covers the need without one.
