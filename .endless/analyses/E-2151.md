## What happens now

`internal/faultbadge` drops a WARNING from the badge once more than an hour of
ACTIVE time has passed since its last occurrence — `staleWarningAfter`, consumed
by `badgeworthy`. Errors never age off. The constant exists nowhere else, and
`endless errors show` does not consult it.

So the incident is not deleted or cleared. It stops being badged.

## Why that is the wrong trade

The distinction between "still listed" and "still badged" is the whole thing. The
badge is what makes a person think to run `endless errors show`. An incident that
leaves the badge has, in practice, left — it is discoverable only by someone who
already suspects it exists.

E-1950 introduced the age-off against a real annoyance: a fault that happened
once, self-healed, and then pinned a permanently unactionable warning to the
pane. The reasoning was that such a warning has nothing left to say.

It has one thing left to say: that it happened. Whether that matters is the
user's call to make, and clearing is a single command (`eeh`, or
`endless errors clear`). The trade is a row on a pane against a failure the user
never learns about — and those costs are not comparable.

It is also the exact failure mode this subsystem's neighbours exist to fix.
`project status` and `project monitor` were built because unverified work accumulated unseen —
57 tasks in one project, 71 of 75 machine-wide between 30 and 90 days old. The
34 stale `needs_input` session rows rotted for 25 to 71 days for the same reason,
and E-2091's answer was to REVEAL them rather than keep hiding them, on Mike's
correction that showing them is what provides the mechanism to resolve them.

A warning that removes itself from the only surface that shows it is that
pattern, inside the fault store.

## Decided

Mike, 2026-09-15: neither severity ages off. Every uncleared incident stays
badged until someone clears it.

This reverses E-1950's rule and restores E-698's original one — "clearing is
manual, so an intermittent fault cannot heal itself out of view" — which was
right for the reason E-1950 undervalued.

## Consequence worth stating plainly

Badge noise becomes the user's to manage rather than the system's. A machine that
accumulates warnings will show a badge until they are cleared, and the pressure
that creates is deliberate: it is what makes clearing a thing that gets done. If
that proves unworkable in practice the answer is a better clearing affordance —
clear-by-code, clear-all-warnings — not a timer that decides on the user's behalf
which failures were unimportant.
