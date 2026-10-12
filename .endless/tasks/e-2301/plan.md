# Let an epic start on its context: stopgap until E-2254

Mike, 2026-10-12: require context, ship as a stopgap. An epic is startable
(claim, spawn, prime) when it has a plan OR written context, the same rule
brainstorm and research tasks already follow (E-1994). Requiring the epic's
objective and strategy instead is E-2254's job once those fields exist.

## Changes

1. `src/endless/task_cmd.py`: add `epic` to `_FRAMING_SUFFICES_TYPES`. That one
   set drives the three places that must agree:
   - `_require_spawnable` (the claim/spawn/prime gate) accepts an epic with
     context and no plan;
   - `task prime` does not tell a primed epic session to draft a plan
     (`drafting` is false when framing suffices);
   - the prime handoff renders its "framing is the starting point, a plan is
     not expected yet" clause for an epic (`framing_suffices`).
2. The refusal, for a type whose framing suffices and that has neither a plan
   nor context, says "has no plan or context". It names
   `endless task update <id> --context-file <path>` as the way forward, beside
   the plan route. This applies to brainstorm and research too: their refusal
   currently names only the plan, which is wrong for them as well.
3. `internal/templatecmd/templates/handoff/epic.md.tmpl`: step 2 reads the
   epic's "context and plan" instead of "the plan". Step 5's stop condition
   reads "the context or plan is insufficient", so a plan-less epic session
   does not stop just because there is no plan.
4. The `prime.md.tmpl` header comment's list of framing-suffices types gains
   `epic`.

5. Grown in implementation: `prime.md.tmpl`'s framing clause read "This is a
   {{.task_type}}", which renders "This is a epic"; it now reads "This task's
   type is {{.task_type}}". The agent-facing refusal summary and remedy (and
   the parked-on-questions summary) say "no plan or context" and name
   `--context-file` for these types, matching the human text.

## Deliverables (each independently checkable)

- `endless task spawn` / `task claim` on an epic with context and no plan
  succeed (pytest).
- The same on an epic with neither plan nor context is refused, and the
  refusal names `--context-file` (pytest).
- A brainstorm with neither is refused with the same context wording (pytest).
- A todo or bugfix with context and no plan is still refused for having no
  plan (pytest), so the waiver stays type-scoped.
- `task prime` on a plan-less epic with context renders the framing clause,
  not the drafting step (pytest or Go template test).
- The rendered epic handoff says "context and plan" in step 2 and "context or
  plan" in step 5 (Go template test).
- `just test` and `just test-go` pass.

## Out of scope

- Requiring an epic's objective and strategy: E-2254, which adds those fields.
  When it lands, that task replaces this stopgap; this one does not
  anticipate it.
- The plan gate for any type other than epic.
- Refusing to claim or spawn a task whose blocker is unfinished: being filed
  by another session.
