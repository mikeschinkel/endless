Load-bearing for AI authoring and the bare-clone guarantee:

Critically this includes the one-command handoff contract (added at Mike's direction, 2026-07-10): the verify-handoff step of every spawn template must direct the session to run project-wide regression itself and REPORT it in prose, and to hand the user exactly ONE verification command, never an enumerated checklist of manual test commands. Strike any 'how to test: run A, then B, then C' pattern from the final-message contract.

Dogfooding target: run the one verify command; if it passes, we are done.
