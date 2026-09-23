## The relation verb for a task bound by a decision

Raised 2026-09-11 while planning E-2128, and folded in here rather than filed
separately: E-1868 removes the task/decision split, so anything built against
`decision_relations` today is work the move deletes.

**`implements` is wrong, and is banned in prose.** An architectural decision is
a standing constraint, not a specification that some task realises and retires.
ED-1589 ("a display never computes an expensive answer to paint a row") binds
every display surface built from now on, so "E-2128 implements ED-1589" claims a
completion that cannot happen — and if it could, the next display surface would
be free to compute. Mike's analogy: a homeowner deciding on midcentury modern
does not then write house plans titled "Implement my midcentury modern home".

`implements` is defensible only for a ONE-TIME commitment — "migrate to
Postgres" — where the decision really is carried out once and then stands. Most
decisions worth recording are rules, not migrations. The relation type is
currently named `implemented_by`, carrying the same flaw at the schema level;
this is the moment to fix it, since the storage is being rebuilt anyway.

**Candidates Mike raised:** complies with, conforms to, respects, follows,
obeys, defers to, honors, adheres to, aligns with, per.

Notes toward choosing, not a choice:

- `per` is the dominant form in the existing corpus by a wide margin, but it is
  a preposition, so it works as an inline citation and not as a relation name.
- `follows` reads best in prose, but its inverse "followed by" collides with
  temporal ordering, which a task tracker cannot afford to blur.
- `defers to` implies yielding in a conflict rather than being built within a
  constraint, which is a different relation.
- `obeys` and `honors` are anthropomorphic; `aligns with` is weaker than the
  relation actually is.
- `conforms to` has no temporal ambiguity and its natural inverse is a real verb
  (`constrains` / `governs`) rather than an awkward passive — matching the
  asymmetric pair `documents`/`implements` the schema already uses.

**Pick the inverse at the same time.** The decision side needs a natural reading.
"Implemented by" was tolerable; "conformed to by" is not.

**Whatever is chosen must let a task SHOW the decisions that bind it.**
`endless task show` renders task-to-task relations only — ED-1589 links to
E-2128 and appears nowhere in `task show E-2128` — so a session reading its own
task cannot see which decisions constrain it. That gap is why tasks acquired the
habit of announcing the decision in their opening line. E-2131 was filed against
the current schema to fix the rendering and was declined as work E-1868 deletes;
the requirement belongs here instead.



## The merge: what the discussion of 2026-09-13 settled

Worked through with Mike. Recorded here rather than as decisions, because these
are design calls for this epic's tree, not standing constraints on the project.

### Decisions get TWO homes, not one

- `tasks.type='decision'` identifies an ARCHITECTURAL decision. Type is the
  right carrier because it is already the mechanism that selects a status
  ladder: brainstorm uses unreviewed/completed, todo uses unverified/confirmed,
  and a decision needs proposed/accepted/rejected/superseded/obsolete. A content
  row cannot select a lifecycle. E-908 is precedent — `decision` was a task type
  before the extraction.
- `task_content` named `decision` carries an IMPLEMENTATION decision: a choice
  made while doing one task, which binds nothing beyond it.

Mike's observation is what motivates the split: when decisions were tasks,
agents rarely filed them and instead buried decisions in plans; since the
extraction, agents file them constantly and file the wrong KIND — task
implementation decisions dressed as architecture.

ED-1590 is the worked example, produced during this very discussion. An agent
filed "a destructive sweep runs on a schedule, never from a hook on the
tool-call path" as an architectural record; Mike rejected it as overbroad —
generalising an absolute prohibition from a single sweep — and as prescribing a
mechanism the code already had. With a `decision` content row available it would
have landed on E-2128 where it belonged, and the pressure to inflate it into a
project-wide rule would not have existed.

### A decision is multi-part, and today's shape cannot hold one

Current decisions carry a single description capped at 1024 characters. Writing
ED-1589 and ED-1590 during this session hit that cap three times — at 1704, 1097
and 1053 characters — and each retry compressed rationale out. What survives is a
statement with a `Rejected:` clause jammed onto the end, competing for the same
budget as the statement itself.

Under `task_content` the parts separate naturally: `statement`, `rationale`,
`rejected`, `consequences`, each its own row with its own room. A decision with
parts is also precisely an Agent Folio Format stream, so the agent-output work
and this one converge rather than competing.

### Numbering

The id spaces ALREADY collide and this is verified, not assumed: ED-1587, ED-1589
and ED-1590 all exist as decisions, and E-1587, E-1589 and E-1590 all exist as
unrelated live tasks. The ranges overlap densely across the corpus.

Therefore:

- **One sequence.** Decisions draw task ids. ED- dies as a NAMESPACE; there is no
  separate dispenser. (Note: a single dispenser across all entity kinds was
  considered previously and rejected; this is narrower — it merges decisions into
  tasks only.)
- **A one-time renumber**, with a permanent alias from each legacy ED- id to its
  new task id. Roughly 1,600 rows, closed at the merge, never growing again.
- **No stored epoch.** An epoch constant and an alias lookup are behaviourally
  identical: look the id up, and a miss means it is post-merge and the number IS
  the task id. The boundary is defined by the table's contents rather than by a
  constant somebody has to maintain and can get wrong.

### One prefix for all tasks

ED- does NOT survive as a live display prefix, and the deciding argument is
mutability: `task update --type` exists, so a type-derived prefix changes a
task's display id when the task is retyped — silently invalidating the form of
every reference already written in plans, lessons, briefs and the ledger. That
defect applies to ED- as much as to a hypothetical EF-/EB-/ER-/EE- family, unless
decision-type were immutable by rule.

The type is already present in every output (`type=todo`), so a per-type prefix
carries no information that is otherwise unavailable — it carries it redundantly,
at the cost of being wrong after a retype.

ED- therefore survives only as a legacy alias prefix that resolves, never as a
rendering. The rule becomes: one prefix per TABLE, with no per-type variation
within tasks.

### Renumber the entity, never the references

Do not rewrite content that mentions ED-NNNN. Three reasons, the first decisive:

1. **The ledger cannot be rewritten.** It is write-only by design and holds the
   bulk of the references, so a rewrite could not be complete even in principle —
   it would leave prose rewritten and history not, which is worse than leaving
   both.
2. Prose references live in plans, analyses, LESSONS.md and the private briefs: a
   mass find-and-replace with no rollback, over content whose correctness nobody
   can verify by reading.
3. Resolution is free. ED-1589 hits the alias, resolves, displays. Old documents
   stay true and the lookup absorbs the change.



### The disk mirrors move too, and this epic owns that move

Noted 2026-09-15. E-2137 phase B consolidates task content into
`.endless/tasks/e-NNNN/<type>.md` and explicitly leaves decisions where they
are, at `.endless/decisions/ED-NNNN.md`, on the grounds that `ED-NNNN` has no
owning task and so is not task-scoped.

That reasoning is correct today and stops being correct the moment this epic
lands. Once a decision IS a task, its mirror is task-scoped like every other
task's content and belongs under `.endless/tasks/e-NNNN/`.

**The move belongs here, not in E-2137**, for the same reason Mike left
`decisions.text` alone: a decision's id changes in the renumber, so moving the
files before that means moving each one twice and landing it at a name that is
about to be wrong. Doing it here moves each file once, straight to its final id
and path.

Concretely, this epic's implementation has to:

- Relocate each decision's content from `.endless/decisions/ED-NNNN.md` to the
  merged task's directory, at the new id, not the legacy one.
- Split it into parts if the multi-part shape above is adopted — `statement`,
  `rationale`, `rejected`, `consequences` — rather than carrying one blob.
- Remove `.endless/decisions/` once empty.
- Leave the legacy `ED-NNNN` ids resolving through the alias table. The FILES
  move; the references do not, per the renumber-the-entity-never-the-references
  rule above.



# Evidence from a supersede, 2026-09-23: there is nowhere to put reasoning

A decision today carries a TITLE and a DESCRIPTION and nothing else. The
description is capped at 1024 characters and must be a single line — no
newlines. `decision add` and `decision update` offer `--description` and
`--description-file`; there is no `--analysis`, no `--plan`, no long-form field
of any kind.

The content model has to answer for that, because it bites hardest exactly when
a decision matters most:

- **Retiring a decision has nowhere to merge its reasoning into.** ED-1570
  superseding ED-1567, and ED-1596 superseding ED-1595, each required folding the
  predecessor's surviving rationale into the successor so the retirement lost
  nothing. Both merged texts (1398 and 1669 chars, several paragraphs each)
  were refused. Every attempt was compressed until it fit, and each pass dropped
  something real — the SQLite-nullability sentence from ED-1570, the E-1972
  worktree measurements and the verify-suite citations from ED-1596. Nothing was
  lost permanently only because both brainstorm outcomes (E-1944, E-1972) happen
  to carry the same content at full length.
- **So the durable record is the task outcome, and the decision is a pointer to
  it.** That is the shape in practice. Whether it is the shape intended is this
  brainstorm's question — and if it is, the decision's own field should say so
  rather than being a place people try to write reasoning into and get refused.
- **Single-line is a separate constraint from the cap and hurts more.** A
  1024-character budget is defensible for a blurb; forbidding paragraph breaks
  inside it makes even a well-sized decision unreadable, and rules out any
  structure (the two directions of a version comparison, a list of what a change
  drops).
- **The refusal message points at fields that do not exist.** It reads "Long-form
  goes in --analysis (rationale) or --plan (the plan)" — borrowed from the task
  validator. On a decision it sends the author somewhere there is nothing.

Carrying decisions on tasks would inherit tasks' own content model, where
`analysis` and `plan` already exist and already hold long-form. That is an
argument FOR the move that this analysis did not previously make.
