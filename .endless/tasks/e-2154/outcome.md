# Glossary of agreed terms: synthesis

## The problem
There are two failures. Coining: an agent invents a word ("attention board", "board") and uses it as if it were the product's name. Drift: nobody coins anything, but one word picks up several meanings ("owner" ended up with three). Coining is handled by knowing the list. Drift is handled by entries that say what a term is NOT.

## The principle
Proposing a new term is GOOD. Using a term the user hasn't agreed to is BAD. The goal is shared vocabulary between user and agent. To paraphrase Shaw, the biggest problem in communication is the illusion that it has taken place. The vocabulary is shared, not mapped. When code and the agreed term disagree (`surfaced` vs "filed by"), the code is the defect, and it gets renamed.

## The design: the lightest thing that changes the default
- **The glossary is a destination, not a workflow.** It's its own table (not files, not the decisions table, whose needs differ), Go-owned and written through the event pipeline. Entries have no proposed/status lifecycle. An entry exists only once the user has said yes.
- **Entry fields:** term, definition, not-to-be-confused-with, split-from, disposition (agreed | rejected), use-instead (rejected only), and links to the task(s) where it was settled. Rejected terms are stored, so they don't get proposed again ("wake").
- **Two layers.** Project terms are the default. Global terms ship as a built-in starter set and carry `built_in` and `enabled`, so users can add their own or disable built-ins. A project entry shadows the global one with the same term. This lines up with the global/project database split.
- **Commands:** `endless glossary ...` for the corpus (colored list, `-p` pagination, `--terms-only`). `endless term ...` for a single entry (add, show, reject, disable). `term show` takes several terms in one call.
- **How agents see it.** The handoff *injects* (puts into context unprompted) the bare agreed terms and the rejected terms with their use-instead, plus the commands to *fetch* definitions and a pointer to the guide. A terms-only list is cheap: once per handoff it's a fixed cost, not one that grows with turns. Definitions are fetched on demand. If agents turn out to guess rather than fetch, a later option is letting the user mark specific terms to inject with their definitions.
- **Agreeing a term.** The agent proposes the term, definition, what it is NOT, and what it was split from. The user says yes in chat, and the agent runs the command. No enforcement and no detection: the user is the detector. Faking a yes is assumed not to be a problem until it's seen happening.
- **Where entries link.** An ad-hoc term links to the task it came up in. A term that needs real deliberation goes through a `decide` task (see ED-1609).
- **The first batch is definitive, not exhaustive.** Only terms whose need and meaning are beyond question. Anything uncertain waits until it causes friction on another task.
- **Visibility.** A glossary tmux tab beside project monitor, so the terms stay visible rather than out of sight.

## For a solo user with no agents (PRODUCT)
A browsable project vocabulary with recorded boundaries and rejected alternatives. Global starter terms work out of the box and can be disabled.

## Settled elsewhere / not here
- Whether decisions keep a table (as a destination, behind a `decide` task type with subtypes, keeping the name `decision` rather than `adr`) is input to E-1861/E-1868, recorded as ED-1609. Reversing E-1868 is acceptable if that's the better approach.
- Injecting definitions for a large glossary (hundreds of terms) is deferred until it matters.
- Correction of the seed: the up-front "agree terms at project start" idea was Mike's remark. Making it a workflow step, and detection, were agent additions and are dropped.

## Follow-ups
- E-2235 Build a project and global glossary of agreed terms (table, commands, handoff injection, guide section; replaces the LESSONS "never mint new terms" entry with "propose terms; use only agreed ones").
- E-2236 Show the glossary in a tmux tab beside project monitor (blocked by E-2235).
- E-2237 Survey past transcripts for definitive glossary terms (blocked by E-2235). Seeds the starter set, migrates ED-1605 and retires it, and spawns rename tasks for code-vs-term mismatches.
- ED-1609 A decide task is the process; a typed table is the destination (proposed; input to E-1861/E-1868).

## Worked example: seed entries in this shape (to be confirmed in E-2237)
- **owner**: the user who owns a task. NOT the claiming session, NOT the worktree lock holder. Split from: claiming session, worktree lock holder (ED-1605).
- **claiming session**: the session that claimed a task. NOT owner.
- **worktree lock holder**: the process holding a worktree's lock. NOT owner.
- **filed by**: the session that filed a task. Agreed reader term. The code's `surfaced` should be renamed to match.
- **triager** (a class of job) vs **rater** (one job in that class). NOT interchangeable.
- **project status / project monitor** vs **session status / session monitor**. Rejected: "attention board", "board", use-instead these.
- **inject** (Endless/harness puts text into context unprompted) vs **fetch** (the agent runs a command to get it).
- **wake**: rejected; discussed and deliberately not adopted.
