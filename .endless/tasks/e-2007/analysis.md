Filed from E-2000, which fixed the *routing* of LESSONS.md but kept the flat
file — and then hit that file's conflicts four times in one session.

Scope note: this is **PRODUCT**, not a self_dev convenience. The feature is
"Endless records what the user corrected, attributably, and can feed it back to
the agent" — a replacement for Claude Code's per-machine `MEMORY.md`, available
to every project that uses Endless.

## Why the flat file has to go

Over one session, `.endless/LESSONS.md` conflicted on rebase four times, always
for the same structural reason: several sessions append at EOF, and git cannot
merge two appends to the same trailing region.

The decisive finding is that the mitigation cannot mitigate its own landing.
`.gitattributes merge=union` is inert during the rebase that introduces it —
git resolves merge attributes from the ONTO commit (main), which does not yet
carry the rule. Verified: `git show main:.gitattributes` had no LESSONS line and
the rebase conflicted anyway. A fix that requires itself to already be landed is
a sign the shape is wrong, not the tuning.

The codebase already solved this three times — `decisions` (table +
`.endless/decisions/ED-NNNN.md` mirror), analyses, plans — plus the db-ledger's
per-machine shards. None conflicts; none needs a merge attribute; no two writers
share a path. LESSONS.md is the last artifact shaped as "one file everyone
appends to," and the only one generating conflicts.

## Two halves

**1. Write path.** A `lessons` table, events, ledger entries, and per-lesson
mirrors (`.endless/lessons/EL-NNNN.md`). Columns follow `decisions`, which
already carries `project_id`, `origin_task_id`, `origin_session_id`,
`created_at` — so the `- **Project**: endless` bullet currently typed by hand
becomes a column, and attribution to the task and session that produced the
correction comes free.

A regenerated aggregate `LESSONS.md` would recreate today's conflict exactly, so
if a single reviewable document is wanted it is generated on demand
(`endless lesson export`) and NOT tracked.

**2. Read path — BECOMING `MEMORY.md`, not replacing it.**

Confirmed against the official docs (code.claude.com/docs/en/memory), not the
binary. There is **no hook to point memory at a pair of commands** — it is
file-based. But the documented knob is better than the env var found earlier:

- **`autoMemoryDirectory`** (settings.json, any scope incl. project
  `.claude/settings.json`; absolute or `~/`) relocates the auto-memory
  directory.
- `autoMemoryEnabled` toggles it; `CLAUDE_CODE_DISABLE_AUTO_MEMORY=1` too.
- The directory holds `MEMORY.md` (an index) plus topic files. **Only the first
  200 lines / 25KB of `MEMORY.md` load each session**; topic files load on
  demand via Claude's normal file tools.
- Claude Code enforces that budget itself: it warns near the limit and errors
  over it, telling Claude to shorten the index, move detail to topic files, and
  merge or drop stale entries.

`CLAUDE_MEMORY_STORES` (found via `strings`) is NOT in the docs — it appears to
belong to Managed Agents' memory stores, which mount under `/mnt/memory/`.
Treat as internal; do not build on it.

**So the design is: Endless points `autoMemoryDirectory` at a directory it
owns and GENERATES the contents from the lessons table.** `MEMORY.md` becomes a
rendered view of the top-N active lessons, kept inside the 200-line/25KB budget;
detail goes to generated topic files. Claude reads it through its own memory
mechanism — no injection channel, no new plumbing.

This supersedes the earlier SessionStart-`additionalContext` proposal, which was
too heavy: it would have pushed lessons into every session's context with no
budget and no way to prune.

**This is the argument for the DB, restated.** `.endless/LESSONS.md` is 199,591
bytes / 2,319 lines. Claude Code will load 25KB of it. A useful memory index
must be constantly pruned, merged, re-prioritized and expired — operations a
flat append-only file cannot support and a table can (status, supersession
relations, recency, hit-count). The DB is not just conflict avoidance; it is the
only way to render an index that fits the budget.

Open: whether Claude's own writes into that directory are imported back into the
table (bidirectional) or the directory is generated-only and lessons are
recorded via the CLI.

## Gating: by lesson KIND, not by the self_dev flag

An earlier revision of this analysis said "inject lessons unless `self_dev` is
true." Another session caught the contradiction: Endless IS self_dev, so that
rule blinds the agent precisely in the repo that generates the most
corrections. It is wrong, and the reasoning behind it was too coarse.

Re-read what memory-off actually protects against: *"a memory that quietly
compensates for a bad behavior hides the defect that should have been fixed in
the product."* That is about lessons concerning **Endless's own behavior**. It
says nothing about lessons concerning the agent's reasoning habits.

Sort this session's nine entries and the split is obvious — nearly all are the
second kind: do not assert provenance without checking `git log`; do not build a
fixture that manufactures its own expected failure; do not answer the adjacent
question; do not extract text by line count. None of those mask an Endless
defect. Suppressing them here is pure loss.

So the gate is a **`kind` column**, not a project flag:

| kind | in self_dev | elsewhere |
|---|---|---|
| `friction` — Endless behaved badly or surprisingly | **not fed back**; escalate instead | fed back (just a tool quirk to remember) |
| `behavior` — the agent's own reasoning or method | fed back | fed back |

`friction` in self_dev is the case CLAUDE.md forbids, and the right handling is
not silence but escalation. Escalation is **not** "always file a task" — it
follows the rule already in `endless guide tasks` for work discovered mid-task:
file ONLY if it is not already filed and no existing task can be extended to
cover it. Preferred order:

1. An existing task covers it → add to that task, do not file.
2. An existing task nearly covers it → extend that task's description or
   analysis.
3. Nothing covers it → file, with `--cleans-up` pointing at the task in hand.

Endless carries 225 open tasks; a `friction` kind that files unconditionally
would make that worse, and duplicate-filing is already a named failure mode. The
lesson row should record which task absorbed it, so the escalation is auditable
and the second session to hit the same friction finds the first one's task
instead of filing a twin.

This is a third argument for the table — a flat file cannot carry the
distinction, so today the only available gate is the blunt all-or-nothing one.

## Real design work (ordinary, not blocking)

1. **Rendering `MEMORY.md` inside 200 lines / 25KB** from 2,319 lines of
   source. Needs status (active / superseded / retired), a selection policy
   (recency, relevance to the current task, kind), and a topic-file split for
   detail. Claude Code enforces the budget, so this is a hard constraint, not a
   preference.

1b. **Entry length.** Existing entries average ~85 lines per 1,000 words of
   value and are far too long for an index — a known problem with how they have
   been written. The table should store a one-line rule (what renders into
   `MEMORY.md`) separately from the full narrative (topic file / `text`
   column).
2. **Supersession.** Lessons contradict each other over time; a later one
   refines or overrides an earlier one. Task/decision relations already model
   this (`replaces` / `replaced_by`).
3. **Migration.** ~2,300 lines in two formats: `### [Date] Short description`
   with What went wrong / Why / Rule / Project bullets, and
   `## Title (date, E-NNNN)` with prose paragraphs. Decide between a parser for
   both and a lossy one-time import with the original archived.
4. **Retiring the workaround.** E-2000's `.endless/LESSONS.md merge=union` line
   and its three comment-integrity assertions come out with the flat file.
5. **Interaction with `autoMemoryEnabled`.** If lessons replace memory for
   downstream projects, say what Endless recommends there — leaving both on
   means two competing stores.
