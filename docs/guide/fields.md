# Task Fields

Every task has multiple body fields. Knowing which to use prevents long descriptions that should have been plans, and short plans that should have been descriptions.

## Field summary

| Field         | Length         | Purpose                                                                                          | How to set                                         |
|---------------|----------------|--------------------------------------------------------------------------------------------------|----------------------------------------------------|
| `title`       | One line       | The task name. Verb-first per E-723/947.                                                         | Positional arg on `task add`; `--title` on update. |
| `description` | < 200 words    | Brief pitch — *what* and *why* in a paragraph or two. Shown by default in `task list` / `task show` (E-1058). | `--description` on `task add` / `task update`.     |
| `text`        | Long-form      | Full implementation plan, including approach, file paths, verification steps. Shown with `task show --text`. | `--text <file>` on `task add` / `task update`.     |
| `prompt`      | Long-form      | LLM prompt — what a spawned Claude session sees as its opening instructions for this task.       | `--prompt <file>` on `task update`.                |
| `analysis`    | Long-form      | Supporting research / exploration content that is *not* a proper plan (E-1073).                  | DB column; CLI flag may not yet be wired — set via DB or via web UI. |
| `notes`       | Freeform       | Catch-all for content that doesn't fit anywhere else. Use sparingly.                             | DB column; CLI flag may not yet be wired.          |
| `outcome`     | Short to long  | Result / reason at terminal status. Required for `confirm`/`assume`/`decline`.                   | `--outcome` on `task confirm` / `task assume` / `task update`; `--reason` on `task decline` (stored as outcome). |

## Distinctions in practice

**Description vs text.** Description is a pitch — readable in 30 seconds, fits in a list view. Text is the plan you'd hand to an engineer. If you find yourself writing four paragraphs into `--description`, stop — put it in a plan file and load with `--text`.

**Text vs prompt.** Text is for humans (and you, reading the task). Prompt is what gets pasted into a *spawned* Claude session as its starting input. They're often similar but not identical: a prompt typically includes the directive "claim the task, do the work, then ..."; the text might describe approach without the imperatives.

**Analysis vs text.** Analysis is research output — comparisons, findings, evidence. Text is the actionable plan. An audit task's deliverable lives in `outcome`, not `text` or `analysis`.

**Outcome.** Single field for "how this task ended." Required at terminal statuses so that the *why* is captured at the moment of the decision. `task decline` uses `--reason` as the CLI flag (stored as outcome internally).

## Reading fields

```bash
endless task show <id>                  # title + description + relations (default)
endless task show <id> --text           # also show text (the plan)
endless task show <id> --prompt         # also show prompt
endless task show <id> --outcome        # also show outcome (always shown for declined)
endless task show <id> --children       # also show direct children
endless task show <id> --llm            # token-efficient form
endless task show <id> --json           # JSON
endless task prompt <id>                # just the raw prompt, undecorated (for piping)
```

## Writing fields

```bash
# At creation
endless task add "Verb-first title" \
    --description "Brief pitch" \
    --text /path/to/plan.md \
    --parent <parent_id> \
    --phase now

# Later
endless task update <id> --title "..."
endless task update <id> --description "..."
endless task update <id> --text /path/to/plan.md
endless task update <id> --prompt /path/to/prompt.md
endless task update <id> --outcome "What was done"
```

`--text` and `--prompt` accept a file path. The content is read and stored verbatim.

## Title rules (verb-first)

Titles must start with a registered action verb (E-723, E-947). If your title is rejected:

1. Pick a verb that fits — `endless verb list` shows registered verbs.
2. If no fitting verb exists, register one: `endless verb add <verb>`.
3. As a last resort, `--force` bypasses validation. Don't habituate to this.

## See also

- `endless guide tasks` — full CRUD
- `endless guide spawn` — how `prompt` is consumed by spawned sessions
- `endless guide decisions` — when fields imply a decision worth recording
