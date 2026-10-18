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
