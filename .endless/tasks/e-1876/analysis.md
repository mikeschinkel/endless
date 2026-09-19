# Why these five are one task

## The gap E-1803 and E-1901 leave

E-1803 defined acceptable agent output functionally — a computed fact the user
cannot derive XOR a genuine open decision, else nothing — and owns the
compose-time PostToolUse nudge. E-1901 adds the teeth: a Stop-hook detector that
blocks the turn unless the final message matches the sanctioned report block.

Between them they establish *that* output is gated and *where* the gate fires.
Neither establishes *what the test actually is*, whether what survives the test
is *true*, where a suppressed-but-genuine observation *goes*, or covers the
plan-write moment and after-the-fact observability. That is this task.

## Part-by-part

1. **Discharged-fact test.** A fact recorded in a test, a guard, a verify
   script, a commit message, or a task field is discharged and must not appear
   in user-facing output — including when the agent introduced the problem and
   then fixed it, where the fix IS the record and nothing is left to decide.
   Deliverable is a test an agent can apply in the moment, plus the boundary
   against a genuine open decision, which stays worth saying.

2. **Veracity.** Any fact that IS surfaced must be verified against live state
   (git, the ledger, the file) at the moment of surfacing, never inferred or
   recalled from earlier context. A wrong fact must clear a HIGHER bar than an
   omitted one: a confidently-wrong fact the user must track down to disprove
   costs more than ceremony does. Some classes are mechanically catchable and
   worth a lightweight check — an agent-authored task-ID citation whose target
   does not resolve or is topically unrelated, or a state claim git contradicts.

   Parts 1 and 2 are the two clauses of one test. Part 1 governs output VOLUME;
   part 2 governs output VERACITY. An agent applies both at the same instant,
   composing the same sentence.

3. **Sink.** `endless task annotate <id> "..."` — task-scoped, append-only,
   write-only by design, hidden from the default `task show`, surfaced only
   under `task show --annotations`. Deliberately NOT project notes: those are
   project-scoped, have found no good use since being added, and are likely to
   be deprecated. The sink must not relocate noise to where the user already
   reads, or it has solved nothing.

4. **Plan-write trigger.** After an agent writes a plan (`task update --text`,
   or a plan-mode save) the chat re-narrates the plan, forcing the user to
   review the same material twice — the plan AND the recap. Same disease as
   end-of-session ceremony: restating a reviewable artifact. Rides E-1772's
   existing mechanism (nudge in the reminder, gate in the command, no hook —
   a CLI command can do it), with wording tunable via the report-prompts surface.

5. **Monitor surface.** E-1901's Part 2 records a relay checkpoint per
   `task report` call into `session_gates`, carrying sanctioned text and a bounce
   count, with accessors already written. Surfacing per-session last-checkpoint
   and bounces in `session monitor` is a read on rows that will already exist.

## Why merged rather than five tasks

Behavioral coupling, not just topical adjacency:

- A **rule without a sink** makes the agent drop genuine observations or smuggle
  them into prose — the failure E-1865 demonstrates.
- A **sink without the rule** yields annotate-and-narrate: the agent writes the
  observation down AND says it, giving the user a third place to read.
- The **volume clause without the veracity clause** governs how much the agent
  says while leaving the costlier failure — wrong content — ungoverned.
- The **gate without parts 4 and 5** leaves the plan-write moment uncovered and
  leaves compliance unobservable.

Economic coupling as well, and it is decisive here: the user's review cost is
per-task attention, not per-task size. Five small tasks cost five shepherd
cycles — triage, approval, spawn, review, land — for changes that individually
move little. One task costs one. Shrinking a task does not reduce that cost;
only removing a task does.

## Evidence

E-1865: an invariant already guarded by its verify script, and a regression
already covered by a test, were both narrated to the user anyway. Every fact
narrated in that session already had a durable home. That is simultaneously the
case for part 1 (the facts were discharged), part 3 (the agent had nowhere
else to put what it felt compelled to record), and the reason a nudge alone
was known to be insufficient.

## Sequencing

Parts 1–4 are independent of anything in flight. Part 5 is blocked by E-1901:
it reads the `session_gates` relay rows E-1901 creates. If E-1901 changes shape
during implementation, part 5 follows it rather than the other way round.

## Merge provenance

Merged from E-1823 (veracity), E-1879 (sink), E-1787 (plan-write trigger), and
E-1826 (monitor surface). E-1826 additionally lost scope before the merge:
its item (1), recording a checkpoint per `task report` call, is delivered by
E-1901 Part 2; its item (3), a record-only Stop hook designed to sidestep the
blocking approach E-1803 rejected, is superseded outright — E-1901 un-rejects
blocking. Only its item (2), the monitor surface, survives as part 5 here.
