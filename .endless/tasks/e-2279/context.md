Task content in Endless lives in a few long-form text fields: `context`,
`analysis`, `plan`, `outcome`, plus less-used ones such as `notes`. In this
project almost every plan was written by a Claude agent, not a person, so what
the fields hold reflects what agents put there. Mike's view is that each field,
`plan` above all, is being used for several distinct jobs at once, and that
those jobs could be identified and given their own structure.

A concrete need makes this urgent. The brainstorm on reconciling plan
deviations before verify concluded that, before an agent hands over a verify,
it must reconcile its finished work against the approved plan item by item.
Each item is marked done as planned, done differently, not done, or blocked,
and work outside the plan is listed as extra. That gate can only be
deterministic if plan items are structured data with identities. A check that
a free-text plan "has numbered items" tests formatting, not itemization: it
accepts numbered prose and refuses a cleanly itemized bulleted plan. The
implementation of that reconciliation is blocked on this research so that it
builds on the content model this research proposes, rather than an interim
text format that would soon be replaced.
