ED-1547 reserves two words, and the distinction is load-bearing rather than
stylistic. A covered entity — one whose id something outside its own row refers
to — is **removed**: the row stays, `removed = 1` is set, the id is never
re-minted. A pure row handle, a surrogate primary key nobody stores or types, is
**deleted**: the row is gone and its id is free. Reading `removed` in the ledger
should tell you the id survived; reading `deleted` should tell you nothing did.

`tasks` is a covered entity and has been soft-removed since E-1929. The command
says so — `endless task remove`. The event it emits says the opposite:
`task.deleted`. So the one durable record of what happened describes a hard
delete that did not occur, on the entity whose id retention ED-1547 exists to
guarantee.

## The mechanism is already in the codebase

`events.RetiredKinds` (internal/events/event.go) exists for exactly this, and
its comment says so: "a retired one that only replay will ever see". It holds
four kinds today. The change is to add `task.removed` to `ValidKinds`, move
`task.deleted` to `RetiredKinds`, and have the executor and projector dispatch
both spellings into the one removal function. No history is rewritten — the
ledger keeps both spellings forever, and only the new one is ever emitted again.

## Secondary, and cosmetic

`session_tasks.removed` is the same contradiction running the other way: a
junction row with a surrogate id, genuinely hard-deleted, named "removed". By
ED-1547's own words — "deleting a junction-table row is unrestricted" — it
should be `session_tasks.deleted`. It is the same two maps in the same file, so
it is nearly free to do here, but it is cosmetic: no id guarantee rides on it
and no reader is misled about data that survived. Drop it if it is not worth the
churn.

## Why it is not folded into E-2010

E-2010 renames `decision.deleted` to `decision.removed`, because it is already
rewriting that handler and adding that column, and shipping new retention
behavior under a name that says "deleted" would bake the contradiction in
deeper. The task rename is different: it is a pure rename of shipped, working
behavior that touches nothing else E-2010 touches, and a ledger-compatibility
change deserves review on its own rather than riding inside a data-loss fix.

Conflicts with E-2010 rather than following it: both edit `ValidKinds` and
`RetiredKinds` in the same file, so they must not be in flight together, but
neither needs to come first.
