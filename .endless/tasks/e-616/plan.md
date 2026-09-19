# Plan #616: Plan Text Lifecycle — Sync Text After Implementation

## Context

Plan text is written before implementation and never updated. By the time a plan item is completed, the text may be significantly diverged from what was actually built — decisions made during verify cycles, user feedback, changed approaches. The #510 implementation is a case study: the original plan proposed click-timers and templUI components, but the final implementation used overlay modals with plain HTML.

## Proposed Approach

### Structure: Original Plan + Implementation Record

When a plan item is completed, its text should contain both the original intent and the actual outcome:

```markdown
# Plan #NNN: Title

## Original Plan
(the text as it was when status moved to `ready` or `in_progress`)

## Implementation Record
### What was built
(summary of actual implementation)

### Key decisions made during implementation
(what changed from the plan and why — especially user feedback that redirected the approach)

### Files modified
(list of changed files)
```

### Mechanism

**Option A: `plan complete` command appends** — When `endless plan complete <id>` is run, it prompts the session to provide implementation notes (or the session proactively provides them). The command prepends "## Original Plan" to the existing text and appends the new section.

**Option B: Hook on status change** — The PostToolUse hook detects when `plan complete` is run and injects additionalContext asking the session to update the plan text via `endless plan update <id> --text <file>`.

**Option C: Session guide convention** — The guide doc instructs sessions to update plan text before marking verify/complete. No automation, just convention.

### Recommendation

Start with **Option C** (add to guide doc now, zero code) and build toward **Option A** (automate in `plan complete`). Option B is fragile and over-engineered.

### What to capture

- Decisions that diverged from the original plan
- User feedback that changed direction (especially from AskUserQuestion — ties to #582)
- Files actually modified
- Status transitions that happened (verify → in_progress → verify → completed)

### Open questions

- Should the original plan text be preserved verbatim, or can it be summarized?
- Should `plan complete` refuse to complete if text hasn't been updated since `in_progress`?
- How does this interact with the audit trail (#617)?
