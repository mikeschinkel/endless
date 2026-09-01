# E-1794 (reopened) — the gate still can't tell a leading slash from a path

## What the first pass got wrong

The first pass narrowed "is this a path" to three lexical signals: absolute,
an explicit ./ ../ ~/ prefix, or a filename extension. It defined absolute as
"a leading slash followed by a path" — which fixed a lone '/' but left every
one-segment leading-slash token reading as an absolute path.

So these are all refused today, none of which is a path:

    /whats-left            a slash-command name in a lesson
    /loop  /code-review    same, and there are dozens more
    /tmp                   a bare well-known directory named in prose

The last one is the sharpest evidence: the gate's own refusal text says "a /tmp
path is lost when a worktree drops", and that sentence cannot be written into a
lesson or a decision by the tool that emits it.

## It is one defect, not three verbs' worth

Reported against `lesson write`, and suspected of `decision add/update`. Both
confirmed — along with `task add/update`, `epic`, and the status-transition
verbs. That is not three bugs: every inline/file flag pair funnels through
`_resolve_content_flag`, which is the single composition point the gate was
deliberately built on. One fix there covers all of them, and any fix made
per-verb would be wrong by construction.

## The rule — one tier, decided

An absolute path is a leading slash, then a directory with something in it
(/tmp/plan.md, /Users/x/plan.md), or a single segment carrying a filename
extension (/plan.md). Nothing after the slash is not a path. A single segment
without an extension is not a path either.

That last clause is the whole reopening, and it is DECIDED, not deferred:

    /whats-left  /loop  /code-review     slash-command names
    /tmp  /etc  /Users                   directories named in prose

Nothing the gate exists to catch is given up. A mis-passed file — the E-1626
corruption — always arrives carrying a directory or an extension. It is never a
bare /tmp.

## Why there is no model tier, and no filesystem probe

Both were considered and both are worse than the lexical rule.

A filesystem probe ("does the directory exist?") ties a gate over durable,
shared, cross-machine content to one machine's disk, and fails OPEN on the
gate's primary target: on a box with no /Users, a non-portable /Users path stops
being detected. Caching the verdict would fix the machine-dependence but not the
next problem.

A model tier — the shape task_cmd._check_verb_via_haiku already uses, table
first, model on miss, cache the answer — would answer the ambiguity correctly
and thereby REINSTATE the bug. Asked whether /tmp is a path, a model says yes,
because as a directory it is. Then "a /tmp path is lost when a worktree drops" —
this gate's own refusal text — becomes unwritable in a lesson again, which is
the exact false refusal this task was reopened to remove.

A false refusal is not a cheap error. It costs the filing agent a thrash, then
costs a human turn to adjudicate. Measured against that, the cheap lexical
answer is also the correct one, so there is no seam here to fill in later.

## Must not regress

Everything the first pass protected, all of it lexical and none of it reaching
tier 2:

    /tmp/plan.md  ./o.md  ~/d.txt  plan.md  docs/guide/index.md   still refused
    absolute paths embedded in prose                              still refused
    --allow-path still per-path, not a blanket
    pytest/uv, a named '/', type / subtype                        still accepted

## Tests and verification

- tests/test_content_flag_gate.py — the tier-1 table and the degrade path, with
  the model call stubbed so the suite stays hermetic and offline.
- .endless/tasks/e-1794/verify.sh — extend the suite this task already owns:
  the reported cases end to end on lesson write and decision add/update, not
  only task update, since the report came from lesson write.
- The plan itself had to be attached with --text-file: its own text is full of
  the tokens under discussion, and file content is never gated. That is the
  sanctioned route working as designed, and it is also why the gate's advice
  ordering (part 2, already landed) matters.
