Candidate fixes: re-exec the CLI immediately after the Step 5 ff-merge so all post-merge work runs on the new source; or run the record step as a subprocess. Either way the invariant to establish is that no post-merge code path shares a process with pre-merge imports.

Note the failure is order-dependent and so will not reproduce reliably: it fires only when the renamed symbol is referenced by a module that had NOT yet been imported when the merge happened. On E-2011 that module was endless.event_bridge, reached through the landing-record emit.

## From the description

self_dev-only (a downstream land does not replace endless's own code), but every self_dev land of a Python rename is exposed.
