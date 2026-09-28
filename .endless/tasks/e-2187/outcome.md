# How task titles and descriptions are actually used

Corpus: every task in the main database, including terminal and removed ones —
1,419 tasks (1,411 live + 8 removed), 1,357 with a non-empty description
(692K characters in all). Read via `endless task list/show --json --db main`.
No task was changed.

The per-task proposal is committed at `.endless/tasks/e-2187/rewrites.jsonl`
(one row per task, schema as planned, plus `title.tags`).

## Headline

- **Only 30 of 1,357 descriptions (2%) are already a pure-WHAT, id-free blurb of
  256 characters or fewer.** Only 227 (17%) are within 256 at all. Median length is
  497; the 90th percentile is 858.
- **The WHAT is 29% of description text.** The other 71% is HOW (20%), context
  about the world (background, status quo and evidence, 30%), and a long tail of
  rationale, relationships, scope, questions and history.
- **It is getting worse over time.** The median description grew from 203 characters
  (E-441..900) to 708 (E-2001..2187). Over the same span the median title grew from
  46 to 75 characters. The WHAT share fell from 35% to 24%, while status-quo and
  background rose from 13% to 34%.
- **59% of descriptions cite a task or decision id** (795 of 1,357; 58 cite an
  ED-; 9 use bare `#NNN`; up to 32 ids in one description). **6% of titles do**
  (81 of 1,419).
- **Every task could be rewritten within title ≤ 60 / description ≤ 256.** The
  proposed titles have a p95 of 59 characters. The proposed descriptions have a
  median of 158 and a p95 of 202, and none needed more than 256.
- **Keep the 256 cap.** The evidence does not argue for a different number (see "The cap").
- **One new content name is justified: `context`.** Everything else displaced has
  an existing home.

## Why descriptions grew: three causes in the product, not just habit

Rules alone will not hold unless these three causes change with them:

1. **The guide invites it.** `docs/guide/tasks.md` defines description as "Brief
   pitch — *what* and *why*… < 200 words", and says "max 1024 character" elsewhere. The
   word "why" is exactly the license for background, status quo and rationale, which
   together make up the largest displaced mass. 200 words is about 1,200 characters, not 256.
2. **The description-sufficiency check reads only the description.** It asks "is
   this description already a sufficient spec?", so an agent wanting to skip planning stuffs a mini-plan and
   backstory into the description. This check is slated for removal once a plan is required
   before spawn, which removes the incentive; no change to it is needed for the cap.
3. **A description edit is a re-spec.** A material `--description` change on a
   pre-work task resets it to `untriaged`. `task update --keep-status` already suppresses
   that reset (and every other inferred transition), so applying the rewrites needs no new
   bypass — see "The artifact and how to apply it".

## Classification

Categories are fixed and are recorded per segment in the artifact, so a later
renaming of the destinations can be recomputed without re-classifying:

| category | definition |
|---|---|
| `what` | the change/deliverable itself; for a bug, the defect statement |
| `background` | context, the incident that prompted it, why it matters |
| `status-quo` | how the system or workflow behaves today (as mechanics, not as the defect) |
| `evidence` | repro, error text, observed output, counts gathered |
| `approach` | HOW: mechanism, steps, files/functions/tables/flags, design detail |
| `rationale` | why this approach, alternatives rejected, tradeoffs |
| `scope` | boundaries, non-goals, "only X", deferral conditions |
| `acceptance` | done-when, what tests must show |
| `open-question` | unresolved questions |
| `relation` | the relationship to other tasks/decisions (blocks, split from, see E-NNN) |
| `history` | progress, what already landed, dated updates, completion reports |

Results for the 1,357 non-empty descriptions:

| category | tasks containing it | % of tasks | chars | % of all description text | median chars when present |
|---|---|---|---|---|---|
| what | 1,238 | 91% | 198K | 29% | 149 |
| approach | 734 | 54% | 139K | 20% | 158 |
| background | 537 | 40% | 80K | 12% | 129 |
| status-quo | 462 | 34% | 81K | 12% | 159 |
| relation | 405 | 30% | 34K | 5% | 67 |
| scope | 273 | 20% | 26K | 4% | 79 |
| rationale | 268 | 20% | 34K | 5% | 107 |
| evidence | 241 | 18% | 44K | 6% | 156 |
| open-question | 145 | 11% | 24K | 3% | 140 |
| history | 115 | 8% | 16K | 2% | 105 |
| acceptance | 101 | 7% | 11K | 2% | 84 |

- **119 descriptions (9%) have no WHAT sentence at all.** They are pure
  backstory, approach or completion report, so the proposal had to be synthesised
  from the title.
- **Only 38 descriptions are pure WHAT.** Only 4 of those exceed 256.
- **Ids cluster in relationships and backstory, not in the WHAT.** The segments
  carrying ids are relation (390), background (236), what (158), evidence (125)
  and approach (104).

Where the eras differ (share of description text):

| tasks | median desc | median title | what | status-quo | background | approach | evidence |
|---|---|---|---|---|---|---|---|
| E-441..900 | 203 | 46 | 35% | 5% | 8% | 29% | 2% |
| E-901..1300 | 502 | 65 | 29% | 11% | 13% | 18% | 3% |
| E-1301..1700 | 511 | 76 | 31% | 11% | 8% | 23% | 7% |
| E-1701..2000 | 602 | 73 | 27% | 11% | 12% | 19% | 8% |
| E-2001..2187 | 708 | 75 | 24% | 19% | 15% | 15% | 10% |

The early corpus over-explained HOW. The recent corpus over-explains the world
as it is today. A rule that only says "no how" would miss the current problem.

### Recurring patterns that fit no category cleanly (by count)

- **Pointers to another field of the same task** ("See --analysis for why…",
  "Design in analysis.", "Plan attached.") appear in up to 124 descriptions (a
  regex upper bound). They carry no content. They should be dropped, not
  moved, since every slot already renders under its own heading.
  Classifiers filed them inconsistently, as approach, relation or history.
- **Provenance** ("Surfaced while…", "Discovered during E-NNN…") appears in about 66.
  `created_by` already records the surfacing session and task. Filed as `background`.
- **Dated notes and "Mike (2026-…) directed…"**: 134 contain an ISO date. They
  are history or attribution, not WHAT.
- **Deferral triggers** ("revisit if…", "promote when…", "defer until…"): about 13.
  There are too few to justify a name, so they are filed as `scope`.
- **Two deliverables in one description** (e.g. E-2030, E-2093, E-2074). No
  256-character blurb can hold these faithfully. They are a signal to split the task, not a
  wording problem. Flagged in `notes`.
- **Orders to the next session and process lessons** (E-1596 "Pickup session should
  INTERVIEW…", E-885 "PROCESS LESSON…"). These belong in the plan or a lesson,
  never in the description.

## Titles

Components found (a title can carry several):

| component | titles | % |
|---|---|---|
| scope qualifier ("in X", "for Y", "when Z") | 433 | 31% |
| how / mechanism | 246 | 17% |
| parenthetical / colon / dash elaboration | 185 | 13% |
| why clause ("so…", "to avoid…") | 136 | 10% |
| task/decision id | 88 tags (81 titles by regex) | 6% |

- 914 titles (64%) exceed 60 characters.
- 965 (68%) were changed by the proposal, and 454 are already fine.
- A scope qualifier is often exactly what distinguishes a title from its siblings, so
  the rule keeps it when it distinguishes and drops it otherwise.
- Minor anomalies:
  - E-1459's title contains an embedded newline.
  - A handful of titles are defect statements rather than verb-first (E-1856, E-2079,
    E-1760, E-1794). These were kept as found, per the brief.

## What a title is NOT

Ready for `docs/guide/tasks.md`:

- **Not the how.** Drop the mechanism: "via…", "by…", "using…", "proposed at X
  and ratified at Y".
  - E-1813 before: *Replace tasks.tier with complexity and risk rating axes proposed at submit and ratified at approve*
  - after: *Replace tasks.tier with complexity and risk rating*
- **Not the why.** No "so that…", "to avoid…", "instead of…" clause.
  - E-2061 before: *Close the land window where the migrated DB meets the stale installed binary*
  - after: *Close land's window between migrated DB and stale binary*
- **Not a table of contents.** No colon, dash or parenthetical followed by a list
  of sub-parts.
  - E-2148 before: *Rework the errors surface: list vs show, project attribution, width, and remedies*
  - after: *Rework the errors surface*
  - E-1596 before: *Build Endless's per-task verification-suite system: language-agnostic, triple-runnable*
  - after: *Build per-task verification-suite system*
- **Not a task or decision id.** Name the other thing in words, and put the
  relationship in a task link.
- **No qualifier that does not distinguish.** Keep "in hookcmd's claude-skip suite"
  when a sibling title would otherwise collide; drop "when run from a self-dev
  session" when it is detail.
  - E-1628 before: *Fix worktree land writing task.landed to the sandbox DB when run from a self-dev session*
  - after: *Fix worktree land writing to sandbox DB from self-dev shell*
- **Not over 60 characters.** The proposal reached p95 = 59 characters with
  these rules.

## What a description is NOT

A description is the blurb on a page that lists tasks: WHAT the task is, in at
most 256 characters. For a bug, it is the defect in one sentence ("X does Y when
it should do Z").

- **Not the how.** Mechanism, files, tables, flags and steps go in `analysis`
  (design) or `plan` (approved work).
  - E-1813 before: *Replace tasks.tier with two rating axes per ED-1538/ED-1539: nullable FK columns to seeded complexity_levels and risk_levels tables (low=1, medium=3, high=5; 2 and 4 unseeded), Go int-const enums with String()/Parse(), agent-proposed at submit and user-ratified at approve. Ratings do not move status. Tier is removed rather than kept — …*
  - after: *Replace tasks.tier with two rating axes to be agent-proposed at submit and user-ratified at approve. The two axes will be complexity and risk and both will use rating values of 'low', 'medium' and 'high'.*
- **Not the world as it is today, the backstory, or the evidence.** How the system
  behaves now, the incident that prompted the task, logs, counts and repro all go in
  `context`.
  - E-2148 before: *The errors surface has seven problems, found while reading two real incidents. 'show' lists rather than showing one item, … The default listing omits the project, so 'errors show' printed 'no errors' inside one project while the status line reported one error and one warning from another … (six more sentences)*
  - after: *Rework endless errors and its status-line notification: a real single-error show, project attribution, compact severity icons, consistent codes, remedies per error, and rename 'badge' to 'notification row'.*
- **Not why this approach.** Alternatives, tradeoffs and "rather than kept because…"
  go in `analysis`.
- **Not a task or decision id.** Say what the other thing is in words, and make the
  relationship a task link. "Informs E-1993's caps" cannot be read without opening
  E-1993.
  - E-1524 before: *… fail on main as of a01840e: … Surfaced during E-1519 verification via 'go test ./...'.*
  - after: *Two tests in internal/hookcmd/claude_skip_test.go fail on main: TestShouldSkipForWorktreeAt_WorktreeBinaryMissing and TestShouldSkipForWorktreeAt_SelfIsGlobal.*
- **Not a pointer.** Never "See --analysis", "Plan attached" or "Details in the
  plan". Every field already renders under its own heading.
- **Not history.** Progress, "already landed", dated updates, who said what when,
  completion reports and lessons go in `notes` (or `outcome`/`reason` where they
  record the end).
- **Not two tasks.** If the WHAT needs "Separately, …", file a second task.
  - E-2030 before: *The report-channel docs tell every session to run task report … Separately, E-1975's 'a command the user must run survives' invariant is scored at optimizer-promotion time only, …*
  - The rewrite had to carry both deliverables ("… Also add a runtime check …"), and the row is flagged to split.

Ids stay fine in every other slot. Text moved out of a description keeps its ids verbatim.

## Where displaced text goes: proposed content names

| category | destination | tasks affected | chars moved |
|---|---|---|---|
| background, status-quo, evidence | **`context`** (new) | 861 (63%) | 207K |
| approach, rationale, open-question, scope, acceptance | `analysis` | 1,054 | 235K |
| relation, history | `notes`, with relation ids turned into task links where unambiguous | 487 | 50K |

**Proposed: `context`.** Why this task exists: how things work today, what
prompted the task, and the evidence for it. It holds everything a stranger
needs to see that the WHAT is worth doing, but not how to do it.

- **Volume:** 63% of described tasks carry it, 30% of all description text. That is
  more than the approach, and more than any other displaced category.
- **No existing slot fits.** `analysis` is design and investigation toward a plan.
  `notes` is miscellany. `plan` is how. Putting the problem statement into any of them
  loses the one distinction that makes it useful.
- **It is the part a reviewer needs** to see that the WHAT is worth doing, without reading the plan.
- **It is the fastest-growing share.** Status-quo and background went from 13% to
  34% of description text across the corpus's history.
- **Why one name, not three.** In the verification pass, background↔evidence and
  background↔status-quo were among the most-confused pairs. One name absorbs that
  ambiguity instead of making agents choose.

**Not proposed, and why:**

- **`approach`** is `analysis`: it is pre-plan design by definition. Moving it into
  `plan` would fake a plan and, on an untriaged or unplanned task, infer
  `submitted`.
- **`acceptance`** (101 tasks, 2% of text) and **`scope`** (273 tasks, 79-character
  median) are too thin for their own name. They go to `analysis`.
- **`trigger`/defer conditions** (about 13) and **`pointer`** do not qualify. Pointers
  are deleted, not moved.
- **`relation`** already has a home in task links; its prose is kept in `notes` so
  nothing is lost.
- **`history`** is `notes`, or `outcome`/`reason` for the ending.

**On renaming `description` to `blurb`.** I recommend against it. The data points
at three product causes (the guide's "what *and why*… < 200 words / 1024", the
description-sufficiency check reading only this field, and nothing else to hold
context), not at the word. A cap enforced on write, the guide fix, requiring a plan
before spawn, and a place for the displaced text will change behaviour. A rename would touch the flag, the JSON
key, the ledger event shape and every muscle memory, for a smaller effect. If a
rename happens anyway, `summary` reads better in a flag than `blurb`.

## The cap

**Keep 256.** Supporting evidence:

- Only 38 descriptions are pure WHAT, and only 4 of those exceed 256.
- The WHAT portion alone exceeds 256 in 165 of 1,238 descriptions (13%). In the
  rewrite, every one of them compressed to 256 or fewer without dropping the
  deliverable; the proposed median is 158 and p95 is 202.
- The tasks that genuinely strain 256 are the two-deliverable ones. The cap
  catching them is a feature: they should be split.

The title cap of 60 is equally attainable: 454 titles already comply, and every
rewrite fits (p95 59).

## Verification

An independent blind pass re-classified a random sample of **72 tasks** (5% of
described tasks; seed recorded in the scratch script) without seeing the primary
output.

- **Character-level category agreement: 83.4%.**
- **Agreement on destination** (description / context / analysis / notes): **88.8%.**
- Mean category-set Jaccard is 0.80. The two passes agreed on whether a WHAT exists
  in 65 of 72 tasks. Title-tag Jaccard is 0.88.

The disagreements, by characters:

- **what ↔ approach (1,075):** the dominant confusion, and the exact line the
  rules must draw. Typically a WHAT clause that carries its mechanism inline
  ("…nullable FK columns to seeded tables…, agent-proposed at submit"). The "not
  the how" rule and its E-1813 example target precisely this.
- **background ↔ evidence (611), background ↔ status-quo (223):** both sides go to
  `context`, so these are harmless under the proposed mapping. This is the
  evidence for one name, not three.
- **background ↔ what (552), status-quo ↔ what (272):** mostly bug descriptions
  that open with current behaviour (E-1920: "Decisions carry only
  accepted/rejected, so…"). One pass read it as the defect (WHAT), the other as
  context. The rule "a bug's WHAT is the defect in one sentence" resolves it.
- **Low-agreement categories:** open-question (59%) and rationale (63%) were the
  least stable. Both go to `analysis`, so the destination is unaffected.
- **Worst single task:** E-590, a bare leftover heading ("Plan Items to Create")
  with no classifiable content.

## Before / after, in full

| task | title before → after | description after (≤ 256) |
|---|---|---|
| E-1813 | Replace tasks.tier with complexity and risk rating axes proposed at submit and ratified at approve → Replace tasks.tier with complexity and risk rating | Replace tasks.tier with two rating axes to be agent-proposed at submit and user-ratified at approve. The two axes will be complexity and risk and both will use rating values of 'low', 'medium' and 'high'. |
| E-2061 | Close the land window where the migrated DB meets the stale installed binary → Close land's window between migrated DB and stale binary | During worktree land the real database is migrated before the installed binary is refreshed, and nothing stops other processes reading the new schema with the old binary in between. |
| E-1920 | (unchanged) Add superseded and obsolete end states for decisions | Add two decision end states: superseded (a newer decision replaced it, and the record names which) and obsolete (it stopped applying with no replacement). |
| E-1628 | Fix worktree land writing task.landed to the sandbox DB when run from a self-dev session → Fix worktree land writing to sandbox DB from self-dev shell | Running worktree land from a shell routed to a self-dev sandbox makes land's endless calls target the sandbox DB when landing should always target the real main DB regardless of caller routing. |
| E-1596 | Build Endless's per-task verification-suite system: language-agnostic, triple-runnable → Build per-task verification-suite system | Formalize verify handoffs as a committed per-task verification script that runs the checks and reports ALL PASSED or a detailed list of failures. |
| E-1100 | (unchanged) Add periodic pruning sweep for stale 'maybe' tasks | Add a periodic sweep that flags or removes 'maybe' tasks never promoted and older than some threshold. ("Trigger: revisit if…" → `analysis` as scope) |

## The artifact and how to apply it

`.endless/tasks/e-2187/rewrites.jsonl` has 1,419 rows, one per task. Each row has:

- `updated_at` and `source_hash` (sha256 of title + "\n" + description).
- `title`/`description` with `current` and `proposed`. `title.tags` is also included.
- verbatim `segments` whose texts rebuild the description (checked for every row).
- `moves` keyed by destination.
- `notes`.

Rules for the migration that applies it:

- **Apply with a one-off script, not a merged command.** Loop the rows and call
  `endless task update <id> --title … --description-file … --keep-status --db main`,
  plus the content flags for each move. `--keep-status` is the existing bypass: no
  description-edit reset, no plan-attach promotion. Nothing new has to land for it.
- **Skip any row whose hash no longer matches.**
- **Moves merge, never overwrite.** The content flags replace (`--notes` says so
  outright), so the script must read the current content, append the moved text under
  `## From the description`, and write the merged result. The row's `notes` names
  which rows already have `analysis` or `notes`.
- **`context` must exist first.** It is one `taskcontent` constant. Until it does,
  `moves.context` has nowhere to go.
- **67 rows will be refused by the content write gate.** The moved text of 48 rows
  cites a file by line number (`file.ext:NNN`), and 20 name an absolute path. Legacy
  descriptions hold these, but the gate runs on every write of analysis/notes and has
  no escape for line citations, by decision. The script must rewrite those tokens (name
  the symbol) or report the rows for hand review. It must not skip the gate.
- **The WHAT is compressed, not moved.** The verbatim original stays in the ledger's
  history. If that is not enough, move the WHAT segments to `context` too.
- **Pointer sentences land in their destination verbatim.** They should be
  deleted during review, not copied.
- **Row notes flag ids that disappear from a title or WHAT.** Those are ids that
  should become task links.
- **Trial on a sample first.** The batch classifiers were consistent on structure
  but not on edge cases (see Verification).

## Found along the way (not acted on)

- **Likely duplicate groups:**
  - E-1524 / E-1594 / E-1598 (the same two failing hookcmd tests)
  - E-1981 / E-1988
  - E-977 / E-978
  - E-985 / E-996
  - E-1480 / E-1483
- **Scratch or test tasks still in the tree:**
  - E-1286, E-1290, E-1292, E-1299 (description "x")
  - E-1288, E-1289
  - E-1424 "Test foo"
  - E-1385..E-1387
  - E-2072
  - E-2115..E-2118 "Probe the citation gate"
- **Title/description contradictions:** E-572 and E-1187.
- **Suspect citation:** E-1242 and E-1243 say "Per E-1205 user report", but E-1205 is
  an unrelated task. It is probably a session id.
- **Stale instructions in descriptions:** E-1002 and E-1075 tell the implementer to
  hand-run `uv tool install` or write raw SQL, both now forbidden.
- **Escape text instead of newlines:** 4 descriptions contain a literal `\n`, and
  E-1459's title contains a real newline. This suggests the write path accepts
  unescaped input unchecked.
