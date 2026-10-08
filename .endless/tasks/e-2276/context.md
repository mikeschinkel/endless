Mike's report, from the E-2225 session: agents consistently finish a task, hand
over the verify or land command, and only then list choices they made that
differ from the plan or that they left open. That order is backwards. A
decision that differs from the approved plan should be raised before it is
implemented, and one discovered mid-implementation should stop the work until
Mike approves it. Raising choices at handoff also signals the work is not done.
Mike suspects the handoffs and/or the plans are structured so that this
happens.

## What happened on E-2225

After handing over `endless task verify E-2225`, the agent listed:
- the landing rule was moved into the shared handoff partial instead of
  replacing the line in each wrapper, as the plan said;
- a test-file rename it had been blocked from making;
- identifiers it chose not to rename, and an extra field rename beyond the
  table Mike approved;
- hookcmd names left with "owner" in them, which Mike then had fixed.

The correction is recorded as a lesson ("Raise plan deviations before
implementing, not after handoff").

## Things in today's handoff that may push this way

- The discovery bullet says "Could it reasonably be done now? Do it — note it
  in the commit message, record the grown scope … and say so in your reply
  draft." It asks for the scope change to be reported afterwards, not approved
  before.
- The only point where a handoff asks the agent to report choices is the final
  message, which comes after the implementation is finished.
- "If anything is ambiguous … STOP and ask" covers ambiguity, not a deliberate
  choice the agent considers obvious.

## A related idea from planning E-2261

Mike wants a verify requested only once every deviation from the plan has been
approved and implemented. Endless already refuses to spawn a task with open
questions; the same check could refuse an agent's request for a handoff verify
while the task has open questions, so the rule is enforced rather than
remembered.

Mike said he had more to add that he could not recall when this was filed.
