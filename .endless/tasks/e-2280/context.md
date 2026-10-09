Agents finish a task, hand over the verify, and only then list where the work
differs from the approved plan. Mike starts the verify and then learns it was
premature, because changes are still needed. Raising differences at handoff
also signals that the work is not done. The only place a handoff asks the agent
to report anything is its final message, which comes after the
implementation. The discovery guidance ("do it, note it, say so in your
reply") explicitly asks for scope growth to be reported after the fact.

Agents mostly do not notice a deviation while working: a different route feels
like ordinary engineering and only looks like a deviation when the diff is read
against the plan. So the fix is not "raise it before implementing". It is a
reconciliation after implementing and before any verify is handed over, which
the tooling enforces.
