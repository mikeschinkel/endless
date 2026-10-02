Observed 2026-08-08 (recorded in E-1883): running `endless task update E-1629`
from the endless checkout, where E-1629 belongs to go-tealeaves, emitted an
event stamped "project":"endless" into endless' db-ledger and committed it to
endless' repo. go-tealeaves' ledger has no record of its own task changing.
Replaying either ledger is then wrong.

A command that names an entity should not need --project and must not fall
back to the cwd's project. Agreed in the E-1883 brainstorm as required under
every scoping option; under the per-project split it is mandatory, since an id
must be routed to its project's database before anything else happens.
