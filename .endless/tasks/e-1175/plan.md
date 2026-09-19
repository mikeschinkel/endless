# E-1175 — Task lifecycle metadata: who owns description / outcome / plan, and when

## Context

`endless task spawn` was originally introduced for one use case: an ad-hoc Claude session writes a prompt for an unrelated NEW session, hands it off, and walks away. Single shot, low ceremony.

It was later adopted for a second use case: a long-running **delegator** session running in `now-ready-auto` mode that picks tasks off the ready queue and spawns implementation sessions for each. Higher cadence, multiple parallel sessions, requires a clean per-task contract.

The same `task add` + `task spawn` primitives serve both use cases today. The metadata bar that's adequate for the first is inadequate for the second, and we have no enforcement.

## What surfaced this

Session E-1144 (rename tmux window labels) was spawned by a delegator. When it finished:

- `description` field was empty (delegator didn't set one; spawned session didn't backfill).
- `text` (plan) was empty (forgivable — small task).
- `outcome` was empty (the most useful artifact: the prompt assumed `:` would work as a separator; reality required falling through `:` → `.` → `_` due to tmux's target-spec parsing rules; that journey is gone now unless someone reads the chat transcript).

A reader scanning `endless task list` saw only the title. The lessons from the session — non-obvious tmux delimiter behavior, the slug heuristic decision — were not captured.

## Decision

| Field         | Required when                          | Owner                                     | Enforcement                                                          |
|---------------|----------------------------------------|-------------------------------------------|----------------------------------------------------------------------|
| `description` | At `task add` for non-human callers     | Whoever files the task (delegator / Claude) | Hard: `task add` errors without it. Humans pass `--draft` to bypass. |
| `description` | Before `task prompt` / `task spawn`     | Whoever spawns                            | Hard: `task prompt` and `task spawn` refuse if missing.              |
| `outcome`     | Before `task update --status verify`    | Spawned session                           | Soft: warn to stderr, do not block.                                  |
| `plan` (text) | Optional always                        | Whoever has design content                | None. Most tasks legitimately have no plan.                          |

`description` is for the human/agent scanning a list — a 1-sentence "what is this." `prompt` is procedural instructions for a spawned session and does not substitute. `outcome` is the post-mortem written by whoever did the work — what shipped, what diverged from the prompt, what surprises were filed as follow-ups.

The hard gate at file-time forces delegator (Claude) callers to author description; the `--draft` flag preserves friction-free human filing where Claude will fill it in later. The hard gate at spawn-time catches the bypass case — a `--draft`-filed task with no description should not be spawnable until someone (probably Claude, on the human's behalf) authors the description.

## Why soft (not hard) on outcome

A completed change with no `outcome` field is still better than no change. Hard-gating the `verify` transition would punish the spawned session for finishing — exactly when we want it to ship. The warning is enough nudge for the right callers and ignorable for the wrong ones; the long-run answer is making outcome easy enough to write that nobody bothers skipping.

## Why no enforcement on plan

Most tasks don't need one. A "rename a flag" task with a 2-line title is self-documenting; a "design new IPC layer" task obviously needs a plan. Forcing a plan field when none is warranted devalues the field.

## Implementation

Three sibling tasks, all related to this decision and to E-1000 (which renames `text` → `plan` and is a prerequisite for using the term "plan" consistently):

- **E-1176** — `task add` requires `--description`; add `--draft` flag for humans.
- **E-1177** — Soft stderr warnings on `task update --status verify` when description or outcome empty.
- **E-1178** — `task prompt` and `task spawn` refuse when description is missing.

Order: E-1176 first (creates the field-required guarantee). E-1177 and E-1178 are independent of each other; either can land before the other.

## Behaviors expected of delegator sessions going forward (before code ships)

The CLI gates land later. In the meantime, delegator sessions should:

1. Always pass `--description` to `task add` when filing a task they intend to spawn.
2. Treat the `prompt` as procedural-only — do not duplicate the "what is this" content from the description into the prompt.
3. End each spawned session's instruction set with: "Before flipping status to verify, run `endless task update <id> --outcome <one-paragraph summary>`."

## Out of scope

- Auto-generating description from prompt's first sentence. Too magical; humans/Claude can do it deliberately.
- Forcing description on transitions other than `verify` and on commands other than `prompt` / `spawn`.
- Changing the spawn-prompt template content itself (separate concern; concerns the delegator's prompt-generation code).

## Related

- **E-1000** — Rename `tasks.text` column and CLI `--text` flag to `--plan`. Prerequisite for consistent terminology in this decision.
- **E-999** — Design field separation for plan content vs analysis. Adjacent / future.
- **E-1144** — Triggering example. The session whose missing metadata surfaced the gap.
