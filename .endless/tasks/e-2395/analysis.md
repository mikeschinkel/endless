Observations from the E-2300 discussion (2026-10-18), to explore:

- Every plan can be divided into sections, with one section as the smallest case. A section is a heading over a group of approach prose, so the rule "a plan has one or more sections" holds for every task type.
- What a section is differs by type:
  - todo / bugfix: one area of change (a piece of code, a command, a schema change), plus recurring non-code sections such as Tests, Docs, and Existing data/migration. A bugfix often has a natural first section for the cause.
  - research: one question or part of the request; the plan holds the research request, so sections divide what is asked, not what will change.
  - epic: one child or group of children, the decomposition and how the parts relate.
  - brainstorm: one open question with its options.
- One possible shape: fix the rule that sections exist, and let each type define what a section contains. For todo/bugfix, area-of-change sections plus a short fixed list of standard ones (Tests, Docs, Migration) that a process can recognise.
- What the current freeform shape cannot support: order is implied, not declared (nothing says whether sections must be done in sequence); a process cannot tell a standard section from an area of change; some prose is binding guidance and some is suggestion, and nothing marks which.
- Small plans: a one-line fix would carry a single section. Harmless, but whether a lone section adds anything is a question.

Use cases from plans written 2026-10-17/18 (E-2300 planning session):

- E-2373 (written by another session as "Steps" 1–6): steps 1–5 are really areas of change (Go reader, Python reader, help, docs, tests) with no order between them beyond tests last; the numbering implies a procedure the content does not need. Step 6, "run the full Go and Python suites", is a process action, not an area of change, and the handoff already requires it. Shows: process actions mixed in with areas of change, and order implied by numbering alone. The task would be implemented the same way from sections.
- E-2300 (numbered sections): loosely in dependency order (record the socket path before judging from it) without saying whether order matters. Has a "Tests" section that serves every other section, and an "Existing rows" section that describes how existing data behaves and involves no code change: a third kind of section beside areas of change and standard sections.
- E-2385: area-of-change sections (no fallback, `endless stop`, restore order, default terminal), one of which (`stop tasks`) contains an ordered list. That order is the runtime behavior being built (refuse, freeze the record, type /exit, wait, kill), not an implementation order. Shows that "order" means two different things: the order of the behavior being specified, which belongs inside a section, and the order of doing the work, which within one task the implementer works out from the code.
- E-2394: sections Command, Claim refusal, Docs, Tests: standard sections (Docs, Tests) appearing naturally beside areas of change. Its Command section also carries a constraint that applies to the whole task ("no SQLite added to Python"), a kind of prose that belongs to no single area.
- Operational order (hypothetical; no plan here needed it): a human or agent action required between two changes, such as running a backfill or rebuilding a projection. The one case where a step that cannot be inferred from the code is genuinely needed. Order across tasks is already expressed by blocked_by, not by plans.

Kinds of plan prose seen so far: area-of-change sections; standard sections (Tests, Docs, Migration); existing-data sections (behavior with no code change); task-wide constraints; ordered behavior inside a section; process actions (probably not plan content at all).
