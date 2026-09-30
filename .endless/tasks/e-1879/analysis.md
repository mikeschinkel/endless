Project notes are not that destination -- they are project-scoped, have found no good use since being added, and are likely to be deprecated.

task-scoped, append-only, write-only by design, and hidden from the default `task show` -- surfaced only under `task show --annotations`, so the sink never relocates noise to where the user already reads.

Neither half works alone: a sink without the gate yields annotate-and-narrate, giving the user a third place to read, while the gate without a sink forces the agent to either drop genuine observations or smuggle them into prose.
