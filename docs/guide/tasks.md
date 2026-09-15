# Tasks: Fields, CRUD, Verbs

Everything about manipulating the task tree: what each field is for, the full command surface, and the verb registry that validates titles.

---

## Task fields

Every task has multiple body fields. Knowing which to use prevents long descriptions that should have been plans, and short plans that should have been descriptions.

| Field         | Length         | Purpose                                                                                          | How to set                                         |
|---------------|----------------|--------------------------------------------------------------------------------------------------|----------------------------------------------------|
| `title`       | One line       | The task name. Verb-first (see Verbs below).                                                     | Positional arg on `task add`; `--title` on update. |
| `description` | < 200 words    | Brief pitch — *what* and *why* in a paragraph or two. Shown by default in `task list` / `task show`. | `--description` (inline) / `--description-file <path>` on `task add` / `task update`. |
| `plan`        | Long-form      | Full implementation plan: approach, the files and functions it touches, verification steps. Name files and functions, never line numbers — see **No time-frozen specifics** below. Shown with `task show --plan`. **On a research task, `plan` instead holds the research *request* — see the Research-task field model below.** | `--plan` (inline) / `--plan-file <path>` on `task add` / `task update`. |
| `analysis`    | Long-form      | Supporting research / exploration content that is *not* a proper plan — comparisons, findings, evidence gathered before the plan is written. Shown with `task show --analysis`. | `--analysis` (inline) / `--analysis-file <path>` on `task update`. |
| `notes`       | Freeform       | Catch-all for content that doesn't fit elsewhere. Use sparingly.                                 | DB column; CLI flag may not yet be wired.          |
| `outcome`     | Short to long  | Result / reason at terminal status. **Required** when completing a `research`/`brainstorm` task (the outcome IS the deliverable) and as the reason on `decline`. Optional on `confirm`/`assume`. | `--outcome` (inline) / `--outcome-file <path>` on `task confirm` / `task assume` / `task update`; `--reason` on `task decline` (stored as outcome). |

### Distinctions in practice

- **Loading a field from a file.** Every long-form field has a `--<field>-file <path>` twin. It refuses an empty or whitespace-only file rather than blanking what's there — see [An empty `--<field>-file` is refused](#an-empty---field-file-is-refused) below.
- **Description vs plan.** Description is a pitch — max 1024 character — readable in 30 seconds, fits in a list view. The plan is what you'd hand to an engineer. If you're writing four paragraphs into `--description`, stop — put it in a plan file and load with `--plan-file` (`--plan` stores its argument verbatim as inline content; pass a path to `--plan-file` to load a file).
- **Plan vs handoff.** The plan is for humans and for the spawned session, which `endless task spawn` directs it to read. The session's *opening input* (the handoff) is generated from a template at spawn time, not stored on the task; see `endless guide orchestration`.
- **Analysis vs plan.** Analysis is supporting evidence gathered *before a plan is written on a do-task* — comparisons, findings, raw material. The plan is the actionable part. A deliverable-shaped task (an audit, or a `research`-type task) puts its *result* in `outcome`, not `plan` or `analysis`; for research tasks specifically, see the Research-task field model below.
- **No time-frozen specifics.** No durable field — description, analysis, plan, outcome, or a decision body — may carry a byte count, a file size, a line number, a `file.ext:NNN` citation, a match count, or a sha of something that still moves. They have near-zero historical value and go stale the moment anything else lands, which leaves a later session unable to tell whether to trust them. Cite a durable identifier instead — a function, handler, command or symbol name — or better, state the SEARCH that finds the sites rather than the sites themselves. This is enforced on write, on both the inline flag and its `--<field>-file` twin, and there is no escape flag.
- **Outcome.** Single field for "how this task ended." Required where the *why* must be captured at the moment of the decision: completing a `research`/`brainstorm` task (the outcome IS the deliverable) and declining (the reason). Optional on `confirm`/`assume`, where "we tested it and it worked" rarely needs prose. `task decline` uses `--reason` as the CLI flag (stored as outcome internally).

---

## Viewing tasks

```bash
# List tasks for the current project
endless task list                                    # flat, sorted by ID
endless task list --all                              # include done items
endless task list --status ready                     # filter
endless task list --status unplanned,ready          # comma-separated
endless task list --phase now
endless task list --parent E-101
endless task list --parent none                      # roots only
endless task list --related-to <id> --rel-type blocks
endless task list --sort status                      # id, status, phase, tier, created, title
endless task list --llm                              # token-efficient agent output
endless task list --json

# Detail for one task
endless task show <id>
endless task show <id> --all-fields
endless task show <id> --plan
endless task show <id> --analysis
endless task show <id> --children
endless task show <id> --outcome
endless task show <id> --no-description
endless task show <id> --brief                       # previews, not bodies (256 chars)
endless task show <id> --brief=40                    # ...cut to 40 instead
endless task show <id> --llm
endless task show <id> --json                        # every body, no flag needed

# Has the work landed?
endless task landed                                  # tasks with a landing on record
endless task landed <id>                             # one task's landing history
endless task unlanded                                # finished tasks whose work has not

# Top actionable tasks, ranked
endless task next
endless task next --limit 5
endless task next --all                              # across all projects
endless task next --tier 1
endless task next --phase now
endless task next --llm

# Other reads
endless task id                                      # the task THIS session is on
endless task recent                                  # recently updated
endless task active                                  # underway + unverified + unreviewed
endless task search "query"                          # ID, title, description
endless task search "query" --plan                   # also search the plan field
endless task handoff <id>                            # render the spawn handoff
```

Reach for `--llm` whenever you're parsing output yourself — it's token-efficient.

`--json` carries every field's body with no flag passed. The display flags
(`--all-fields`, `--analysis`, `--plan`, `--outcome`, `--no-description`) shape
the terminal render only; they do not change the JSON. In it, `null` means one
thing — the field is empty — and `<field>_chars` is always an integer, `0` when
it is, so `<field>_chars == 0` and `<field> is null` are the same question.
Children are always counted (`children_count`, `children_by_type`); the child
list itself still needs `--children`.

`--brief[=N]` is the light payload: it cuts every long field to a preview
ending in `…` rather than omitting it, so a populated field stays a string and
its `_chars` still reports the true stored length. It means the same thing in
all three formats and wins over the display flags — `--all-fields --brief`
gives previews.

### A status says what was decided, never where the code is

A task status answers *what did we decide about this work* — verified, believed
done, declined. It does **not** answer *is the code on the base branch*. Reading
it that way costs real work: a task sitting at `assumed` with its fix on a branch
nobody landed looks finished from every listing, so the bug it fixed gets found
and fixed a second time weeks later.

So `task show` states landedness rather than implying it. For a finished task —
`confirmed`, `assumed`, `completed` — the `Landed:` line is **always** rendered,
in one of four forms:

```
Landed:     2026-08-30 10:16 pm  421726c     a landing is on record
Landed:     never                            nothing on record, nothing outstanding
Landed:     never — 3 unlanded commits       the branch still holds the work
Landed:     unknown — base branch unresolved the probe could not run
```

`never` is the value in both negative cases; the clause after the dash qualifies
it. `unknown` is a third value and never collapses into `never`: a probe that
could not run has not established anything.

Two commands answer the two follow-up questions, and they are easy to confuse:

- `endless task unlanded` — which finished TASKS claim to be done while their
  work has not reached the base branch. Two sections: work to land, and tasks
  with no landing on file at all.
- `endless task unsettled` — whether a WORKTREE is modified (commit or discard)
  or unlanded (land). See [orchestration](orchestration.md#why-a-worktree-is-unsettled-task-unsettled).

Landedness is measured by CONTENT, never by SHA: `worktree land` rebases, so a
landed commit keeps a different hash on the branch forever. Commits confined to
`.endless/` do not count as the task's work — they are Endless's own records.

### Every listing stops at 20 rows and says so

Every listing surface in Endless renders at most 20 rows — the task tree
(`task list`, `task search`, `task next`, `task recent`, `task landed`,
`task unsettled`, `epic list`, `decision list`), the sessions
(`session list`, `session search`, `session history`), the
registries (`project list`, `worktree list`, `verb list`, `phrase list`) and the
raw hatch (`endless sql`). When there are more, the last line says how many were
left out and how to see them:

```
… 1340 more rows (--no-limit)
```

- `--limit N` picks a different cap; `--no-limit` removes it. The two together
  are refused, and `--limit 0` is refused with a pointer to `--no-limit`.
- The count under a table (`35 match(es)`, `35 item(s)`) is the size of the
  **result**, not the height of the table. Those two numbers differing is the
  cap, working.
- **Do not pipe a listing to `head`.** That is what the cap replaces: `head` is
  silent, so a truncated result is indistinguishable from a complete one — which
  is how two searches once returned false "no existing task" answers.
- `--json` and `--tsv` are **uncapped**, because a program parsing them has no
  footer to read. An explicit `--limit` still caps them, and then the footer goes
  to stderr so the payload stays parseable.
- The footer prints where the missing rows *would be*. `session history` shows
  the newest messages oldest-first, so its footer is above the first line, not
  below the last; under `--sort asc` it moves to the bottom.

The one uncapped listing is `jobs list`: it renders inside the Go binary from a
compile-time registry, so it cannot grow with use.

`endless task id` is the one read that takes no id: it prints the task **your
own session** is on, as a single bare `E-NNNN` line, so a shell, a recipe or an
agent can compose it instead of asking you to retype an id you already claimed:

```bash
endless task show "$(endless task id)"
endless task update "$(endless task id)" --status unverified
```

It exits 1 with the reason on stderr when the session holds no task, so
`endless task id || ...` scripts cleanly. The binding is the one the tmux status
row reads — keyed by the pane the session runs in, so outside tmux there is
nothing to resolve. `endless tmux task` is an alias for it.

### Session provenance: who filed this, and who else worked it

`task show` traces a task back to the sessions that shaped it, so a task doubles as a navigational hub for jumping between them:

- The **`Created:`** line names the session that filed the task and the task that session was active on — `Created:  2026-08-04 4:38 am by ES-101 (E-102)`. Absent when the task was filed outside any session.
- The **`Touched by:`** block is the session-side peer of `This task:`, one row per session that ever touched the task, **most recent touch first**:

  ```
  Touched by:
  - Claimed:     ES-101 (E-102) [ended]
  - Revisited:   ES-102 (E-103) [idle]
  - Surfaced:    ES-103 (E-104) [idle]
  ```

  The leading relation says how the task entered that session's scope — **Claimed** (the session claimed it), **Surfaced** (created it), **Revisited** (touched it without claiming), or **Touched** for a historical row recorded before the vocabulary existed. The parenthesized id is the task that session is bound to *now*; `[state]` is the session's state, or `gone` when the session row itself no longer exists.

  **Claimed** is read off `sessions.task_id`, the write-once ownership record, not off the touch — so a session that filed a task and later claimed it reads `Claimed`, not the `Surfaced` its touch row was stamped with, and a session that claimed the task without ever recording a touch is listed too. That is the same column `task spawn` refuses on, so this block and that refusal cannot disagree.

Sessions render as **`ES-NNNN`** and tasks as `E-NNNN` — separate id spaces that would otherwise be indistinguishable side by side. Feed an `ES-NNNN` straight to `endless session goto ES-101` to jump there. `--json` reports the same facts as `created_by` / `touched_by`; `--llm` as `created_by=` / `touched_by=` lines.

---

## Adding tasks

```bash
endless task add "Title here"
endless task add "Title here" --parent <parent_id>
endless task add "Title here" --description "Brief pitch" --phase now
endless task add "Title here" --plan-file /path/to/plan.md --status ready
endless task add "Title here" --type bugfix          # todo|bugfix|research|epic|brainstorm
endless task add "Title here" --tier 1               # 1-4 or auto|quick|deep|discuss
endless task add "Title here" --blocked-by E-100     # also: --blocks, --relates-to,
                                                     # --implements, --cleans-up,
                                                     # --cleaned-up-by, --duplicates,
                                                     # --replaces (all repeatable)
```

`--duplicates` and `--replaces` are also on **`task update`** — the only relation
flags there, so an existing task can be marked without reaching for `task link`:

```bash
endless task update E-101 --duplicates E-102         # applies to every id named
endless task update E-9 --replaces E-5               # relation only — see below
```

Both record the relation and **nothing else**. `endless task replace <old> --by
<new>` remains the surface that also closes the replaced task (and knows to hold
a shipped status); `--replaces` deliberately does not, so it never closes
something you only meant to link.

To record a decision prompted by a task, use `endless decision add "..." --about <id>` — see `endless guide decisions`. (There is no `--decision` flag on `task add` or `task update`.)

Use the task ID printed by `task add` **literally**. IDs advance globally across parallel sessions — never guess.

### Work you discover mid-task (`--cleans-up`)

**Filing is one of five answers, not the default — and it is never yours alone.** When you spot a bug, a rough edge, or an obvious cleanup while working a task, run these tests in order — the handoff every spawned session receives carries the same five:

{{if .report_gate}}**1. Could it reasonably be done now?** Then do it, and do all three parts: note it in the commit message, record the grown scope on the task itself, and put it in your reply draft so your user learns the scope grew without having to read the diff:

```bash
# The task row is what posterity reads; the commit and the reply are transient.
endless task update <id> --plan-file <path> --keep-status
# Discoveries go in your reply draft; the minimizer decides what survives.
endless task report <id> --draft-file <path>
```
{{else}}**1. Could it reasonably be done now?** Then do it, and do all three parts: note it in the commit message, record the grown scope on the task itself (`endless task update <id> --plan-file <path> --keep-status`), and say so in your reply so your user learns the scope grew without having to read the diff.
{{end}}
**The test is cost and reviewer confusion, not kinship.** How big is the fix, and would carrying it in this diff mislead whoever reviews it? "Not a pure example of this task" is not a reason to file — a one-line fix in a file you already have open is cheaper to make than the task row describing it. Do not let purity get in the way of proficiency.

The bound this test really protects is **size**, not relatedness: a *large or risky* drive-by stays a separate task, because fixing it inline inflates the diff your user reviews, couples two unrelated changes into one land, hides the change the task was actually about, and expands the blast radius of a revert. A small unrelated fix does none of that.

Recording the grown scope is not optional. The commit message is found only by someone already reading that diff, and your reply dies with the session; the task row is what anyone reads later. A task whose plan describes only its original scope is a lie about what shipped under it. `--keep-status` says the edit records history rather than re-specifying the work.

**2. Is it a bug in work THIS session landed?** Then reopen the task that shipped it — `endless task update E-<id> --status revisit` — and fix it there. A defect in your own landed work is that task done wrong, not a new task. See [Fix a bug in your own landed work](orchestration.md#fix-a-bug-in-your-own-landed-work).

**3. Otherwise, ask your user before filing.** You have the context to explain a finding; you do not have the standing to rule on whether it earns a row. Filing reflexively inflates the backlog your user has to review, prioritize and schedule; dropping it silently loses a real finding. So put the decision in front of them, in this shape:

- **What you observed**, and why it would matter to someone who was not in this session. Enough to understand the purpose and no more — not a repro transcript, not a diff, not a plan.
- **The case for filing:** what goes unfixed or unremembered if it is dropped, who trips over it next, whether it compounds.
- **The case against:** the review and scheduling cost, whether an existing task already owns the area, whether it is a symptom of something already filed, whether noticing it again later costs less than carrying the row.
- **Your recommendation**, so your user is ratifying a judgement rather than doing the triage themselves.

They answer; you file or drop on that answer.

**4. Filing on that answer** — a new task linked back to the one you're on, and confirm with your user before implementing it:

```bash
endless task add "Verb-first title" --cleans-up <current_id> --description "What you saw and why it matters"
```

`cleans_up` is the canonical follow-up link (see [When to use each relation type](#when-to-use-each-relation-type)), so the discovery stays attached to the work that surfaced it and shows up as a follow-up on the parent task's `task show`.

**5. Filing more than one?** Check whether they share a root cause — file the cause, not each symptom. Two tasks that trace to one defect are one task; see [Lean toward FEWER tasks](#lean-toward-fewer-tasks) for why.

One case overrides test 1's bound: **a failure in a check you are obliged to run and report** — a red test, a lint error, a broken build in the project-wide regression you run before handing off. You cannot report that suite green while leaving it red, so fix the minimum that clears it even when the cause is otherwise out of scope, and say so explicitly in your report so the user isn't surprised by it in the diff. The override is about the obligation to report, not about being blocked: an unrelated red test rarely stops you finishing the task, and it is still yours to clear.

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
| `plan`    | The research **request** — scope, the open questions to answer, the inputs to draw on, and the deliverable spec. This is the brief, written up front (where a do-task would hold its implementation plan). |
| `outcome` | The **deliverable** — the findings and decisions, plus pointers to the implementation work the research spawns (typically follow-up tasks). Set this at completion; research's only terminal status is `completed`. |

Keep large standalone deliverables — a full research report or decision document — as a file alongside the task (today, `docs/research-<date>-<slug>.md` or `docs/decision-<date>-<slug>.md`) and reference it from `outcome` rather than pasting the whole thing inline. The `outcome` then captures the conclusions and links to the report for the detail.

> This file-alongside convention is interim and expected to evolve toward per-task directories and typed content storage; the field roles above (`plan` = request, `outcome` = deliverable) are the stable part.

### Brainstorm tasks (`--type brainstorm`)

A `brainstorm` task is the requester-led sibling of `research`. They produce the same *shape* of artifact — information, not code — but run on opposite information flow:

- **`research`** — information flows *inward*: the agent goes to external sources, gathers evidence, and synthesizes it out to the requester. Agent-led.
- **`brainstorm`** — information flows *outward from the requester*: their head is the primary source. The agent interviews, challenges, reflects, and captures. Requester-led, interview-mode.

**The behavioral contract — a session working a brainstorm task must:**

> Open by interviewing the requester. Ask questions first. Do **not** research autonomously or jump to a plan. Surface tensions, offer options, challenge the thinking, and capture ideas as they emerge. Conclude with a synthesis in `outcome` and spawn the follow-up tasks/decisions it produced.

Field model (mirrors the research model):

| Field     | On a brainstorm task holds…                                                                                   |
|-----------|--------------------------------------------------------------------------------------------------------------|
| `plan`    | The **seed / framing** — the spark, written up front: "I want to explore X; here's what's nagging me." A starting point, not a script. |
| `outcome` | The **synthesis** of what was landed on, plus `cleans_up` / `implements` links to the decision / research / do-tasks it spawned. Set at completion; a brainstorm's only terminal status is `completed` (with `--outcome`). |

**Ungated.** Unlike `research`, `brainstorm` requires no `--justification` — frictionless ideation is the point. A brainstorm is typically a *precursor* that resolves into an `endless decision add` and/or new tasks linked from its outcome. Because the type itself signals an information deliverable, `completed` does not require an investigation-category title verb (the same exemption epics get).

---

## Updating tasks

```bash
endless task update <id> --title "New title"
endless task update <id> --description "..."
endless task update <id> --plan-file /path/to/plan.md
endless task update <id> --status ready
endless task update <id> --phase later
endless task update <id> --tier 2
endless task update <id> --parent 444                # move under different parent
endless task update <id> --parent 0                  # make it a root
endless task update <id> --outcome "What was done"
endless task update <id> <id2> ... --status ready    # bulk update
endless task update <id> --plan-file <path> --keep-status   # edit, infer nothing
endless task update <id> --clear analysis            # empty a field, on purpose
```

Attaching a non-empty plan (`--plan`) to a `unplanned` task moves it to `submitted` (spec-complete, awaiting approval — **not** `ready`, which now means human-approved). Applies on both `task add` and `task update`. An explicit `--status` in the same call always wins. When the description alone is a sufficient spec (no plan), run `task submit <id>` to reach `submitted` directly. A human then runs `task approve <id>` to promote `submitted → ready`.

### `--keep-status`: edit the content, infer nothing

`task update` reads a status change out of what you edited, in three places:

| The edit | Infers |
|---|---|
| non-empty `--plan` on an `untriaged`/`unplanned` task | → `submitted` (plan attached = spec-complete) |
| a material `--description` change on a pre-work task | → `untriaged` (the spec every later judgment was made against changed) |
| `--tier 1` on an `untriaged`/`unplanned` task | → `ready` (tier 1 is exempt from planning and triage) |

**`--keep-status` suppresses all three.** The status you see is the status you keep. Reach for it when the edit is not a re-spec — a typo fix, a formatting pass, appending a finding to a plan that is deliberately parked at an unapproved status. Without it, a one-line append to an `unplanned` task's plan silently promotes it to `submitted`.

`--keep-status` cannot be combined with `--status`; the call is refused rather than silently resolved. Naming a status is already the explicit way to say what the status should be, and it wins over all three inferences on its own.

**Editing a done task's plan changes nothing but the plan.** There was a fourth inference: a real `--plan` change on a `confirmed`/`assumed`/`completed` task flipped it to `revisit`, reading the edit as unshipped scope. Recording what shipped is now an obligation (test 1 above), so that edit is routine and benign — and the flip cost a verification `revisit` cannot walk back. Reopening is still spelled `--status revisit`, and it is your user's call, not an inference.

### An empty `--<field>-file` is refused

`--description-file`, `--plan-file`, `--analysis-file` and `--outcome-file` write whatever the file holds. When the file comes back empty — a failed extraction, a `sed` that matched nothing — that used to replace the existing content with nothing and print a normal success line. It happened to a 3.5KB analysis, and the content survived only because the session still had it in context.

**Zero bytes is never a legitimate value for these fields**, so an empty *or whitespace-only* file is refused. The error names the path, so you can find the step that produced it:

```
Error: --analysis-file loaded no content from /tmp/extract.md (0 bytes).
  Refusing to blank analysis: an empty file is far more often a failed
  extraction than an intent to erase the field. Re-check the command
  that produced the file.
  To erase analysis on purpose, say so: --clear analysis
```

The refusal is unconditional. **There is no `--force`** — that is the whole design, not an omission. `--force` is exactly the flag a mistaken caller appends after reading a refusal, which would restore the failure mode with an audit trail claiming it was deliberate.

Emptying a field is a separate, explicit act:

```bash
endless task update <id> --clear analysis                  # repeatable
endless task update <id> --clear description --clear plan
```

`--clear` names the field it erases, so it cannot be produced by a pipeline that went wrong, and it is refused alongside that same field's `--<field>` / `--<field>-file` — two flags writing one column is the ambiguity the guard exists to remove. It is available on `task update`, `epic update` and `decision update`; not on `task add` or the status-transition verbs, where there is nothing yet to clear. The inline `--<field> ''` form still clears too, for the same reason `--clear` is safe: it names the field.

---

## Status transitions

```bash
endless task submit <id>                             # agent: unplanned/revisit → submitted (spec-complete, awaiting approval)
endless task approve <id>                            # human: submitted → ready (background sessions refused)
endless task claim <id>                              # ready → underway + create worktree
endless task update <id> --status revisit            # hand the task back (see `task release`: disabled)
endless task update <id> --status unverified             # work done, awaiting verification
endless task update <id> --status unreviewed --outcome "..."   # research/brainstorm: outcome written, awaiting the owner's read
endless task confirm <id> --outcome "..."            # user-only — sessions do not self-confirm
endless task confirm <id> --cascade --outcome "..."  # confirm a task and descendants
endless task assume <id> --outcome "..."             # believed complete, can't verify
endless task decline <id> --reason "..."             # active decision not to do
endless task replace <id> --by <new_id>              # supersede with another task
```

Found a bug in work you already landed? Reopen that task (`--status revisit`) instead of filing a new one — see [Fix a bug in your own landed work](orchestration.md#fix-a-bug-in-your-own-landed-work).

### Superseded work is `superseded`, never `obsolete`

**When something replaced the work, the record is the relation — not
`obsolete`.** The axis is *whether anything took over*, not whether the work
shipped. `obsolete` means **no longer needed, and nothing replaced it**; that is
the same line Endless draws for decisions, where `decision supersede --by` names
a successor and `decision obsolete --reason` says it "stopped applying and
nothing replaced it". Reaching for `obsolete` when there IS a successor throws
away the one fact worth keeping.

That fact is a relation **and** its own terminal — one command writes both:

```bash
endless task replace <old> --by <new>       # replaced_by recorded; status → superseded
```

`superseded` is refused unless the relation is actually there, which is the
mirror of the rule above: the status names a successor, so it needs one to name.
Set by hand on a task nothing replaced, it would send every reader looking for a
task that does not exist. Decisions have enforced the same shape for as long as
they have had the status — `decision supersede` requires `--by`.

`task replace` keeps a shipped task's status exactly as it stands (an unshipped
one becomes `superseded`) and records `replaced_by`. Holding the shipped
status is about not overwriting which terminal the work actually reached — the
supersession rides on the relation either way. A **terminal**
status then shows the supersession alongside it — `assumed (replaced by E-101)`
on `task show`'s `Status:` line, appended to the row in `session status`, and as
a `replaced_by` key in the `--llm` and `--json` modes of both. So a superseded
task reads as *handed on*, not *abandoned*, without anyone having to go looking
for its relations.

The human **tables** are the exception: `task list`, `task recent`,
`task search`, `task next`, `task active` and `task show --children` render the
bare status. They share one Status column across every row, so annotating a
handful of cells sized the column for all of them and took the difference out of
every title — a real cost for a fact one `task show` away.

**Shipped work CAN be `obsolete`.** Code that is being *deleted* rather than
superseded is obsolete in the plainest sense of the word: no longer in use.
There is no successor to name, so
`task replace` had no answer for it, and `declined` — an active decision not to
*do* the work — says something false about work that was built and landed. The
fact that it shipped is not lost by saying so: that lives in the landing record,
which is where `task show`'s `Landed:` line comes from.

---

## Triage (`untriaged` → `submitted` | `unplanned`)

Every new task is filed `untriaged` — nobody has looked at it. Triage moves it
one hop by answering one question: **is this description already a sufficient
spec?** Sufficient → `submitted` (awaiting the human's `approve`); not
sufficient → `unplanned` (design work first).

This is automatic. `endless task add` spawns the triage of that one task
detached, so an interactive filing is usually routed within seconds, and a
background sweep re-checks the whole queue every 15 minutes as the backstop.

```bash
endless triage run                                   # sweep this project's untriaged queue
endless triage run --task E-123                      # just this one
endless triage run --all-projects                    # every project (what the job does)
endless triage run --dry-run                         # print the calls, write nothing
```

Three properties are worth knowing when you are working alongside it:

- **It judges only what is written down.** The call sees the description, the
  parent, the sibling titles, and any linked decisions — never the transcript
  of the session that filed the task. That is deliberate: a description that
  only makes sense to whoever was in the room is not a sufficient spec, and
  triage is the thing that says so. Write the description for a stranger.
- **It fails open.** A model timeout, a missing `claude`, or an unparseable
  reply leaves the task `untriaged` and exits zero. Nothing is ever
  mis-transitioned because the model was unreachable.
- **You always win.** `endless task submit <id>` and
  `endless task update <id> --status unplanned` remain the override, and a task
  you route by hand is never overwritten by an in-flight triage call. Use them
  freely when you disagree with a call.

Attribution is queryable: a triage transition is recorded with
`actor.kind = triager`, and its payload carries the deciding model and the
model's one-line rationale.

Two levers. The wording lives in a template, so you can tune it without
touching product source — the render order is
`<project>/.endless/templates/triage/sufficiency.md.local.tmpl` (yours, never
committed) → `…/sufficiency.md.tmpl` (committed) → the shipped default. The
model is `models.triage` in `<project>/.endless/config.json` or your user
`config.json`, defaulting to `sonnet`. (`models.verb_check` resolves the same
way and drives the title verb check; it defaults to `haiku`.)

Set `ENDLESS_NO_TRIAGE=1` to suppress the automatic file-time path for one
process — what a test suite or a bulk import wants. An explicit
`endless triage run` still runs.

---

## Reporting to your user

{{if .report_gate}}**Every reply you send your user goes through the minimizer first.** Write the
reply you mean to send — exactly as you would send it, tables and code blocks
and all — to a file, then:

```bash
endless task report [<id>] --draft-file <path>
```

Send that command's output as your **entire final message, verbatim**. No
preamble, no framing sentence, nothing after it.

The task id is **optional**. Pass it to attribute the report; omit it when you
have nothing claimed. An unclaimed quick question is exactly where sprawl
happens, so the channel covers it too. The command never changes the task's
status.

### Why a second party

The previous design took a structured payload — `verify`, `notes`, `questions` —
and rendered a block you appended to your own prose. Two output channels
existed, so content landed in the cheap one: sessions wrote the verify command
in prose and then told `task report` there was nothing to report.

Every variant of that design fails identically, because in all of them the agent
decides what to volunteer — which means the agent is judging its own output in
the same breath as writing it, and judging generously. An adversarial minimizer
is a **second party**. That is the whole fix.

### It is not a length limit

Its objective is to delete what your user did not ask for, not to make the
reply short. A discussion your user asked for survives at whatever length it
takes; a single question gets a single answer and nothing else, however well
written.

Four invariants are guaranteed: markdown tables survive byte for byte, fenced
code blocks survive byte for byte, a command your user is meant to RUN always
survives, and a direct question gets its direct answer.

### Your draft is never lost

```bash
endless task report --raw        # prints your draft back, unchanged
```

This is what lets the minimizer be maximally aggressive at zero risk — nothing
is destroyed, only hidden. If it cut something your user genuinely needs, you
get **one appeal per turn**: re-run with a draft that argues for the missing
content. The appeal goes through the minimizer too.

### It is enforced

A Stop hook blocks a final message that differs from the command's output, and
blocks a turn that produced a reply without running the command at all. The
second is the one that matters — an enforcement you only meet by opting in
enforces nothing.

The gate fails open wherever it cannot prove a violation: unregistered projects,
Agent-tool subagents (their final message is a return value to the parent, not a
handoff), turns with no assistant text, and any session it cannot resolve. When
its bounce budget is spent it lets the turn end and **says so to the user**, so
a surrender is never mistaken for compliance.

Switch it off per project with `"report_gate": false` in `.endless/config.json`.
It defaults **on**, and it deliberately does not live in `.claude/settings.json`
— a gate an agent can switch off in the course of normal work is not a gate.

The channel runs only under an agent harness Endless **supports** — today,
Claude Code in a terminal, and nothing else. A session in the Claude Code
Desktop app, an IDE extension, or any other host is neither told to use the
channel nor gated by it, whatever `report_gate` says: the two are independent
vetoes and both must say yes. It is an allow-list, so a harness nobody has seen
yet lands outside the channel rather than silently inside it.

One consequence worth knowing: `endless task spawn` opens a **tmux window**, so
the session it hands off to is a terminal Claude Code one and gets the contract
regardless of which harness ran the command. `endless task claim` renders its
handoff in the claiming session's own hook, so that one does follow the harness.

### Telling the minimizer how it did

Your user annotates a reply by writing a **`$TOKEN`** as the first token of a
line, optionally scoped to a quoted span:

```
$BLOAT "the whole second paragraph"
$JARGON "load-bearing" and "at its core" — stop using these
$GOOD
```

**The vocabulary is open.** Any word works; the four that ship (`$CUT`,
`$BLOAT`, `$WRONG`, `$GOOD`) are examples, not a list. A closed vocabulary is
only worth its consistency if the user can recall it mid-complaint, and one they
cannot recall produces no label at all — which is strictly worse, because label
supply is upstream of everything else in the loop. Consistency is leaned on
rather than enforced: when a new token clusters near one already in use, you will
be asked in band whether the two mean the same thing. Ask your user; they may
answer or ignore.

Every label attaches to the preceding turn's corpus row. A bare token is
recorded, not refused — under a free vocabulary the word IS the account.

The sigil is what buys immunity, and it is why the token must lead the line.
`WRONG:` and `GOOD:` at line start are exactly what a user naturally types as a
prose label; one character makes the signal unambiguous. `CUT the scope` does not
fire, a fenced code block containing `$PATH` does not fire, and `$PATH=/usr/bin`
does not fire.

Labels do **not** score prompts. They calibrate the judge that does — see
`endless minimizer status`.

### When the output arrives as two options

On a sampled fraction of turns `task report` emits **two** minimizations of your
draft rather than one:

```
─[Option A of B]────────────────────────────────
…
─[Option B of B]────────────────────────────────
…
```

Send it verbatim, exactly as you would send one. **You do not pick** — choosing
one yourself destroys the comparison, which is the only place the loop gets a
real counterfactual. Your user replies `$A` or `$B`, and may add a span:
`$B "this sentence"` means "B wins, and that span is still bloat".

Each option is previewed inline; either can be read in full with
`endless session turn A -p` / `endless session turn B -p`. The pair also asks
whether your user wants these more or less often (`$MORE` / `$LESS`) — that is
the only control over the sample rate, and it is deliberately not a config knob.

### The `$FULL` escape hatch

When your user wants an answer that bypasses the minimizer entirely, they type
**`$FULL`** as the first token of a line. That licenses **one** response,
answered fully and unconstrained — it does not go through the minimizer at all.

`$FULL` is a *directive*, not a label, so unlike a token it may stand alone or
carry the question with it: `$FULL why did the rebase conflict?` The other
directives are `$A` / `$B` (pick between paired options) and `$MORE` / `$LESS`
(how often to see pairs).

It is **not a mode switch**. The next turn returns to the default.
{{else}}**This project has the report channel off** — `"report_gate": false` in
`.endless/config.json`. There is no `endless task report` step here and no Stop
hook holding your turn against one. Write your reply and send it.

The command still runs if you invoke it — a gate-off project is opting out of
enforcement, not banning the command — but nothing routes you to it and nothing
reads what it returns, so nothing here sends you there.

What your user is owed does not change. A command they have to RUN, a table, a
fenced code block, and a direct answer to a direct question all have to reach
them, whatever else you leave out.

Turning the channel on is one key: drop `"report_gate": false` from
`.endless/config.json`, or set it to `true`. It defaults **on**, and it
deliberately does not live in `.claude/settings.json` — a gate an agent can
switch off in the course of normal work is not a gate.

The channel also runs only under an agent harness Endless **supports** — today,
Claude Code in a terminal, and nothing else. The two are independent vetoes and
both must say yes.
{{end}}
---

{{if .report_gate}}## The minimizer's autoresearch loop (`endless minimizer`)

The minimize prompt is not a constant. A background job scores every reported
turn, generates challenger prompts, replays them against the champion over a
frozen slice of the corpus, and promotes the winner. Nobody approves a promotion
— the user supplies ground truth in the flow of work (labels, and picks between
paired options) and the loop improves itself around them.

You will rarely type any of this. It matters to you for two reasons: the prompt
your draft meets today may not be the one it met last week, and `--raw` is still
the answer when something is missing.

```bash
endless minimizer status              # champions, sampling, judge calibration
endless minimizer variants            # the prompt lineage; `*` is in force
endless minimizer show <hash>         # one variant in full
endless minimizer rollback            # undo a promotion — a pointer move
```

Three things are tuned jointly, per task type: the prompt text, a JSON **fetch
policy** (what the minimizer is shown of what the user already has — their task
plan, their session status, the replies they already read), and a **bypass
threshold** below which a short draft skips the minimizer entirely.

Promotion is decided by paired replay over a frozen corpus, never by a rolling
average. Rolling metrics — `status` prints keep-ratio by draft size — are an
alarm, not a verdict: a rolling mean improves whenever the work gets easier.

Turn the loop off per project without turning the gate off:

```json
{"minimizer": {"enabled": true, "optimizer": false}}
```

`"report_gate": false` is still read as `{"enabled": false}` so a project that
opted out under the old name stays opted out.

---

{{end}}## Removing and moving

```bash
endless task remove <id>                             # warns if it has children
endless task remove <id> --cascade                   # also remove descendants
endless task list --removed                          # what has been removed
endless task move <id> --parent <parent_id>
endless task move <id> --root
endless task move --children-of <id> --root
endless task clear <id> --<field>                    # clear a single field
```

**`remove` does not delete the row — it marks it removed.** The id is therefore
never re-minted: the allocator counts past every removed task, so an id that was
used once is used once forever.

That matters because several tables deliberately outlive their task and carry no
foreign key on it — `session_tasks`, `session_notices`, `task_landings`. While
ids were re-freed, a later task taking a freed id silently inherited those rows
and reported them as fact. Retention closes that at the source.

What you see:

- The task disappears from every listing — `task list`, `task next`,
  `session status`, the monitor, search.
- **`endless task show <id>` still renders it**, marked `⊘ REMOVED`. An id that
  is now a hole explains itself instead of erroring.
- **`endless task list --removed`** lists the removed set. It *replaces* the live
  listing rather than adding to it, so the two never interleave.
- Undelivered change notices about the task are dropped (delivered history
  stays), and any session pointing at it has its active task cleared. Landing
  history survives — it is audit data, and the retained row is what explains it.

**`remove` refuses while the task still has relations.** Relation rows
carry no foreign key on their task endpoint, so they survive the removal. The
refusal names the exact `unlink` command that clears each one:

```
E-101 has 2 relation(s).
Removing would orphan them — relation rows survive a task delete, and
task ids are reused, so a later task inheriting one of these ids would
inherit its relations too. Unlink them first:

    endless task unlink E-101 --to E-102 --type cleans_up
    endless decision unlink ED-42 --to E-101 --type documents
```

Deny rather than cascade: a severed relation cannot be reconstructed, and a
refusal costs one command. Every relation type, no exemption — `relates_to`
included. There is deliberately no flag to remove a task *and* its relations in
one step; `--cascade` is about children and only widens which tasks get checked
(the whole descendant set, so removing a parent cannot bypass the guard).

The same guard applies to **`task import --replace`** and
**`task import-json --clear`**, which remove tasks by source file
rather than by id. An imported task that has since been linked to is no longer
disposable just because the file regenerated it — clear the relation, or
re-import without the flag. Bulk clear retains its rows too: one rule, no second
path that can orphan anything.

Rows orphaned before this landed are cleaned up by `reconcile` — which runs on
`endless project list` / `project scan` — and it prints what it removed.

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
| `replaces`     | A supersedes B. Record it with `task replace B --by A`, which sets B to `superseded` — or holds B's status if B's work already shipped, since the terminal it earned is still true. |
| `duplicates` / `duplicated_by` | A and B were filed for the **same concern** — two descriptions of one piece of work, not two pieces. A is the redundant filing; B is the one kept. |

**Quick decision tree:**

- *"B has to be done before A can land"* → `blocks`.
- *"I noticed an issue while doing B; here's a separate task A to fix it"* → `cleans_up`.
- *"A is the work and B is the spec/decision behind it"* → `implements` (or `documents` if B is a decision).
- *"A and B are the same task filed twice"* → `duplicates`.
- *"They're related, no firm dependency"* → `relates_to`.

If you find yourself reaching for an undocumented type or `relates_to` for everything, that's a signal — surface it to the user.

### `duplicates` vs `replaces` vs `relates_to`

The three are easy to confuse, and picking the wrong one loses the fact you were
trying to record:

- `replaces` says B **was** the work and A **took over** from it — the concern
  moved, usually because B's approach was wrong or its scope changed. Two
  distinct pieces of work, one handing off to the other.
- `duplicates` says there was only ever **one** piece of work, described twice.
  Nothing handed off; a second filing simply should not exist.
- `relates_to` says they share context. It is true of duplicates too, which is
  why it is the wrong answer — it is true of almost everything, so it records
  nothing.

Recorded, not enforced: linking `duplicates` changes no status. Close the
redundant task separately — `obsolete` when nothing replaced it, and for work that
already shipped the relation *is* the record, exactly as with `replaces` (see
the `obsolete` row in [Task statuses](index.md#task-statuses)).

```bash
endless task link E-101 --to E-102 --type duplicates     # E-101 is the redundant filing
endless task link E-102 --to E-101 --type duplicated_by  # same row, written from the keeper's side
endless task update E-101 --duplicates E-102             # same fact, no --type to remember
```

Once the redundant task **is** closed, the relation rides along with its status
— `obsolete (duplicates E-102)` in `task show` and `session status`, and as a
`duplicates` key in their `--llm` and `--json` modes. This is the same rule
`replaces` follows, applied for the same reason: a terminal status reads as the
end of the story, and `obsolete` alone says "no longer needs doing" rather than
"already being done over there". It follows that rule's exception too: the human tables
render the bare status, for the column-width reason given above.

The note appears **only** beside a terminal status, and only on the redundant
task — never on the one that was kept. `--json` emits it either way, because
JSON is data and a display rule has no business hiding a fact from a consumer.

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
endless verb update <verb>                           # correct a registered verb's definition or category
endless verb remove <verb>                           # remove (with confirmation)
```

`verb update` changes only the fields you pass — `--definition`, `--category`
(repeatable), or both — and leaves everything else exactly as it was. Reach for
it instead of remove-then-add, where a field you forget to restate is silently
rewritten rather than left alone; that is how `brainstorm` came to be
registered as an `action` verb, which then made a `--type brainstorm` task
refuse a title led by the word "brainstorm". `--category` replaces the whole
set, so `--category action --category investigation` is how a verb becomes a
dual.

The correction is written to the layer you are addressing — the project's
`verbs.jsonl` by default, the machine's under `--machine-only` — even when that
layer had no entry for the verb. A built-in verb corrected that way gets a new
line carrying only the corrected fields; the rest still resolves from the
built-in, so `endless verb update research --category action` keeps
`research`'s definition. The other layer is kept in step only when it already
carries the verb, so correcting a verb that arrived with a clone does not
install it machine-wide.

When the first word of a title isn't a registered verb, `task add` shells out to `claude --model haiku -p` and asks whether the word is a verb. On a `YES: <definition>` reply, Endless auto-registers the verb on the fly and lets the title pass — you'll see a `• Auto-registered verb '<word>': <definition>` line before the task-added line. On `NO` (or any failure: missing binary, timeout, malformed reply), `task add` falls through to the standard error.

When `task add` rejects a title:

1. **Pick an existing verb** — run `endless verb list`, find one that fits.
2. **Register a new verb manually** if haiku said NO but you disagree: `endless verb add <verb> --definition "..."`. Use sparingly — the verb list is a contract for readability.
3. **`--force`** bypasses validation. Don't habituate to this — it's an escape hatch, not a workflow.

Verbs are stored in `verbs.jsonl` at the project root (one JSON object per line) and auto-commit to `main` directly (they're treated as global-config artifacts, not task work). The line-oriented format lets git auto-merge concurrent verb additions via the `merge=union` driver in `.gitattributes`. A legacy `verbs.json` array, if present, is migrated to JSONL automatically on first load.
