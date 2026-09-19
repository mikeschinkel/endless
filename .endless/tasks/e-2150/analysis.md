## The specimen

E-2146 was filed in error during E-2144 and folded back. I closed it with
`task replace E-2146 --by E-2144`, which recorded `replaced_by` and set a
status; Mike corrected the status to `declined`, because deciding a task should
not exist as a separate filing is an active decision, not a supersession.

The row is now right — `task show` renders `Status: declined (replaced by
E-2144)`, composing both facts. The BANNER is not:

    [Endless] task show: E-2146 is NOT authoritative — it has been replaced by
    E-2144. Read E-2144 instead.

The decline is nowhere in it. Mike's explicit decision is invisible to every
agent that reads the task, and the row is introduced as a handoff.

## Why it happens

`src/endless/authority.py:for_task(status, replaced_by, duplicates)` tests the
two relations before it tests status, and returns on the first hit:

    if replaced_by:  return Caveat(...)
    if duplicates:   return Caveat(...)
    if status == "superseded": ...
    if status == "obsolete":   ...
    if status == "declined":   ...

So a relation always wins, for every status. Verified 2026-09-15: `declined`,
`obsolete`, `superseded` and `confirmed` all produce the identical "it has been
replaced by E-2144" line when the relation is present.

## What the rule was actually for

Its docstring says: "The two relations trigger regardless of status, because
that is the point of them: `task replace` deliberately holds shipped work at the
status it earned, so a REPLACED task can read `confirmed` and still not be the
record to quote."

That reasoning is correct and must be preserved. A `confirmed` task carries no
warning of its own, so without the relation-first rule a superseded-but-shipped
task would read as current. The rule is right for the SHIPPED statuses.

It over-applies to the three abandonment statuses. `declined`, `obsolete` and
`superseded` are already non-authoritative on their own — every one of them
returns a Caveat with no relation present — AND each names a distinct reason the
row is closed. There is nothing to rescue there; the relation is additional
information, not a correction.

## Shape of the fix (not a decision, a starting point)

When the status is itself an explicit closure decision, lead with the decision
and still name the successor, e.g.

    E-2146 is NOT authoritative — it is declined: an active decision not to do
    the work, and E-2144 replaced it. Read E-2144 instead.

`Caveat` already carries `see`, so the redirection survives either ordering; the
question is only which clause leads and how the two compose into one sentence
without becoming a paragraph.

Decide also what `duplicates` does under the same rule, and whether
`for_decision` has the matching problem — it tests `superseded_by` before
`rejected`, which is the same shape.

## Why this is not E-2144's bug

The precedence predates it (E-1956 era). E-2144 touched the obsolete/superseded
WORDING inside these branches and added the `superseded` branch, but never the
ordering. Filed so the use-case is not lost, per Mike 2026-09-15.
