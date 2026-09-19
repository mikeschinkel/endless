## What makes a docs task auto-landable

Not "docs are low risk" — that framing does not survive contact with the first
real case. E-2144 corrects the `obsolete` gloss, and that wording lives in a Go
transition table whose output the guide and `docs/status-lifecycle.mmd` are both
generated from. A rule keyed on "only touches `*.md`" would exclude the very task
that motivated the type.

The property that actually holds is CHEAP REVERSAL: a docs task's deliverable is
content whose correctness is judged after it lands, because being wrong costs a
follow-up correction rather than a broken system. That is what licenses skipping
the second human step, and it is per-task judgement, not a file-type test.

## Why the human step being skipped is the right one

The tempting framing — "approval to `ready` IS the review, because the human read
the wording" — was considered and rejected by Mike on the evidence:

- Descriptions must not carry replacement text. They are a blurb saying WHAT a
  task is for, and are being SHRUNK (to 256-384 chars) precisely because agents
  use them as mini-plans.
- In practice the user does not read most agent-written content before it lands;
  they discover it is wrong later and correct it. A design premised on a read
  that does not happen is a fiction.
- Agents routinely leave open questions in a plan rather than the final wording,
  so there is often no text to have approved.

So the type does not claim the content was reviewed. It claims the content is
cheap to fix if wrong.

Once E-1531 lands, a docs task carries `old_text` and `new_text` fields holding
the exact wording. Until then the plan field carries it.

## The gates it must not auto-land past

Auto-landing agent-written prose is the same closed loop that produced the defect
E-2144 fixes: an agent wrote a definition, it became the guide, and an agent later
cited the guide back as authority for the agent's own behaviour. The brakes are
the checks that already exist, and the type must refuse to land when any fails:

- `just lifecycle-check` and `just guide-check` — generated docs silently drift
  from their source otherwise, which is exactly how the `obsolete` gloss reached
  five transition labels.
- The Python doc-sync suites.
- The task's own verify suite. If the change touched code — and a docs task may —
  a suite is required, on the same terms as any other task.

A docs task that cannot pass those is not a docs task; it is an ordinary one that
was misfiled.

## Open for the planning pass

Whether `ready` is reached by the normal approve step or whether a docs task
auto-advances from `submitted`. Auto-advancing removes the last human touch
entirely, which is either the point or a step too far — decide it with Mike, not
by inference.
