# Tasks: Fields, CRUD, Verbs

Everything about manipulating the task tree: what each field is for, the full command surface, and the verb registry that validates titles.

---

## Task fields

Every task has multiple body fields. Knowing which to use prevents long descriptions that should have been plans, and short plans that should have been descriptions.

| Field         | Length         | Purpose                                                                                          | How to set                                         |
|---------------|----------------|--------------------------------------------------------------------------------------------------|----------------------------------------------------|
| `title`       | One line       | The task name. Verb-first (see Verbs below).                                                     | Positional arg on `task add`; `--title` on update. |
| `description` | < 200 words    | Brief pitch — *what* and *why* in a paragraph or two. Shown by default in `task list` / `task show`. | `--description` (inline) / `--description-file <path>` on `task add` / `task update`. |
| `text`        | Long-form      | Full implementation plan, including approach, file paths, verification steps. Shown with `task show --text`. **On a research task, `text` instead holds the research *request* — see the Research-task field model below.** | `--text` (inline) / `--text-file <path>` on `task add` / `task update`. |
| `analysis`    | Long-form      | Supporting research / exploration content that is *not* a proper plan — comparisons, findings, evidence gathered before the plan is written. | `--analysis` (inline) / `--analysis-file <path>` on `task update`. |
| `notes`       | Freeform       | Catch-all for content that doesn't fit elsewhere. Use sparingly.                                 | DB column; CLI flag may not yet be wired.          |
| `outcome`     | Short to long  | Result / reason at terminal status. **Required** when completing a `research`/`brainstorm` task (the outcome IS the deliverable) and as the reason on `decline`. Optional on `confirm`/`assume`. | `--outcome` (inline) / `--outcome-file <path>` on `task confirm` / `task assume` / `task update`; `--reason` on `task decline` (stored as outcome). |

### Distinctions in practice

- **Description vs text.** Description is a pitch — max 1024 character — readable in 30 seconds, fits in a list view. Text is the plan you'd hand to an engineer. If you're writing four paragraphs into `--description`, stop — put it in a plan file and load with `--text-file` (`--text` stores its argument verbatim as inline content; pass a path to `--text-file` to load a file).
- **Text vs handoff.** Text is the plan — for humans and for the spawned session, which `endless task spawn` directs it to read. The session's *opening input* (the handoff) is generated from a template at spawn time, not stored on the task; see `endless guide orchestration`.
- **Analysis vs text.** Analysis is supporting evidence gathered *before a plan is written on a do-task* — comparisons, findings, raw material. Text is the actionable plan. A deliverable-shaped task (an audit, or a `research`-type task) puts its *result* in `outcome`, not `text` or `analysis`; for research tasks specifically, see the Research-task field model below.
- **Outcome.** Single field for "how this task ended." Required where the *why* must be captured at the moment of the decision: completing a `research`/`brainstorm` task (the outcome IS the deliverable) and declining (the reason). Optional on `confirm`/`assume`, where "we tested it and it worked" rarely needs prose. `task decline` uses `--reason` as the CLI flag (stored as outcome internally).

---

## Viewing tasks

```bash
# List tasks for the current project
endless task list                                    # flat, sorted by ID
endless task list --tree                             # hierarchical
endless task list --all                              # include done items
endless task list --status ready                     # filter
endless task list --status unplanned,ready          # comma-separated
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
endless task active                                  # underway + unverified
endless task search "query"                          # ID, title, description
endless task search "query" --text                   # also search text field
endless task handoff <id>                            # render the spawn handoff
```

Reach for `--llm` whenever you're parsing output yourself — it's token-efficient.

### Session provenance: who filed this, and who else worked it

`task show` traces a task back to the sessions that shaped it, so a task doubles as a navigational hub for jumping between them:

- The **`Created:`** line names the session that filed the task and the task that session was active on — `Created:  2026-08-04 4:38 am by ES-1020 (E-1865)`. Absent when the task was filed outside any session.
- The **`Touched by:`** block is the session-side peer of `This task:`, one row per session that ever touched the task, **most recent touch first**:

  ```
  Touched by:
  - Revisited:   ES-996 (E-1833) [idle]
  - Surfaced:    ES-994 (E-1829) [idle]
  ```

  The leading relation says how the task entered that session's scope — **Goal** (the session claimed it), **Surfaced** (created it), **Revisited** (touched it without claiming), or **Touched** for a historical row recorded before the vocabulary existed. The parenthesized id is the task that session is active on *now*; `[state]` is the session's state, or `gone` when the session row itself no longer exists.

Sessions render as **`ES-NNNN`** and tasks as `E-NNNN` — separate id spaces that would otherwise be indistinguishable side by side. Feed an `ES-NNNN` straight to `endless session goto ES-1020` to jump there. `--json` reports the same facts as `created_by` / `touched_by`; `--llm` as `created_by=` / `touched_by=` lines.

---

## Adding tasks

```bash
endless task add "Title here"
endless task add "Title here" --parent <parent_id>
endless task add "Title here" --description "Brief pitch" --phase now
endless task add "Title here" --text-file /path/to/plan.md --status ready
endless task add "Title here" --type bugfix          # todo|bugfix|research|epic|brainstorm
endless task add "Title here" --tier 1               # 1-4 or auto|quick|deep|discuss
endless task add "Title here" --blocked-by E-100     # also: --blocks, --relates-to,
                                                     # --implements, --cleans-up,
                                                     # --cleaned-up-by (all repeatable)
```

To record a decision prompted by a task, use `endless decision add "..." --about <id>` — see `endless guide decisions`. (There is no `--decision` flag on `task add` or `task update`.)

Use the task ID printed by `task add` **literally**. IDs advance globally across parallel sessions — never guess.

### Filing work you discover mid-task (`--cleans-up`)

**File it; don't fix it.** When you spot a bug, a rough edge, or an obvious cleanup while working a task, the default is a new task linked back to the one you're on — *not* an inline fix:

```bash
endless task add "Verb-first title" --cleans-up <current_id> --description "What you saw and why it matters"
```

`cleans_up` is the canonical follow-up link (see [When to use each relation type](#when-to-use-each-relation-type)), so the discovery stays attached to the work that surfaced it and shows up as a follow-up on the parent task's `task show`.

Fixing a drive-by inline looks helpful and isn't: it inflates the diff your user reviews, couples two unrelated changes into one land, hides the change the task was actually about, and expands the blast radius of a revert. Stay on the task you claimed.

The exception is a drive-by you genuinely cannot complete the task without — a broken build, a test that fails for an unrelated reason. Fix the minimum that unblocks you, and say so explicitly in your report so the user isn't surprised by it in the diff.

### Lean toward FEWER tasks

When you file discovered work, prefer **one** task over several whenever one is reasonable.

Every task you file spends your user's attention — review, prioritization, scheduling — which is the scarce resource Endless exists to protect. Several near-identical tasks cost several times the attention of one task covering the same ground, while delivering the same result. A backlog inflated with split hairs makes Endless *cost* time instead of saving it.

Split into separate tasks only when the items genuinely need **different reviewers, different decisions, or different land timing**. The rule of thumb: same file + same kind of edit + same reviewer = **one** task.

Four near-identical "document X in the guide" tasks are the anti-pattern; folding them into one task with four sub-points is the pattern. The same logic applies to any set of edits a single reviewer would approve in a single pass.

### Research-type gate

`--type research` discourages casual use: a research task is justified only when its findings can't be inlined as a do-task. The CLI enforces this:

- **Exempt:** `--parent <id>` where `<id>` is an `--type epic --status underway` task. No `--justification` required.
- **Otherwise:** `--justification "<reason>"` is required and stored under a `## Justification` heading in the task's notes.

```bash
# Exempt: parent is an underway epic
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

### Brainstorm tasks (`--type brainstorm`)

A `brainstorm` task is the requester-led sibling of `research`. They produce the same *shape* of artifact — information, not code — but run on opposite information flow:

- **`research`** — information flows *inward*: the agent goes to external sources, gathers evidence, and synthesizes it out to the requester. Agent-led.
- **`brainstorm`** — information flows *outward from the requester*: their head is the primary source. The agent interviews, challenges, reflects, and captures. Requester-led, interview-mode.

**The behavioral contract — a session working a brainstorm task must:**

> Open by interviewing the requester. Ask questions first. Do **not** research autonomously or jump to a plan. Surface tensions, offer options, challenge the thinking, and capture ideas as they emerge. Conclude with a synthesis in `outcome` and spawn the follow-up tasks/decisions it produced.

Field model (mirrors the research model):

| Field     | On a brainstorm task holds…                                                                                   |
|-----------|--------------------------------------------------------------------------------------------------------------|
| `text`    | The **seed / framing** — the spark, written up front: "I want to explore X; here's what's nagging me." A starting point, not a script. |
| `outcome` | The **synthesis** of what was landed on, plus `cleans_up` / `implements` links to the decision / research / do-tasks it spawned. Set at completion; a brainstorm's only terminal status is `completed` (with `--outcome`). |

**Ungated.** Unlike `research`, `brainstorm` requires no `--justification` — frictionless ideation is the point. A brainstorm is typically a *precursor* that resolves into an `endless decision add` and/or new tasks linked from its outcome. Because the type itself signals an information deliverable, `completed` does not require a `completable`-marked title verb (the same exemption epics get).

---

## Updating tasks

```bash
endless task update <id> --title "New title"
endless task update <id> --description "..."
endless task update <id> --text-file /path/to/plan.md
endless task update <id> --status ready
endless task update <id> --phase later
endless task update <id> --tier 2
endless task update <id> --parent 444                # move under different parent
endless task update <id> --parent 0                  # make it a root
endless task update <id> --outcome "What was done"
endless task update <id> <id2> ... --status ready    # bulk update
```

Attaching a non-empty plan (`--text`) to a `unplanned` task moves it to `submitted` (spec-complete, awaiting approval — **not** `ready`, which now means human-approved). Applies on both `task add` and `task update`. An explicit `--status` in the same call always wins. When the description alone is a sufficient spec (no plan text), run `task submit <id>` to reach `submitted` directly. A human then runs `task approve <id>` to promote `submitted → ready`.

---

## Status transitions

```bash
endless task submit <id>                             # agent: unplanned/revisit → submitted (spec-complete, awaiting approval)
endless task approve <id>                            # human: submitted → ready (background sessions refused)
endless task claim <id>                              # ready → underway + create worktree
endless task release [<id>]                          # release current session's claim
endless task update <id> --status unverified             # work done, awaiting verification
endless task confirm <id> --outcome "..."            # user-only — sessions do not self-confirm
endless task confirm <id> --cascade --outcome "..."  # confirm a task and descendants
endless task assume <id> --outcome "..."             # believed complete, can't verify
endless task decline <id> --reason "..."             # active decision not to do
endless task replace <id> --by <new_id>              # supersede with another task
```

---

## Reporting to your user

At **any in-session user-facing checkpoint** — a terminal status, a status
request, "here's where the work stands", a blocker, a decision you need — route
the update through this command rather than composing one by hand:

```bash
endless task report <id>
```

It reports **only what your user could not already compute** — the follow-ups
you filed, an epic's children, your gated notes/questions, and unexpected
worktree state — and prints a steering prompt telling you to relay that
**verbatim, plainly, no ceremony**. It is status-agnostic (run it at whatever
status you reached, mid-session or at the end) and **does not change the task's
status**.

Status, landing, and parentage are deliberately **not** in the output: `task
show` and `session status` already render them, and the handoff tells you not to
recap them — the command holds itself to the same bar it enforces on your notes
(E-1880). So a clean session's report is **empty**, and steers you to say
nothing rather than manufacture a summary. Say what you delivered; stop.

**Report by default, at every checkpoint.** The rule is functional, not a list
of situations: acceptable content is a computed fact the user cannot derive on
their own, XOR a genuine open decision they must make — otherwise say nothing.
Don't enumerate the moments this applies to (any such list drifts the moment a
new surface appears); judge each checkpoint by that function. Route the update
through this command and relay its output as-is; don't write it as freeform
prose. The normal path takes **no payload**.

For a genuinely out-of-band note (an anomaly or discovery the command can't
compute) or an open question for the user, pass `--json` / `--json-file`. The
exact payload shape lives in `endless task report --help` — the single canonical
home; read it there rather than duplicating it here.

### The `FULL STATUS` escape hatch

Reports are deliberately terse, and spawned sessions are told not to recap
status, phase, or relationships. When the user wants the full picture anyway,
they type **`FULL STATUS`**.

That keyword licenses **one** response, answered fully and unconstrained: recap
whatever is useful, at whatever length the answer needs, ignoring the
report-only-what-it-can't-compute discipline for that reply.

It is **not a mode switch**. The response after it returns to the default
terseness. If the user wants another full answer, they type it again.

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
| `documents`    | A is a decision that explains B. Auto-created when you pass `--about <task>` to `endless decision add`.              |
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
