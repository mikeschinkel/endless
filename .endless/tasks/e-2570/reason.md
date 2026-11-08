Not needed. Three findings retire it.

Sessions are not ledgered: every session.* string across all 70 db-ledger files
sits inside task prose, never in a kind field, so no session event has ever been
written and a rebuild never replays a session. That removes the argument that
made the shared removal function necessary for tasks (E-1929) and decisions
(E-2010) — there, the projector would otherwise re-free an id on replay.
Sessions have no replay to be wrong about.

The one named consumer already protects itself. This task's context rested on
E-2486's `correct session delete` being the first path that could remove a
session. E-2486 landed, and its delete refuses when anything refers to the row,
counting PRAGMA foreign_key_list plus the undeclaredRefs list in
internal/events/correction.go. So the only session that can be deleted is one
nothing refers to — and ED-1547's harm is precisely "an id something outside the
row refers to". No referrers, no harm.

And sessions have no workflow that makes that a coincidence. Tasks and decisions
each produce a row that is referenced AND legitimately removable: you remove a
task that is still in someone's session_tasks. That is why they needed the
column. A session is never deliberately discarded while something still points
at it; an abandoned session has no referrers by construction, which is the case
the guard permits and ED-1547 does not care about.

Sessions are infrastructure, not records a user manages. They are not ledgered,
so there is no bloat concern, and they move to the machine database rather than
the project databases, so they do not proliferate. Nothing in the tree deletes a
session, and that is the design rather than a gap.

WHAT THE REVIEW DID TURN UP, filed separately

undeclaredRefs is missing six INTEGER session references, so a correcting delete
counts no referrer for them:

    error_triage.session_id
    error_triage.accepted_session_id
    error_triage.declined_session_id
    errors.session_id
    errors_sources.session_id
    session_notices.changed_by_session

Found by querying the live schema for every INTEGER column whose name mentions
session that has no PRAGMA foreign_key_list entry pointing at sessions, then
checking each against the list. An earlier text-parsing pass got this wrong in
both directions — it invented jobs.session_id, which does not exist, and missed
errors.session_id and errors_sources.session_id — because a table altered by ADD
COLUMN ends with `, col TYPE);` on the closing line, which breaks a regex
anchored on a newline before the paren. The pragma join is the reliable form and
is what the missing test should use.

There is no test pinning undeclaredRefs against the schema, which is the hole
E-2010 closed for projectReferences with
TestProjectReferences_CoversEverySchemaReference.
