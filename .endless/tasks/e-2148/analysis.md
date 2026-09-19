## Expect many landings, not many tasks

Mike will iterate on this layout repeatedly. Land it, look at it, adjust, land
again — under THIS task id rather than filing a new task per tweak. The task is
the surface, not any one revision of it. Do not close it because one pass shipped.

## What is wrong today

Observed 2026-09-15 on a machine with two open incidents.

**1. `show` is the wrong verb.** It lists. `show` elsewhere in the CLI means "one
item, in detail" (`task show`, `decision show`). This should be `errors list`,
leaving `errors show <id>` free to mean what `show` means everywhere else.

**2. `errors show <id>` has nothing to show.** There is no detail view. The
listing truncates the summary mid-word with an ellipsis and there is nowhere to
go for the rest.

**3. The listing does not say which project caused it.** Default scope is the
current project, and the PROJECT column appears only under `--all-projects`. That
produced a flat contradiction: `endless errors show` printed "no errors" from
inside the endless project while the status line simultaneously reported
`1 error, 1 warning`, because both incidents belonged to a different project.
Two surfaces disagreeing about whether anything is wrong is worse than either
answer alone.

**4. It wraps, and wastes the width it has.** SEVERITY spells out "warning" and
"error" in full, and the status line spells out "1 error, 1 warning". Icons carry
the same distinction in one column instead of ten, in both places. Reclaim the
width for the summary, which is the part being truncated.

**5. A warning carries an ERR- code.** `ERR-0001` is severity warning. Either the
prefix should follow severity (`WARN-0001`) or the prefix should be severity-
neutral; today it states one thing and means another.

**6. Nothing says how to resolve any of it.** The footer explains how to DISMISS
an incident and is careful to say dismissing is not a retry — but the incident
itself carries no remedy. `docs/errors.md` has a "What to do" section for every
code, and the listing never points at it. A user reading the list learns that
something is wrong and not one thing about fixing it.

**7. "Badge" is a name nobody chose.** The one-line notification that
`session status`, `session monitor`, `project status` and `project monitor`
append below their rows is called a "badge" everywhere: the package is
`internal/faultbadge`, and docs/guide/reference.md teaches the word. Asked
2026-09-16 what it referred to, Mike: "That is completely unintuitive to me.
'Notification row' makes more sense here."

It survived because a session coined it and later sessions cited that coinage as
though it were settled product vocabulary — the term appears nowhere Mike wrote
it. A name that exists only in text Claude produced is unratified, however many
files carry it.

The rename spans the package name, the comments throughout it, docs/errors.md,
docs/guide/reference.md, and the guide's errors section: mechanical but wide.
Folded here on Mike's direction (surfaced during E-2151) rather than filed
separately, because this task owns the surface and item 4 already touches the
same row. It should still land as its own pass rather than mixed into a
functional one.

## Not in scope

The per-code remedy TEXT already exists in docs/errors.md and is asserted complete
by the faults package's own tests. This task surfaces it; it does not rewrite it.


