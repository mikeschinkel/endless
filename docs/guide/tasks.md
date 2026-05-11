# Tasks: Fields, CRUD, Verbs

Everything about manipulating the task tree: what each field is for, the full command surface, and the verb registry that validates titles.

---

## Task fields

Every task has multiple body fields. Knowing which to use prevents long descriptions that should have been plans, and short plans that should have been descriptions.

| Field         | Length         | Purpose                                                                                          | How to set                                         |
|---------------|----------------|--------------------------------------------------------------------------------------------------|----------------------------------------------------|
| `title`       | One line       | The task name. Verb-first (see Verbs below).                                                     | Positional arg on `task add`; `--title` on update. |
| `description` | < 200 words    | Brief pitch — *what* and *why* in a paragraph or two. Shown by default in `task list` / `task show`. | `--description` on `task add` / `task update`.     |
| `text`        | Long-form      | Full implementation plan, including approach, file paths, verification steps. Shown with `task show --text`. | `--text <file>` on `task add` / `task update`.     |
| `prompt`      | Long-form      | LLM prompt — what a spawned Claude session sees as its opening instructions for this task.       | `--prompt <file>` on `task update`.                |
| `analysis`    | Long-form      | Supporting research / exploration content that is *not* a proper plan (E-1073).                  | DB column; CLI flag may not yet be wired — set via DB or via web UI. |
| `notes`       | Freeform       | Catch-all for content that doesn't fit elsewhere. Use sparingly.                                 | DB column; CLI flag may not yet be wired.          |
| `outcome`     | Short to long  | Result / reason at terminal status. Required for `confirm`/`assume`/`decline`.                   | `--outcome` on `task confirm` / `task assume` / `task update`; `--reason` on `task decline` (stored as outcome). |

### Distinctions in practice

- **Description vs text.** Description is a pitch — readable in 30 seconds, fits in a list view. Text is the plan you'd hand to an engineer. If you're writing four paragraphs into `--description`, stop — put it in a plan file and load with `--text`.
- **Text vs prompt.** Text is for humans (and you, reading the task). Prompt is what gets pasted into a *spawned* Claude session as its starting input. Often similar but not identical: a prompt typically includes directives like "claim the task, do the work"; the text describes approach without imperatives.
- **Analysis vs text.** Analysis is research output — comparisons, findings, evidence. Text is the actionable plan. An audit task's deliverable lives in `outcome`, not `text` or `analysis`.
- **Outcome.** Single field for "how this task ended." Required at terminal statuses so the *why* is captured at the moment of the decision. `task decline` uses `--reason` as the CLI flag (stored as outcome internally).

---

## Viewing tasks

```bash
# List tasks for the current project
endless task list                                    # flat, sorted by ID
endless task list --tree                             # hierarchical
endless task list --all                              # include done items
endless task list --status ready                     # filter
endless task list --status needs_plan,ready          # comma-separated
endless task list --phase now
endless task list --parent E-799
endless task list --parent none                      # roots only
endless task list --related-to E-1248 --rel-type blocks
endless task list --sort status                      # id, status, phase, tier, created, title
endless task list --llm                              # token-efficient agent output
endless task list --json

# Detail for one task
endless task show <id>
endless task show <id> --text
endless task show <id> --prompt
endless task show <id> --children
endless task show <id> --outcome
endless task show <id> --no-description
endless task show <id> --llm
endless task show <id> --json

# Top actionable tasks, ranked
endless task next
endless task next --limit 5
endless task next --all                              # across all projects
endless task next --tier 1
endless task next --phase now
endless task next --llm

# Other reads
endless task recent                                  # recently updated
endless task active                                  # in_progress + verify
endless task search "query"                          # ID, title, description
endless task search "query" --text                   # also search text field
endless task search "query" --prompt
endless task prompt <id>                             # raw prompt, undecorated
```

Reach for `--llm` whenever you're parsing output yourself — it's token-efficient.

---

## Adding tasks

```bash
endless task add "Title here"
endless task add "Title here" --parent <parent_id>
endless task add "Title here" --description "Brief pitch" --phase now
endless task add "Title here" --text /path/to/plan.md --status ready
endless task add "Title here" --type bug             # task|plan|bug|research|spike|chore|decision
endless task add "Title here" --tier 1               # 1-4 or auto|quick|deep|discuss
endless task add "Title here" --blocked-by E-100     # also: --blocks, --relates-to,
                                                     # --implements, --cleans-up,
                                                     # --cleaned-up-by (all repeatable)
endless task add "Title here" --decision "<rationale>"  # creates paired decision-type task
```

Use the task ID printed by `task add` **literally**. IDs advance globally across parallel sessions — never guess.

---

## Updating tasks

```bash
endless task update <id> --title "New title"
endless task update <id> --description "..."
endless task update <id> --text /path/to/plan.md
endless task update <id> --prompt /path/to/prompt.md
endless task update <id> --status ready
endless task update <id> --phase later
endless task update <id> --tier 2
endless task update <id> --parent 444                # move under different parent
endless task update <id> --parent 0                  # make it a root
endless task update <id> --outcome "What was done"
endless task update <id> --decision "<rationale>"    # creates paired decision task
endless task update <id> <id2> ... --status ready    # bulk update
```

Attaching a plan (`--text`) to a `needs_plan` task auto-promotes the status to `ready`.

---

## Status transitions

```bash
endless task claim <id>                              # ready → in_progress + create worktree
endless task release [<id>]                          # release current session's claim
endless task update <id> --status verify             # work done, awaiting verification
endless task confirm <id> --outcome "..."            # user-only — sessions do not self-confirm
endless task confirm <id> --cascade --outcome "..."  # confirm a task and descendants
endless task assume <id> --outcome "..."             # believed complete, can't verify
endless task decline <id> --reason "..."             # active decision not to do
endless task replace <id> --by <new_id>              # supersede with another task
```

---

## Removing and moving

```bash
endless task remove <id>                             # warns if it has children
endless task remove <id> --cascade                   # also remove descendants
endless task move <id> --parent <parent_id>
endless task move <id> --root
endless task move --children-of <id> --root
endless task clear <id> --<field>                    # clear a single field
```

---

## Relations between tasks

```bash
endless task block <a> --by <b>                      # A is blocked by B
endless task unblock <a> --by <b>
endless task deps <id>                               # all relations grouped by type
endless task relations <id>                          # alias of deps with grouping
endless task link <a> --to <b> --type implements     # generic typed link
endless task unlink <a> --to <b> --type implements
```

Available `--type` values include `blocks`, `relates_to`, `implements`, `cleans_up`, `cleaned_up_by`, `documents`, `replaces` (see `endless guide decisions` for documents-link semantics).

---

## Sessions and chat

```bash
endless task chat                                    # start a chat-only session (no task tracking)
```

---

## Verbs

Verbs are the registered action words that may start a task title (E-723, E-947). When you `task add`, Endless validates that the title begins with a registered verb.

**Why:** verb-first titles enforce that every task names an action — "Fix login redirect", "Refactor task_cmd", "Document the guide command" — rather than vague nouns like "Login bug". This makes task lists readable at a glance.

```bash
endless verb list                                    # all registered verbs (project + machine layers)
endless verb add <verb>                              # register a new verb
endless verb remove <verb>                           # remove (with confirmation)
```

When `task add` rejects a title:

1. **Pick an existing verb** — run `endless verb list`, find one that fits.
2. **Register a new verb** if no existing one fits and the verb genuinely adds value: `endless verb add <verb>`. Use sparingly — the verb list is a contract for readability.
3. **`--force`** bypasses validation. Don't habituate to this — it's an escape hatch, not a workflow.

Verbs auto-commit to main on `worktree land` (E-1141).
