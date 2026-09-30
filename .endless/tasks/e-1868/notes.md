## An amended decision should leave `accepted` (from ES-1248, 2026-09-29)

ED-1538 was materially amended on 2026-08-20 under E-1991 (it added the
status and phase eligibility conditions) and stayed `accepted` throughout. An
amendment is a new judgment that nobody has accepted yet, so an amended
decision should drop back to a pending status: `proposed`, or a dedicated one
such as `review`/`revisit`/`updated`. The same rule the description re-spec
reset applies to tasks (a material edit invalidates prior approval).

Mike's call: do this as part of the move of decisions onto tasks, or fold it
into an existing task, rather than build it on the current decisions table.

## From the description

Reverts E-1511/E-1378 (decisions extracted into a dedicated decisions table).

a brainstorm child (E-1861) determines how, and implementation children emerge from it. Replaces E-1511. Also inherits the disposition of ED-907, which asserts decisions-on-tasks is a temporary shim 'until E-801/E-841 (documents system + ADR workflow)' - both now obsolete, and this epic makes decisions-on-tasks the destination rather than the shim, so ED-907 needs reconsidering or superseding once the mechanism is settled.
