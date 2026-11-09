ED-1547 reserves two words, and the distinction is load-bearing rather than
stylistic. A covered entity — one whose id something outside its own row refers
to — is **removed**: the row stays, `removed = 1` is set, the id is never
re-minted. A pure row handle, a surrogate primary key nobody stores or types, is
**deleted**: the row is gone and its id is free. Reading `removed` in the ledger
should tell you the id survived; reading `deleted` should tell you nothing did.

Two live event kinds contradict that, in opposite directions.

`tasks` is a covered entity and has been soft-removed since E-1929. The command
says so — `endless task remove` — and so does the schema: `tasks.removed` and
the `live_tasks` view already exist and already carry the right word. The event
it emits says the opposite: `task.deleted`. So the one durable record of what
happened describes a hard delete that did not occur, on the entity whose id
retention ED-1547 exists to guarantee.

`session_tasks` runs the other way. It is a pure junction — surrogate
`id INTEGER PRIMARY KEY`, `UNIQUE(session_id, task_id)`, nothing stores or types
that id — and its executor issues a real `DELETE FROM session_tasks`. The kind
is `session_tasks.removed`, and the command behind it is `session task remove`,
so a reader who knows ED-1547's vocabulary infers a row survived when none did.

## The population is closed, not a sample

Every other kind that names a deletion was checked and is correctly named.
`task_dep.deleted` and `decision_relation.deleted` both really do hard-delete a
junction row with a surrogate id. `project.unregistered` and `project.purged`
claim no delete at all, and after E-2010 neither performs one. `decision.deleted`
was renamed by E-2010. So the misnamed set is exactly the two above.

## The mechanism, and why it is being generalized here

E-2010 shipped the first rename: `decision.deleted` moved to
`events.RetiredKinds` so nothing can emit it again, `decision.removed` entered
`ValidKinds`, and the executor and the projector each grew an explicit case
listing both spellings, placed ahead of the retired-kind no-op in their default
branch. That no-op is the hazard: a rename that updates one switch and not the
other turns replay of the old spelling into a declared no-op, so a rebuild stops
removing and silently resurrects every row ever removed.

E-2571 is the second and third instance of the same shape, which is the first
point at which recording a rename ONCE is cheaper than recording it per switch.

Nothing stores or queries an event kind in SQL, and no payload is selected by
kind, so the reach of a rename is the kind maps, the two dispatch switches, the
Python emit sites and the tests.

## Why it is not folded into E-2010

E-2010 renamed `decision.deleted` because it was already rewriting that handler
and adding that column, and shipping new retention behaviour under a name that
says "deleted" would have baked the contradiction in deeper. These renames are
different: they are renames of shipped, working behaviour, and a
ledger-compatibility change deserves review on its own rather than riding inside
a data-loss fix. The `conflicts_with` link to E-2010 is spent — E-2010 has
landed — and is kept only because it records why the two were separated.
