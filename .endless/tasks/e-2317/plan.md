# Plan: tests for what counts as a decision

Answers from Mike (2026-10-13): Q1 = B plus a qualifying-kinds list (EQ-46). Q2 = C, worded for any project, not just a CLI. Q3 = C. Q4 = A.

## Items

1. `docs/guide/decisions.md` has a section "Is it a decision?" that states three tests, all of which must pass:
   - **Real alternative:** someone could have argued the other side.
   - **Outlives the task:** it would still bind if the task that prompted it were reworked or deleted. A statement that describes one task's implementation (mechanisms, values or symbols) belongs in that task's plan or analysis, not a decision.
   - **The user made it:** the user made the choice, in this or any earlier session. A preference inferred from tone, or a rule the agent thinks ought to exist, is not one.
2. The same section requires at least one qualifying kind:
   - affects the project's structure;
   - cuts across components;
   - sets a scope boundary or deferral;
   - sets a convention the project's users see (vocabulary, naming, or the shape of its interface: web UI, API, CLI or other);
   - reverses or modifies an earlier decision.
3. The section says zero decisions is the normal outcome of a session, and that a decision mapping one-to-one onto a single task's implementation belongs in that task.
4. The section routes everything that fails a test to a lesson (`endless lesson write`), including conventions about how an agent works. When it's unclear which, it is a lesson.
5. "Always document decisions where applicable" is removed from the guide's STRONG guidance and replaced by a pointer to "Is it a decision?".
6. "What deserves a decision" / "What doesn't" are rewritten to match items 1–4, with no conflict between them.
7. The brainstorm handoff (`internal/templatecmd/templates/handoff/_mechanics.tmpl`, both places that mention `endless decision add`) still asks for decisions, but only those passing the tests. It names the three tests in one line, points to `endless guide decisions`, and says zero is normal.
8. Every other handoff template or guide page that tells an agent to file decisions gets the same one-line test names and pointer (found by searching templates and `docs/guide/` for `decision add`).
9. The guide cross-reference index is regenerated (`just guide-index`) if the new section changes it, and `just test` passes.

## Out of scope

- Changing `/whats-left` or shipping it. That's E-2318; once shipped, `/whats-left` should point to `endless guide decisions` instead of carrying its own copy of the tests.
- A check at `decision add` that warns on decisions linked to one task (Q1 option D, not chosen).
- Re-reviewing existing proposed or accepted decisions against the new tests.
