# Provenance — what was observed in the E-1845 session

Kept deliberately short: this field records only how the scope extension was
discovered. The specification, decisions, and deliverables live in the plan, and
are not restated here.

During a single task (E-1845, adding the `untriaged` status), the session filed
four new tasks:

- **E-1888** and **E-1891** — filed as separate bugs, both about a status list
  that had drifted from `cli.TASK_STATUSES`. Mike identified them as two
  symptoms of one defect; E-1888 was later absorbed into E-1891, which was
  re-scoped to the root cause.
- **E-1894** — filed to add CI so per-task verify scripts could not "rot
  silently". Retired as a category error: a per-task acceptance harness becoming
  invalid after its task lands is its expected end state, not a defect.
- **E-1899** — remove `task list --tree`, filed at Mike's explicit invitation.

So of four filings, one was sound, one was over-fragmentation the user had to
clean up, one rested on a false premise, and one was requested.

The agent's behavior was not a misreading of its instructions. The handoff
template told it to file discoveries and explicitly forbade fixing them inline.
That line is deliverable A of this task.
