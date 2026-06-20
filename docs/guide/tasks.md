# Tasks: Fields, CRUD, Verbs

Everything about manipulating the task tree: what each field is for, the full command surface, and the verb registry that validates titles.

---

## Task fields

Every task has multiple body fields. Knowing which to use prevents long descriptions that should have been plans, and short plans that should have been descriptions.

| Field         | Length         | Purpose                                                                                          | How to set                                         |
|---------------|----------------|--------------------------------------------------------------------------------------------------|----------------------------------------------------|
| `title`       | One line       | The task name. Verb-first (see Verbs below).                                                     | Positional arg on `task add`; `--title` on update. |
| `description` | < 200 words    | Brief pitch — *what* and *why* in a paragraph or two. Shown by default in `task list` / `task show`. | `--description` on `task add` / `task update`.     |
| `text`        | Long-form      | Full implementation plan, including approach, file paths, verification steps. Shown with `task show --text`. **On a research task, `text` instead holds the research *request* — see the Research-task field model below.** | `--text <file>` on `task add` / `task update`.     |
| `analysis`    | Long-form      | Supporting research / exploration content that is *not* a proper plan — comparisons, findings, evidence gathered before the plan is written. | DB column; CLI flag may not yet be wired — set via DB or via web UI. |
| `notes`       | Freeform       | Catch-all for content that doesn't fit elsewhere. Use sparingly.                                 | DB column; CLI flag may not yet be wired.          |
| `outcome`     | Short to long  | Result / reason at terminal status. Required for `confirm`/`assume`/`decline`.                   | `--outcome` on `task confirm` / `task assume` / `task update`; `--reason` on `task decline` (stored as outcome). |

### Distinctions in practice

- **Description vs text.** Description is a pitch — max 1024 character — readable in 30 seconds, fits in a list view. Text is the plan you'd hand to an engineer. If you're writing four paragraphs into `--description`, stop — put it in a plan file and load with `--text`.
- **Text vs handoff.** Text is the plan — for humans and for the spawned session, which `endless task spawn` directs it to read. The session's *opening input* (the handoff) is generated from a template at spawn time, not stored on the task; see `endless guide orchestration`.
- **Analysis vs text.** Analysis is supporting evidence gathered *before a plan is written on a do-task* — comparisons, findings, raw material. Text is the actionable plan. A deliverable-shaped task (an audit, or a `research`-type task) puts its *result* in `outcome`, not `text` or `analysis`; for research tasks specifically, see the Research-task field model below.
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
endless task list --related-to <id> --rel-type blocks
endless task list --sort status                      # id, status, phase, tier, created, title
endless task list --llm                              # token-efficient agent output
endless task list --json

# Detail for one task
endless task show <id>
endless task show <id> --text
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
endless task handoff <id>                            # render the spawn handoff
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

### Research-type gate

`--type research` discourages casual use: a research task is justified only when its findings can't be inlined as a do-task. The CLI enforces this:

- **Exempt:** `--parent <id>` where `<id>` is an `--type epic --status in_progress` task. No `--justification` required.
- **Otherwise:** `--justification "<reason>"` is required and stored under a `## Justification` heading in the task's notes.

```bash
# Exempt: parent is an in-progress epic
endless task add "Compare X vs Y" --type research --parent E-100

# Standalone: justification required
endless task add "Compare X vs Y" --type research \
    --justification "Needs benchmarks across 3 datasets before plan."
```

The same gate fires on `endless task update --type research <id>` (promoting an existing task to research). Setting `--justification` twice on a task whose notes already contain a `## Justification` heading is refused; clear or hand-edit notes first.

### Research-task field model

A research task's deliverable is *information*, not code — so its body fields carry different roles than a do-task's. Same fields, different jobs:

| Field     | On a research task holds…                                                                                      |
|-----------|--------------------------------------------------------------------------------------------------------------|
| `text`    | The research **request** — scope, the open questions to answer, the inputs to draw on, and the deliverable spec. This is the brief, written up front (where a do-task would hold its implementation plan). |
| `outcome` | The **deliverable** — the findings and decisions, plus pointers to the implementation work the research spawns (typically follow-up tasks). Set this at completion; research's only terminal status is `completed`. |

Keep large standalone deliverables — a full research report or decision document — as a file alongside the task (today, `docs/research-<date>-<slug>.md` or `docs/decision-<date>-<slug>.md`) and reference it from `outcome` rather than pasting the whole thing inline. The `outcome` then captures the conclusions and links to the report for the detail.

> This file-alongside convention is interim and expected to evolve toward per-task directories and typed content storage; the field roles above (`text` = request, `outcome` = deliverable) are the stable part.

---

## Updating tasks

```bash
endless task update <id> --title "New title"
endless task update <id> --description "..."
endless task update <id> --text /path/to/plan.md
endless task update <id> --status ready
endless task update <id> --phase later
endless task update <id> --tier 2
endless task update <id> --parent 444                # move under different parent
endless task update <id> --parent 0                  # make it a root
endless task update <id> --outcome "What was done"
endless task update <id> --decision "<rationale>"    # creates paired decision task
endless task update <id> <id2> ... --status ready    # bulk update
```

Attaching a non-empty plan (`--text`) to a `needs_plan` task auto-promotes the status to `ready`. Applies on both `task add` and `task update`. An explicit `--status` in the same call always wins.

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
endless task deps <id>                               # all relations for a task
endless task links <id>                              # show typed relations, grouped by type
endless task link <a> --to <b> --type implements     # create a typed link
endless task unlink <a> --to <b> --type implements
```

### When to use each relation type

| Type            | Use when                                                                                                                  |
|-----------------|---------------------------------------------------------------------------------------------------------------------------|
| `blocks`        | A's work cannot start (or cannot land) until B is done. Strict ordering. Use `task block` rather than `task link --type blocks` — it's the same thing with a friendlier surface. |
| `relates_to`    | A and B share context but neither blocks the other. The weakest typed link. Reach for it when nothing more specific fits. |
| `implements`    | A is the implementation of a plan, idea, or decision recorded in B. Common pattern: B is type=`plan` or type=`decision`, A is the work. |
| `cleans_up` / `cleaned_up_by` | A handles a loose end discovered while working on B. **This is the canonical "follow-up" link** — use it for follow-up tasks filed mid-stream. (We considered `follows_up` and rejected it in favor of `cleans_up` to keep the vocabulary tight.) |
| `documents`    | A is a decision that explains B. Auto-created when you use `--decision "..."` on `task add` / `task update`.              |
| `replaces`     | A supersedes B (B is now obsolete). Typically paired with `task replace`.                                                 |

**Quick decision tree:**

- *"B has to be done before A can land"* → `blocks`.
- *"I noticed an issue while doing B; here's a separate task A to fix it"* → `cleans_up`.
- *"A is the work and B is the spec/decision behind it"* → `implements` (or `documents` if B is a decision).
- *"They're related, no firm dependency"* → `relates_to`.

If you find yourself reaching for an undocumented type or `relates_to` for everything, that's a signal — surface it to the user.

---

## Sessions and chat

```bash
endless task chat                                    # start a chat-only session (no task tracking)
```

---

## Verbs

Verbs are the registered action words that may start a task title. When you `task add`, Endless validates that the title begins with a registered verb.

**Why:** verb-first titles enforce that every task names an action — "Fix login redirect", "Refactor task_cmd", "Document the guide command" — rather than vague nouns like "Login bug". This makes task lists readable at a glance.

```bash
endless verb list                                    # all registered verbs (project + machine layers)
endless verb add <verb>                              # register a new verb
endless verb remove <verb>                           # remove (with confirmation)
```

When the first word of a title isn't a registered verb, `task add` shells out to `claude --model haiku -p` and asks whether the word is a verb. On a `YES: <definition>` reply, Endless auto-registers the verb on the fly and lets the title pass — you'll see a `• Auto-registered verb '<word>': <definition>` line before the task-added line. On `NO` (or any failure: missing binary, timeout, malformed reply), `task add` falls through to the standard error.

When `task add` rejects a title:

1. **Pick an existing verb** — run `endless verb list`, find one that fits.
2. **Register a new verb manually** if haiku said NO but you disagree: `endless verb add <verb> --definition "..."`. Use sparingly — the verb list is a contract for readability.
3. **`--force`** bypasses validation. Don't habituate to this — it's an escape hatch, not a workflow.

Verbs are stored in `verbs.jsonl` at the project root (one JSON object per line) and auto-commit to `main` directly (they're treated as global-config artifacts, not task work). The line-oriented format lets git auto-merge concurrent verb additions via the `merge=union` driver in `.gitattributes`. A legacy `verbs.json` array, if present, is migrated to JSONL automatically on first load.
