# Decisions

Decisions are first-class items in Endless. They live alongside tasks and capture *why* something is the way it is — choices about approach, scope, conventions, deferrals.

> **Implementation note (transitional):** decisions are currently stored in the `tasks` table with `type=decision` and will move to their own table in a future release. The CLI surface (`endless decision ...`) is stable; the underlying storage is implementation detail you can ignore today.

## STRONG guidance (read this before writing decisions)

**Always document decisions where applicable.** When you and your user resolve a non-obvious question — choosing between approaches, scoping in/out, picking conventions, deferring something to later — file it. Decisions you didn't write down will be re-argued in three weeks.

**Verify with your user before stating a decision as binding.** Especially: do **not** record a *prohibition* when your user only expressed a *preference*.

Soft signals (preferences, not rules):

- "Ideally we'd ..."
- "Usually we ..."
- "I'd prefer ..."
- "Most of the time, ..."
- "Let's try ..."

Hard signals (rules):

- "Never do X."
- "Always X."
- "X is required."
- "Don't ship without X."
- Direct acceptance after explicit "should this be a rule?"

When you're not sure which you heard, **ask before writing**. A wrongly-recorded prohibition is harder to recover from than an undocumented preference — the prohibition gets cited as authority in future sessions, calcifies, and becomes part of "how things are."

**Don't self-cite.** If you wrote a decision yesterday and you're now treating it as established convention, verify with `git blame` or with your user first. Your own prior plans are not authority (see also the user's session-memory notes if any).

## Inline `--decision` is the preferred form

The cheapest way to record a decision is inline, on the task that prompted it:

```bash
endless task add "Verb-first title" --decision "Why we chose this approach over the alternatives."
endless task update <id> --decision "Why we changed the approach."
```

This creates a paired decision-type task and links it to the originating task via a `documents` relation. The decision is searchable, reviewable, and tied to context.

## Explicit creation

For decisions without a single triggering task, or for cross-cutting choices:

```bash
endless decision add "Statement of the decision" \
    --description "Longer explanation if needed" \
    --about <task_id>     # task this decision documents (soft link, repeatable)
    --decides <task_id>   # task this decision settles (hard link, repeatable)
```

Decision titles should state the decision directly. **Do not** start with "Record that ...". Decision titles skip the verb-first validation since they're statements, not actions.

Examples of good decision titles:

- `Use --type for relation-type flag on task link/unlink`
- `Project-config writes resolve to cwd-local, not anchored to main`
- `Allow tracking field to be set in global config as a default`

Examples of bad decision titles:

- "Decided on the approach" (vague)
- "Record that we picked X" (don't start titles with "Record that")
- "User prefers X" (a preference voiced by one person isn't a decision; if it's a rule, state the rule directly)

## Viewing and linking

```bash
endless decision list                            # decisions for current project
endless decision list --llm                      # token-efficient
endless decision show <id>
endless decision link <a> --to <b> --type ...    # decision-to-decision typed link
endless decision unlink <a> --to <b> --type ...
```

## Distinguishing decision from task

- A **task** is something to do.
- A **decision** explains why something was done a particular way (or not done).

Same item is rarely both. When in doubt: if the verb in the title is imperative ("Add", "Refactor", "Fix"), it's a task. If it's declarative ("Use X for Y", "Allow ...", "Prefer ..."), it's a decision.

## What deserves a decision

- Choosing between alternatives (database X vs Y, library X vs Y, pattern X vs Y).
- Scoping in or out (we will not do X because ...).
- Naming / vocabulary conventions.
- Deferrals ("not now because ...").
- Reversals of prior decisions.

What doesn't:

- Routine implementation details (variable names, where a function goes).
- One-time choices with no future ambiguity ("file lives at this path because that's where the test fixture loader looks").

## See also

- `endless guide tasks` — `--decision` flag on task add/update
- `endless guide` (index) — status semantics, when transitions warrant a decision
