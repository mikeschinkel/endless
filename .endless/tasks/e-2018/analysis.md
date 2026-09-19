## Should docs/status-lifecycle.mmd be the source for the guard? (Mike, 2026-08-20)

Yes — but not as a runtime dependency, and not against the diagram as it stands
today.

### The parse is genuinely deterministic

`stateDiagram-v2`'s transition syntax is a tiny regular subset: `A --> B: label`.
One regex over the file extracted every edge cleanly on the first try. No LLM,
no ambiguity, no `claude -p`.

### The diagram today is NOT complete enough to derive from

Measured, not assumed — parsing the current file gives:

- **`blocked` does not appear at all.** It is a status in the guide's table but
  has no node and no edges. A diagram-derived table would refuse every
  transition into or out of `blocked`.
- **`completed` has no inbound edge.** Only `completed --> revisit` and
  `completed --> [*]`. Nothing draws how a task REACHES `completed` — yet
  research tasks land there (ED-1502), and E-1817 itself is `completed`. A
  diagram-derived guard would have refused the exact transition that closed the
  task that filed this one.

Both are documentation defects that have been invisible because nothing consumed
the diagram. That is the argument FOR doing this: deriving from it converts them
into a failing test on day one.

### The diagram cannot express two axes the guard needs

- **Actor.** Edge labels carry it as prose — "user approves", "session claims",
  "agent submits", "triage routes". Consistent enough to become a label grammar,
  but that would make the diagram a DSL wearing mermaid syntax. Worth doing
  deliberately, not by accident.
- **Task type.** `unverified → confirmed` is implementation; `→ completed` is
  research. E-2016 adds another status for research/brainstorm. One diagram,
  several lifecycles.

### Recommendation: GENERATE the diagram from the table (Mike, 2026-08-20)

Parsing was the wrong direction. Invert it: the Go transition table is the
source, and `docs/status-lifecycle.mmd` is a generated artifact. Drift stops
being something a test DETECTS and becomes something that cannot happen.

This is not a new mechanism — the repo already runs this pattern. `just
guide-index` regenerates a block in `docs/guide/index.md` between
`<!-- BEGIN generated: ... -->` / `<!-- END generated -->`, and `just
guide-check` exits non-zero on drift as a pre-land / CI gate. Apply the same
shape to the lifecycle.

What it collapses: today the `.mmd` is hand-authored, byte-copied into
README.md and docs/guide/index.md, and `tests/test_status_lifecycle_sync.py`
asserts the copies match. With generation the generator writes all of them, and
that test's job narrows to "is the committed artifact current" — the `gofmt -l`
shape.

It also fixes the two gaps above for free: `blocked` missing and `completed`
having no inbound edge are both artifacts of hand-maintenance.

And it makes the actor axis honest. Edge labels ("user approves", "session
claims") stop being a prose convention a reader has to trust and become RENDERED
from the table's actor column.

**Generate parts, not the whole file.** The editorial content — "Blocking is a
relation (blocked_by), not a state, so it is intentionally absent" — is a
hand-written preamble in the template. Only the states and edges are generated.
Same split `guide-index` already uses.

### One trap

**Deterministic order.** Go map iteration is randomized, so the table must be
ordered. Use `dtx.OrderedMap` from `github.com/mikeschinkel/go-dt/dtx` — already
in go-pkgs, already a dependency shape this repo uses. Order the output to READ
well — entry, happy path, reopening, terminal — not alphabetically.

(A second trap — "model the deliberate omission of `blocked` as a render flag" —
is withdrawn. There was no deliberate omission; see below.)

### `blocked` was never a decision, it was a contradiction

I had written that `blocked` is "intentionally absent" from the diagram. That
came from the diagram's own comment ("Blocking is a relation (blocked_by), not a
state, so it is intentionally absent") — an agent-written comment I repeated as
fact. Measured instead:

- `blocked` IS in the status vocabulary and in SQL status lists.
- 8 tasks currently hold `status=blocked`.
- `endless task block` creates a `blocked_by` RELATION and never touches status.
- Every sampled blocked-status task ALSO has the relation (E-634, E-721 → E-808;
  E-1515 → E-1479).

So the system ships both mechanisms, independently, and the comment asserts a
design the rest of the system contradicts. Worse, the duplication must go stale:
E-808 is `unverified`; when it reaches confirmed the relation stops blocking
automatically while `status=blocked` sits on E-634 and E-721 until a human edits
it.

Rather than encode the ambiguity as a flag, decide it: **ED-1572** proposes
blocking is a relation only, and `blocked` leaves the status vocabulary. If
accepted, the generator needs no render flag and the guard needs no special case.

### Sequencing

The table comes first, with the actor and type columns and the render flag. Then
the generator and its drift check, replacing the byte-sync test. Then the guard
consumes the same table. The two diagram gaps need no separate fix — they
disappear when the diagram is generated, provided `completed`'s inbound edges are
written into the table (they exist in the CLI today; they were only ever missing
from the picture).



---

## Where the guard hooks in (added 2026-08-25)

E-2067, spawned today, is the same shape as this task's guard half and lands
first: `endless task update --parent` can create a parent cycle because
`tasks.parent_id` has TWO writers and only one validates. `task move` emits
`task.moved`, whose executor walks the ancestor chain and refuses a circular
reference; `task update` emits `task.fields_updated`, whose executor carries
`parent_id` in its allowedFields map and writes it unchecked.

E-2067's fix is to lift that ancestor walk into a shared validator called from
both executors — mirroring `ValidateMaybeParentless`, which is already factored
that way and called from both. So by the time this task starts,
`execTaskFieldsUpdated` should have an established place to hang a
per-field validator, and one worked example of the pattern.

This task's transition-legality and actor-reality checks belong in exactly that
slot: `task update --status` reaches the same executor by the same route that
let the parent cycle through. Read E-2067's landed diff before designing the
hook — if it introduced a validator registry rather than a single call, follow
it rather than inventing a second arrangement.

Note the asymmetry that makes this more than a copy: the parent guard needs only
the tasks table, while transition legality needs the edge table this task's own
plan proposes. So the hook is shared; the data behind it is not.
