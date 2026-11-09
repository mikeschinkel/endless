## Why rename the command rather than keep the kind

`session task remove` presented a genuine fork: the kind can agree with the
command that emits it, or with ED-1547's word for what the row did, but not
both. Renaming the command to `session task delete` is the only option under
which command, kind and retention all agree, and it costs nothing here —
Endless is pre-release, so there is no alias to keep and no deprecation path to
run. Leaving the kind as `session_tasks.removed` and recording why would have
preserved a contradiction permanently to avoid a rename that is free.

ED-1655 ("sessions are never deleted") does not make this moot. That decision
governs the row in `sessions`. A `session_tasks` row is a separate membership
record, and deleting one leaves the session and its id untouched; ED-1655's
reasoning rests on nothing needing to delete a session, not on history being
unalterable, so it does not reach across.

## Why one rename map rather than explicit cases per switch

The alternative was to follow E-2010's precedent and list both spellings in an
explicit case in each of the two switches. Its advantage is that a search for
the spelling an operator sees in the ledger lands on the code that handles it.
Its cost is that the alias is recorded once per switch per rename, and a
half-applied rename fails silently and catastrophically: the retired spelling
falls through to the declared no-op, replay stops removing, and a rebuild
resurrects every row ever removed.

A single `RenamedKinds` map consulted before dispatch makes that failure
unreachable rather than merely tested for — there is one place to update, and
neither switch can disagree with the other. The grep cost is paid back by the
map itself, which is where a reader looking for the old spelling now lands.

Normalization happens at dispatch only. `evt.Kind` is not rewritten, so
refusals, errors and anything that renders an event still name the spelling
that is actually in the ledger rather than a spelling that was never written.

The invariant that makes the map safe is checkable and is being checked: every
key is a retired kind (so nothing can emit it) and every value is a live kind
(so replay reaches a real handler). A key that is also in `ValidKinds`, or a
value that is not, is a rename recorded wrong.

## Risk

The rated risk is in replay, not in the rename. The ledger already holds
`task.deleted` events; if dispatch stops reaching the removal handler for them,
a rebuild returns every removed task to the live set with no error. That is why
the parity assertion under both spellings is an item rather than left to the
general test suite. The membership half has no such exposure in this project's
own ledger, but another install's may hold the old spelling, so it is retired
rather than dropped regardless.
