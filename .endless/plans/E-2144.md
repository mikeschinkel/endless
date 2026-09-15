## Grown scope, second pass (2026-09-15)

Mike challenged the "E-1956's refusal is untouched" line: he had just obsoleted
an epic whose work had shipped, and asked whether that gate rests on invalid
criteria.

Investigating found a defect in what this task's first pass landed. The first
pass grepped the literal phrase from this task's description ("never needed
doing") and missed two paraphrases of the same wrong definition:

- `src/endless/authority.py` — "it is obsolete: retired before the work ever
  shipped", the authority banner injected into agent context on every
  `task show` of an obsolete task.
- `internal/taskstatus/transitions.go` — the transition group name "made
  irrelevant before the work ever shipped", the banner's source of framing.

The banner was not just stale, it was false. `for_task` sees status and two
relations; it cannot see landings. E-1421 is an epic that landed twice
(18fe0f0), is obsolete, and printed "the work never shipped" directly above its
own `Landed:` line. Epic status is derived in Go and never passes the Python
gate, so "obsolete implies never shipped" is not an invariant the status carries.

Fixed under this task (commit 9f1f40c3): banner now reads "retired as no longer
needed"; group name describes the edge set rather than defining the word. Suite
section G covers both. Wording only — no edge changed, E-1956's gate untouched.

Left open, raised with Mike, NOT decided here: whether the E-1956 gate's premise
("obsolete reads as never happened") survives this task's redefinition, and
whether its remedy (`task replace <old> --by <new>`) has an answer for work that
is DELETED rather than superseded. E-1434 and E-1437 are `assumed` with real
landings and are the code E-2142 will remove; the gate refuses `obsolete` on
both and offers a remedy that needs a replacement to name.
