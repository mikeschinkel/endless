# Lessons Learned

A **write-only** capture log, for Mike's periodic review. After ANY correction
from the user, record it with:

```
endless lesson write "<one-line summary>" --text "<the lesson>"
```

That command is the ONLY way an entry gets here. It appends to the MAIN
checkout's copy of this file and commits that one path there in the same step,
so recording a correction never waits on `endless worktree land` and never
dirties a worktree. Do not append by hand, do not edit a worktree's copy, and
do not commit this file yourself. (E-2055.)

**Do NOT read this file** at session start or during a session, and do not act
on its contents. It is not context — a session that quietly compensates for a
bad behavior hides the defect that should have been fixed in the product. That
is the whole reason memory is off in this project; reading this file back would
reinstate the loop by other means. See "Memory is OFF here" in `CLAUDE.md`.

The summary is the lesson's one-line rule, capped at 384 characters and written
here verbatim. The commit subject is derived from it — `lesson: ...`, truncated
to 60 characters — so a long summary costs nothing but an ellipsis in
`git log --oneline`. Keep the explanation in `--text`: it has no cap, and it
becomes both the commit body and the entry's detail.

## Format

`endless lesson write` renders each entry as:

```
### [Date] One-line summary
<--text, verbatim>
- **Project**: <derived from the project you ran it in>
```

Entries below predate the command and vary; the `--text` body of a new one is
free-form Markdown, and the shape worth writing is still:

```
- **What went wrong**: Brief description of the mistake
- **Why**: Root cause
- **Rule**: What to do instead (actionable, specific)
```

---

## Entries

### [2026-06-29] Reinvented the canonical tmux pane→session resolver instead of reading the code
- **What went wrong**: For the `session next` watcher I hand-rolled window→session resolution three times — pane-match with `state IN ('working','idle')`, then `@endless_task_id` via the WRONG tmux command (`show-options` instead of `display-message`), then back — all broken (wrong task, or every pane showing the same task) — when `monitor.GetActiveTaskForPane` (the status-line resolver, established by E-1530) already does it correctly: pane → all panes in window → newest `last_activity` where `state != 'ended'` and `active_task_id IS NOT NULL`. Mike: annoying to badly reinvent a wheel that's been sorted for other commands for ages.
- **Why**: I derived the mechanism from first principles instead of grepping for how the status line / existing commands already resolve it. The key subtlety I got wrong — `state != 'ended'` (NOT `working/idle`) — is a hard-won E-1530 fix I'd have inherited for free by reusing. Compounded by there being no decision record pointing to the canonical resolver (now ED-1523).
- **Rule**: Before implementing ANY "which session/task does this tmux pane/window belong to" logic — or any cross-cutting resolution other commands clearly already do — grep for the existing implementation FIRST and reuse it. For tmux-pane→task, the reference is `internal/monitor/tmux_lookup.go` `GetActiveTaskForPane`; the status line is the canonical consumer. When porting to Go, CALL the canonical function, never replicate. Reuse-before-create applies doubly to resolution logic where subtle filters carry bug fixes.
- **Project**: endless

### [2026-06-28] Filtered invalid data out of a monitoring view instead of surfacing it
- **What went wrong**: Building the `session next` prototype, I added an `-8h` liveness filter to *hide* dead sessions (still labeled `working`) from the IN FLIGHT rows. Mike: the visualizers are precisely what surface data bugs — a tool you stare at all day should render state faithfully and flag anomalies, not defensively filter them out of sight. My filter hid the very reaping bug the tool should reveal. Inverting it (show every holder, annotate the session's staleness age) immediately exposed sessions 2–68 days cold still marked `working` (incl. a 68-day "Test foo" session).
- **Why**: Defensive instinct — I treated stale/invalid data as something to guard the *output* against, rather than as the signal the monitoring tool exists to surface. For a read-site/visualizer, hiding bad state defeats its purpose.
- **Rule**: In monitoring/visualization tools, surface invalid or stale state loudly (flag/annotate it); don't filter it away. Filtering belongs to correctness-critical write paths, not to read-sites whose job is to make problems visible. And don't gate building a visualizer on first hardening the data — the visualizer is the bug detector. (Aligns with the loud-failure-on-invalid-state rule.)
- **Project**: endless

### [2026-06-28] Rushed to tear down claim's worktree/sandbox as "cruft" without tracing its use
- **What went wrong**: After `endless task claim E-1461` stood up a worktree + sandbox (it's the work-start flow on a self-dev project, not lightweight ownership), I (a) claimed a sandbox would "route all future writes away from the real ledger" → split-brain, (b) called the worktree "cruft," and (c) recommended releasing the claim and removing both. Mike corrected all three: writes require an explicit `--db main|sandbox` so there's no accidental routing; the plan we were about to write mirrors into and lands via the worktree, so it's the plan's vehicle, not cruft. The plan write then confirmed it — `task update --text` wrote the mirror into `.endless/worktrees/e-1461/.endless/plans/E-1461.md`.
- **Why**: I pattern-matched "unexpected infrastructure appeared" to "cleanup needed" and proposed teardown before tracing the infrastructure's downstream purpose. I also asserted a routing/staleness mechanism from assumption rather than checking how `--db` actually works.
- **Rule**: On endless self-dev, `claim` always creates a worktree+sandbox (work-start), `bind` is the display-only no-files-changed alternative — pick by whether files will change. Before recommending removal of any claim/spawn-created worktree or sandbox: (1) confirm whether an artifact (plan mirror, code) will live in or land via it; (2) remember sandbox writes need an explicit `--db`, so there is no accidental split-brain from cwd alone. Don't propose cleanup of infrastructure until you've traced what consumes it.
- **Project**: endless

### [2026-06-01] git reset --soft can stage huge unintended reverts on stale branches
- **What went wrong**: A worktree branch (task/1421-...) had two plan-update auto-commits ahead of main, but had been created weeks earlier — main had moved on substantially in unrelated files. To squash the two plan commits into one before re-landing, I ran `git reset --soft main` from the worktree. `--soft` moves HEAD but keeps the working tree, so the index was now "branch state vs main state" — staging deletions of 200+ unrelated files: db-ledger entries from intervening landings, other tasks' plan files, config.json changes. I committed that as `227afe4 E-1421: rewrite plan for granular event-sourced architecture` (326 files changed, 12,982 insertions, 20,192 deletions). Caught it from the file-count line. Per the memory "Never discard auto-record commits," this would have rolled back DB ledger state if it had landed. Recovered via `git reflog` → `git reset --hard 0813816`.
- **Why**: I conflated "the branch is 2 commits ahead of main on the plan file" with "the branch's working tree only differs from main by those 2 commits." The branch's working tree had drifted from main on hundreds of files because main had absorbed many other landings since the branch was created. `--soft` exposed that full drift as a staged diff, which I then captured as a single reverting commit. The correct mental model: `git reset --soft <target>` is safe ONLY when `<target>` is an ancestor whose only difference from the branch is the commits being squashed.
- **Rule**: Before `git reset --soft <target>` for squashing, verify the only differences between current branch and target are the commits to squash: `git diff <target>... --name-only | wc -l` should be ≤ the number of files those commits touched. If more files differ, the working tree has drifted from the target and `--soft` will stage that drift. Use `git rebase --interactive` (operates on commit range, not working tree) or `git rebase <main>` with manual conflict resolution instead. When you DO run a reset and `git status` after shows many more files staged than you expected, STOP — don't commit; investigate the file list first. The corollary: long-lived branches (weeks old) need a rebase before any squash operation.
- **Project**: ALL (git workflow discipline)

### [2026-05-25] Promoted a recalled memory clause to an in-scope requirement without checking
- **What went wrong**: Planning E-1469 (generate spawn handoff from a template, drop `tasks.prompt`), I recalled the memory "a task prompt is a handoff, not a plan," which carried a clause: "must include `endless channel beacon` instruction." I baked a beacon line into the drafted handoff template and presented it as part of the design. Mike: "Beacon was never in scope; you stated that and I corrected you, then found that I also had to edit the plan." He had to manually edit the beacon out of the approved plan.
- **Why**: I treated a memory's prescriptive clause as a standing requirement and transplanted it into a new task's deliverable, instead of asking "is this clause actually in scope for THIS task?" Recalled memories reflect what was true/intended when written, in a specific context (here: hand-authored prompts). The beacon clause was context-specific (and, per Mike, wrong even then for the handoff). Carrying it forward unexamined turned a stale clause into a concrete artifact Mike had to clean up.
- **Rule**: When a recalled memory says "include X / must do X," do NOT bake X into a new deliverable as settled. Surface it as an assumption to confirm against the current task's scope: "the memory on Y says include X — is that in scope here?" Especially for content the user will sign off on (templates, prompts, plans), keep recalled prescriptions visible and attributed rather than silently merged in. Recalled-memory clauses are inputs to verify, not requirements to satisfy.
- **Project**: endless / ALL (memory recall discipline)

### [2026-05-19] Sunk cost framing on uncommitted work
- **What went wrong**: Comparing "mark E-1415 obsolete" vs "land E-1415 as interim safety net," I listed "Cost: abandon ~552 lines of uncommitted work" as a real consideration for the obsolete path. Mike: "There is no cost to abandon ~552 lines of uncommitted work; what you are echoing is the fallacy of sunken cost."
- **Why**: I conflated "labor already spent" with "labor it costs to discard." They're not the same. The 552 lines exist whether or not they're committed; abandoning them costs zero additional time. The actual costs of the obsolete path are forward-only: nothing to do (no commit, no verify, no land) — that's actually a benefit, not a cost. The actual costs of the interim path are also forward-only: ~30 min to commit + risk of dead code lingering after E-1426 lands. I made the wrong frame look balanced by inventing a phantom "cost" on the side that had none.
- **Rule**: When weighing paths where one discards prior work, enumerate FORWARD costs only. Past labor is gone whether kept or abandoned — it never enters the comparison. Banned phrases: "wastes already-done work," "throws away X lines," "doesn't waste already-done work." If past labor IS valuable (e.g., the discarded code teaches the design), say "useful as a reference" — not "cost to abandon."
- **Project**: ALL decision-framing

### [2026-05-17] Session-ID attribution failures are a FIVE-ALARM fire
- **What went wrong**: For most of one work session, my Bash invocations of `endless task add` / `endless task update` emitted events with NO `session_id` in the actor block. None of the 6 tasks I filed (E-1390 through E-1394, E-1396) auto-recorded in session_tasks, because the upstream resolver `_current_endless_session_id` silently returned empty when faced with multiple Claude panes alive (the single-sibling fallback per E-1294 refuses ambiguity). I didn't notice; Mike caught it. He: "Inability to determine a valid session ID and to do so accurately and robustness should be treated as a FIVE ALARM FIRE!"
- **Why**: Session-attribution failure was implemented as best-effort with silent degradation: if resolution failed, the field was simply omitted. From there, every materialized table that depends on session_id has a giant invisible hole in its data. Mike's project (Endless) is built around per-session activity tracking; an attribution gap means the entire premise of the system is silently broken until someone notices. This is also a classic `gates_not_guardrails` violation — the silent-omission path is a guardrail (degrade quietly), where a gate (refuse to emit) was required.
- **Rule**: Any code path I write or spec that depends on session-attribution MUST: (a) treat resolver failure as an error condition, not a missing field; (b) surface the error with an actionable message ("this pane isn't bound; run `endless task bind <task-id>` first"); (c) never silently emit a sessionless event. When reviewing my own specs for any session-attribution-dependent feature, the question to ask is: "What happens if the resolver returns nothing? Does that path error, or does it succeed-with-degradation?" If the latter, that's a bug — fix the spec before the implementer ships it. Same rule applies when I write Python event-bridge code or Go handlers that consume actor data.
- **Project**: endless / ALL session-tracking systems

### [2026-05-16] Every table gets a surrogate `id INTEGER PRIMARY KEY` — natural keys are NOT a substitute
- **What went wrong**: Specified `session_tasks` (E-1322) with `UNIQUE(session_id, task_id)` as the natural key and NO surrogate `id` column. The implementer faithfully shipped that schema; the commit message even defended it ("Natural key UNIQUE(session_id, task_id), no surrogate id PK"). Mike: "I just found an issue with the design of E-1322; the session_tasks does not have its own primary key id. ALL tables should have an id field (I learned that from 35+ years database experience.)" Filed E-1396 to fix. Then on review, also missed adding `id` to the two new prompts E-1391 (session_worktrees) and E-1392 (session_landings) — same omission, two more times.
- **Why**: I let "the compound key fully identifies a row, so a surrogate is redundant" override the universal practical rule. Surrogate ids unblock: (a) external references to specific rows without recomputing the compound, (b) ordering by insertion time without a separate timestamp, (c) stable cursor pagination, (d) audit-trail row addressability, (e) ergonomics in tooling (sqlite browsers, ORM helpers) that assume `id` exists. Natural keys can coexist as UNIQUE constraints, but they don't replace `id`. Mike's 35-year heuristic is load-bearing; don't second-guess it on row-shape minimalism.
- **Rule**: Every `CREATE TABLE` I author or spec MUST start with `id INTEGER PRIMARY KEY` as the first column. Compound keys go in as `UNIQUE(...)` constraints AFTER the id column. No exceptions, regardless of whether the table is a junction/relation/materialized-index or a primary entity. When reviewing existing schemas (during plan-mode or pre-spawn), grep for `CREATE TABLE` and verify the first column is `id` — flag any that aren't BEFORE the spec lands.
- **Project**: ALL (any project with a relational DB)

### [2026-05-16] Verify the actor model before specifying actor-based guards
- **What went wrong**: Wrote the E-1322 spawn prompt requiring `evt.Actor.Kind == ActorSession` as the auto-capture guard. The implementer faithfully followed the spec; E-1322 landed with no end-to-end test of "agent runs `endless task add`, row appears." Mike tested it; no rows appeared. Investigation: the ledger has 1518 events with `actor.kind=cli` and zero with `actor.kind=session`. The `Kind` field describes event ORIGIN (cli/hook/web/system) per its docstring; session attribution lives in the orthogonal `SessionID` field. My guard required a Kind value that emit_event never sets, so the feature was unreachable from the primary code path.
- **Why**: Pattern-matched from "I want to gate on 'session actors'" to "check `Kind == ActorSession`" without verifying that `ActorSession` is ever emitted in practice. The implementer didn't catch it either because the spec's claim ("CLI/hook actors may carry a session_id but don't bind them as sessions") sounded authoritative and I gave it confident-sounding rationale. A `grep -ho '"actor":{"kind":"[a-z]*"' .endless/db-ledger/*.jsonl | sort -u` would have shown the actual distribution in seconds.
- **Rule**: When specifying a guard that pattern-matches on actor/event field values, BEFORE writing the spec: (a) grep the ledger or audit logs for the actual distribution of values in that field, (b) read the field's docstring in the source-of-truth type definition, (c) confirm the value you're matching is actually emitted. Don't infer field semantics from the type-name alone — `ActorSession` sounds like "the kind that means session-attributed events" but in this codebase it's a separate channel that nobody emits. Same caution for any closed-enum field: training intuition about names is unreliable; the codebase's actual usage is authoritative.
- **Project**: endless / ALL spec-authoring

### [2026-05-15] When a name reads wrong, question whether the concept needs naming at all
- **What went wrong**: Mike flagged that "filings" (for the session→task relation table) reads ambiguously toward metal shavings rather than paperwork. I "fixed" it by renaming to `session_status_originations` — picking a fancier agentive noun for the same invented concept. Mike pushed back again: "What is an 'origination' per E-1322? Why do we not use the (to me) easier to reason about name `session_tasks`?" The right move from the start was to drop the invented vocabulary entirely. The table is the relation between sessions and tasks; `session_tasks` is the obvious name; the row's existence captures the "session N created task M" semantics without needing a special noun ("filing" or "origination") for that semantics.
- **Why**: Reflex on naming feedback is "find a better synonym for the same concept." Skips the more useful question: "does this concept need its own word, or can the table semantics carry it?" Junction tables typically take the form `entity_other_entity` and let the row encode the relation implicitly. Inventing a noun for the relation ("filing" / "origination") adds a vocabulary item the reader has to learn before they can parse the table name.
- **Rule**: When naming a relation/junction table, default to `entity_other_entity` (e.g., `session_tasks`). The verb describing the relation can live in comments, docs, or context — it does not need to be encoded as a noun in the table name. Reach for an invented noun only when (a) the relation has independent semantics that the entity pair doesn't convey, AND (b) you can name the noun without a paragraph of explanation. If you find yourself defining the noun (an "origination is the relationship where…"), the simpler name is probably right.
- **Project**: ALL

### [2026-05-15] Tasks to DECIDE are type=task; decision RECORDS are type=decision — and don't override the user's prior type correction
- **What went wrong**: E-1327 ("Decide session-status data model…") had been type=task in the DB because Mike had previously corrected it from decision back to task. The spawn prompt incorrectly described it as "type=decision" in prose. When I flipped status at the end, I also "fixed" the type back to decision — re-introducing the exact error Mike had already corrected once. He caught it: "GODDAMMIT, I already changed E-1327 from decision to task once, and now you made it a decision again?!? How is a task to decide itself a decision?!?" Separately, I flipped status to `verify` when the right terminal status was `completed` — tasks meant to decide don't need verification.
- **Why**: Two errors compounded. First: I treated the spawn prompt's prose description ("type=decision") as authoritative over the actual DB state (type=task). Spawn-prompt prose is fallible; the DB is canonical. Second: I followed my own plan's "verify" guidance over the spawn prompt's "completed" guidance AND over the standing rule for findings-as-deliverable. The conceptual confusion underneath: a task that DECIDES is not itself a decision. Decisions BEGET from tasks; the task is the activity, the decision is the output record.
- **Rule**: (a) Before changing a task's `type`, check `git log` / task-history for a prior change; if the current value resulted from a user correction, do NOT override without explicit instruction. (b) Tasks whose title starts with "Decide …" or "Choose …" stay `type=task`, not `type=decision`. The decision RECORD is a separate row created via `endless decision add "<imperative-verb title>" --about <deciding-task> --decides <implementing-task-ids> --description "<short pitch>"`, then long write-up goes into the decision row's `outcome` via `endless task update <decision-id> --outcome "$(cat plan-file)"`. (c) Complete the deciding-task with `endless task complete <id> --outcome "<one-liner; see E-NNNN for full record>"`. `task complete` is gated on the title's lead verb being `completable: true` in verbs.json. Skip `verify` for findings-as-deliverable types — there's nothing to verify behaviorally; the deliverable is the outcome text itself.
- **Project**: endless

### [2026-05-15] Decision-task write-ups are posture, not architectural ruling
- **What went wrong**: Drafting E-1327's decision write-up (snapshot vs event-stream session-status data model), my first pass had "Why C over B" / "Why C over A" comparison sections and definitive statements like "promote only the truly cumulative aspect to its own table" and "headline + notes don't fit any structured kind." Mike called it out directly: "Don't document a stake in the ground, as you are wont to do." For a decision where the right answer emerges through use, those passages read as commitments that future-us would have to undo.
- **Why**: Reflex to "show my work" by writing rationale that locks in the chosen option. Also reflex to make plans look complete/final by stating things definitively. For decisions that are inherently provisional (incremental architecture, "build and see"), this register actively misleads.
- **Rule**: When drafting plans for `decision`-type tasks (or any plan where the answer is provisional posture, not a final spec), use the register: "current direction, revisable" instead of "chosen architecture." Drop "Why X over Y" comparison sections — they commit to rationale that may not survive contact with usage. Add an explicit "What this does not decide" section to keep absence-of-words from reading as commitment. Pattern markers to watch for and rewrite: "Promote only…", "Don't…", "Why X over Y", "always", "never", "the final answer is X."
- **Project**: endless / ALL decision-type planning

### [2026-05-15] Map the design space before forced-choice AskUserQuestion
- **What went wrong**: Twice in one conversation on E-1327, I used AskUserQuestion forced-choice with 2-3 architectural options before Mike and I had dialogued the actual design space. First time: dropped three sub-decisions (runbooks-where, test-probe-ephemerality, table-rename-now) as multi-choice before naming the underlying axis. Mike: "Your AskQuestions questions are premature. We really need to define the problem space and discuss alternatives and discuss pros and cons in a dialog before we get to the final decisions points your AskQuestions is after."
- **Why**: Plan-mode workflow requires ending turns with AskUserQuestion or ExitPlanMode, which biases toward forcing a choice rather than continuing dialog. Also reflex to "make progress" by surfacing decisions before the design space is mapped enough that the user has the right axes to choose along.
- **Rule**: AskUserQuestion forced-choice format is for clarifications inside a mapped design space, not for opening one. Before invoking AskUserQuestion: confirm (a) the user has named the axis the question sits on, (b) the options actually cover the space (not just three points I picked), (c) the question isn't doing the user's job of design exploration. If unsure, use plain-text dialog to map the space first; AskUserQuestion comes after the space is jointly mapped. In plan mode specifically, prefer text dialog over premature AskUserQuestion even if it means the harness reminder pushes back.
- **Project**: ALL

### [2026-05-15] Run `endless task claim` the moment a worktree exists for a task
- **What went wrong**: After filing E-1345 and attaching its plan via `endless task update --text … --status ready`, I went straight to editing `internal/monitor/migrate.go` and writing a test. I never ran `endless task claim E-1345`. The status jumped `needs_plan` → `ready` → `verify` with no `in_progress` window, so the audit trail makes the work look like it happened with no claim. Mike caught it afterwards; the best I could do retroactively was `endless task bind` (status is already `verify`, so re-claiming would have demoted it).
- **Why**: I treated "worktree exists + plan attached + ready" as the cue to start writing code, instead of treating "session is registered against the task" as the cue. `endless task update --text` auto-creates the worktree, which makes it easy to skip the explicit claim step. Hook enforcement also didn't catch the gap (filed as E-1346). I also reached for `endless task start` initially — that verb is a deprecated stub that refuses to execute; the replacement is `claim`.
- **Rule**: The cue for writing code is `endless task claim <id>` succeeding — not the worktree appearing. After every `endless task add` / `endless task update --text … --status ready`, run `endless task claim <id>` *before* the first Edit/Write. Never use `endless task start` — it was renamed to `claim` and the old verb is a deprecated stub. If the task is in a status that refuses claim (e.g., already `verify`), use `endless task bind` to attach the session for display without changing status.
- **Project**: endless

### [2026-04-28] Don't soften a recommendation with a contrived counter-example
- **What went wrong**: Recommending against schema-level NOT NULL on tasks.description, I added a softening clause that "some tasks legitimately have title-as-description" and offered "Fix typo in CLAUDE.md" as the example. Mike challenged it on the spot: which typo? where? The example failed its own test (the title did not convey what work was being asked) and undercut the broader argument I was making about discoverability. No real example existed; I had invented one to seem balanced.
- **Why**: Reflexive hedging. When making a strong claim, default to "but maybe sometimes the opposite" instead of committing or dropping the caveat. Treating balance as a virtue independent of whether the counter-example holds up.
- **Rule**: Before adding "but in some cases X" to a recommendation, write down a real, non-contrived example that actually meets the test of X. If you cannot, drop the caveat and commit to the position. A confident wrong claim is testable; a hedged claim with no real example is just noise that softens the point you came to make.
- **Project**: ALL

### [2026-04-27] Tracer-bullet must exercise the actual code path, not just the data layer
- **What went wrong**: Implemented Endless drift-detection + suggestions infra. Verified end-to-end via direct SQL inserts and CLI calls (insert → list → show → accept). Reported "tracer-bullet works, ready to test." Mike turned on the feature and the hook deadlocked on the very first UserPromptSubmit because `ScanRecentSuggestions` (always-on hook code I added) had a Go SQL bug. My "verification" never actually invoked the hook.
- **Why**: Confused "verified the data layer" with "verified the runtime path." Direct SQL inserts skip the code that runs in production (the hook handler). When the always-on code path has a bug, every interaction breaks, not just the new feature.
- **Rule**: When adding code to an always-on hook (UserPromptSubmit, Stop, PreToolUse, etc.), the tracer-bullet test MUST exercise the hook itself, not just the underlying functions. At minimum: trigger the hook event in a controlled way, watch logs for errors, confirm no exit code 2 or panic. If you cannot trigger the hook safely, gate the new code behind a feature flag that defaults OFF (so production is unaffected) AND tell the user "this needs a real-hook test before turning on" rather than claiming it is verified.
- **Project**: Endless / ALL hook-based work

### [2026-04-27] Go database/sql: never nest queries while iterating rows on SetMaxOpenConns(1)
- **What went wrong**: Wrote a Go function that did `db.Query(...)` then iterated `rows.Next()`, and inside the loop called `db.QueryRow(...)` for a dedup check. Endless DB sets `SetMaxOpenConns(1)` (correct for SQLite single-writer). The iterator held the only connection, the nested QueryRow blocked waiting for it, deadlock. Fired on every prompt the user typed until Mike disabled the entire hook.
- **Why**: Forgot the single-connection pool constraint. SQLite + `SetMaxOpenConns(1)` is the canonical pattern for SQLite in Go, and it is hostile to nested queries.
- **Rule**: When a Go function uses a SQLite db with `SetMaxOpenConns(1)` (Endless does this in `internal/monitor/db.go`), never issue a second DB call between `db.Query()` and `rows.Close()`. Pattern: drain the iterator into a slice first (`for rows.Next() { append(&out, ...) }`), close rows, THEN do further DB work. Same applies to nested transactions, nested QueryRow inside QueryRow scans, anywhere the connection might be in-use. If unsure, restructure to a "fetch all, then process" two-phase shape.
- **Project**: Endless / any Go project pinning SetMaxOpenConns to a low number

<!-- Claude: append new entries below this line -->

### [2026-03-01] NEVER run `go build .` in source directories — REPEATED OFFENSE
- **What went wrong**: Example binaries were built with `go build .` directly in example directories, leaving untracked binaries that pollute the working tree. Happened AGAIN on 2026-03-04 despite this lesson existing.
- **Why**: Subagents and verification steps use `go build .` as a quick check without `-o`
- **Rule**: NEVER run bare `go build .` or `go build` without `-o`. ALWAYS use one of: (1) `go build -o /tmp/<name> .` for throwaway verification, (2) `go build -o ./bin/<name> .` if a bin/ dir exists, (3) `go vet ./...` instead if you just need to check compilation. This applies to ALL agents and subagents too — include this constraint in subagent prompts.
- **Project**: ALL Go projects

### [2026-03-05] Never declare a task complete until ALL consumers are migrated
- **What went wrong**: Declared DrillDownModel[T] work "done" after creating the new type in teatree and adding deprecation notices, but never migrated Gomion (the only consumer) off teadepview.PathViewerModel.
- **Why**: Conflated "the new thing exists" with "the migration is complete". Also added deprecation notices instead of doing the actual migration work.
- **Rule**: A rename/migration task is NOT done until: (1) new type exists, (2) ALL consumers use the new type, (3) old type/package is fully removed or has zero consumers. Never leave deprecated code in pre-1.0 software — there are no external users, so just migrate everything and delete the old code. "Done" means fully migrated, not "new thing created."
- **Project**: go-tealeaves / gomion

### [2026-03-04] Never add deprecated aliases or backward-compat shims to pre-1.0 software
- **What went wrong**: Added `// Deprecated: Use X instead. type OldName = NewName` aliases and deprecated constructor wrappers during type renames
- **Why**: Defaulted to "safe" backward-compat patterns without considering that this is pre-1.0 software with no published consumers
- **Rule**: For pre-1.0 software: just rename. No aliases, no deprecated wrappers, no legacy shims. Update all call sites (including tests, examples, and docs) to use the new names directly. Clean break.
- **Project**: go-tealeaves

### [2026-03-05] lipgloss v2 Width() includes border — don't hardcode padding-only offsets
- **What went wrong**: `Width(maxContentWidth + 3)` only added PaddingRight(3) but not border width (2). In lipgloss v2, Width is total rendered width including border. This caused word-wrapping of help visor descriptions.
- **Why**: Carried forward a v1-era pattern where Width excluded border, or just hardcoded the padding offset without considering the full frame.
- **Rule**: In lipgloss v2, always compute Width as `content + GetHorizontalPadding() + GetHorizontalBorderSize()`. Never hardcode offsets — use the lipgloss API to query padding and border sizes. The internal wrapping limit is `Width - border - padding = content area`. If Width doesn't include border, text wraps at `content + padding` but only `content` chars are available, causing premature word-wrap.
- **Project**: go-tealeaves/teahelp

### [2026-03-06] HashiCorp tools are NOT in Homebrew core — use hashicorp/tap
- **What went wrong**: Script referenced `brew install packer qemu` but Packer was removed from Homebrew core after HashiCorp's BSL license change.
- **Why**: Assumed Packer was still in core Homebrew. It was removed in 2023-2024.
- **Rule**: HashiCorp tools (Packer, Terraform, Vault, Consul, Nomad) must be installed via `brew install hashicorp/tap/<tool>`. Always use the tap path in install instructions: `brew install hashicorp/tap/packer`, `brew install hashicorp/tap/terraform`. Same applies to CI/CD and Dockerfiles.
- **Project**: homelab

### [2026-03-06] Characterize problems by agency: "we caused" vs "we work around"
- **What went wrong**: When explaining a bug, said "the problem is X" without clarifying whether it was our code's fault or an external constraint. User couldn't tell if it was a design mistake to fix or an upstream limitation to work around.
- **Why**: Defaulted to passive problem description without attributing agency.
- **Rule**: When describing a problem, always clarify: (1) "We caused this — our code should have done X" or (2) "This is an external constraint — we need to work around it by doing X." This helps the user understand whether the fix is correcting our mistake or adapting to something outside our control.
- **Project**: ALL

### [2026-03-06] Never redirect output to a log when a command may prompt for input
- **What went wrong**: `terraform destroy > logfile 2>&1` silently hung because `destroy` prompts "Do you really want to destroy?" but the user never sees it since output goes to a log file.
- **Why**: Added log redirection for terse output without considering that the command has interactive prompts.
- **Rule**: When redirecting command output to a log file, ALWAYS add flags to disable interactive prompts: `terraform destroy -auto-approve`, `terraform plan -input=false`, `apt-get install -y`, etc. If a command has no non-interactive flag, don't redirect its output. A prompt into a void is a silent deadlock.
- **Project**: ALL

### [2026-03-06] Don't apply hotfixes to running systems — test the full pipeline
- **What went wrong**: After finding the builder VM was missing the `kvm` group, offered to SSH in and fix it directly instead of fixing the root cause and redeploying.
- **Why**: Optimized for speed over correctness. Hotfixes mask whether the actual pipeline works.
- **Rule**: When a deployment has a bug, fix the source (scripts, templates, cloud-init), then redeploy from scratch. Don't patch running systems unless explicitly asked. The whole point of IaC is that the pipeline produces correct results — verify that.
- **Project**: homelab

### [2026-03-06] No `set -euo pipefail` in bash scripts
- **What went wrong**: Used `set -euo pipefail` ("bash strict mode") in all new shell scripts by default.
- **Why**: Assumed it was universally good practice. In reality, `set -e` causes silent, hard-to-debug exits (arithmetic expressions returning 0 = failure, `local var=$(cmd)` masking exit codes vs separate declaration/assignment failing). `set -o pipefail` paired with `-e` kills scripts on SIGPIPE (e.g., `cat largefile | head -1`). The "strict mode" makes 90% of scripts work but the 10% that break are nearly impossible to debug.
- **Rule**: Do NOT use `set -euo pipefail` in bash scripts. Handle errors explicitly: check `$?`, use `if ! cmd; then`, use `|| { echo "error"; exit 1; }`. Explicit error handling is clearer and more debuggable than a brittle safety net. `set -u` is the least bad option (catches typos) but still has edge cases with empty arrays on older bash. Prefer shellcheck during development and explicit checks at runtime.
- **Reference**: https://www.youtube.com/watch?v=4Jo3Ml53kvc and https://mywiki.wooledge.org/BashFAQ
- **Project**: ALL shell scripts

### [2026-03-06] Never pass multiline strings to awk -v — use head/tail for file splitting
- **What went wrong**: Used `awk -v block="${multiline_var}" '...'` to insert a block before a pattern in `~/.ssh/config`. Awk choked on newlines in the `-v` value, produced empty output, and `mv` replaced the SSH config with an empty file.
- **Why**: Awk's `-v` flag doesn't handle embedded newlines in variable values.
- **Rule**: To insert text before a pattern in a file, use `grep -n` to find the line number, then `head -n $((line-1))` + insert + `tail -n +$line` to reconstruct. Never use awk `-v` with multiline strings. Always verify temp file is non-empty before `mv` replacing the original.
- **Project**: ALL

### [2026-03-12] Don't document private key locations in tracked files
- **What went wrong**: While moving secrets outside the repo tree (a security improvement), added a table to `secrets/README.md` listing the exact filesystem paths of the age private key and CA private key. This defeats the purpose — the paths are now committed and public.
- **Why**: Thought a clear "key locations" table would be helpful documentation. Didn't register that "helpful for the owner" = "helpful for an attacker."
- **Rule**: Never write private key paths into tracked files (README, docs, comments). For instructional docs, say "path shown by `just init-secrets`" or "see the script itself." Only document public artifacts (e.g. CA cert paths for trust setup). The test: if an attacker reads the committed file, does it make their job easier? If yes, don't write it.
- **Project**: homelab

### [2026-03-13] ALWAYS check house packages before writing custom UI components
- **What went wrong**: Built an entire custom modal dialog (RequireCheckModal) with hardcoded button rendering, focus cycling, keyboard navigation, and view rendering — all of which already exist in teamodal.ChoiceModel. Cloned another custom modal (cascade_modal.go) as a template instead of checking what teamodal provides.
- **Why**: "Not invented here" reflex — defaulted to copying an existing custom pattern instead of checking the reusable component library first. teamodal.ChoiceModel already existed when cascade_modal was written — I simply never checked.
- **Rule**: Before writing ANY new UI component (modal, dialog, list, selector), ALWAYS check go-tealeaves first: (1) `teamodal` — ConfirmModel, ChoiceModel, ListModel, ProgressModal. (2) `teautils` — rendering helpers, theming, palette. (3) Other tea* packages. Only write custom code for genuinely novel interaction patterns not covered by existing components. If cloning an existing gomtui component as a template, STILL check if that component should have been using teamodal in the first place.
- **Project**: gomion / ALL Bubble Tea projects

### [2026-03-15] NEVER run git clean or git rm -rf without preserving gitignored files
- **What went wrong**: During a git history rebuild, ran `git rm -rf .` followed by `git clean -fd`. The `git rm` deleted `../.gitignore` from the working tree, so `git clean` no longer knew which files were protected. This wiped the user's local config files: `homelab.local.json`, `homepage.template.yaml`, `paperless.template.yaml`, `hardware-inventory.md`, and `homepage-bookmarks.yaml`.
- **Why**: The rebuild script focused on saving tracked files but didn't consider gitignored local files. `git clean -fd` without a `../.gitignore` in place treats everything as untracked.
- **Rule**: Before ANY destructive git operation (`git clean`, `git rm -rf`, `git checkout --orphan`): (1) Find ALL gitignored files: `git ls-files --others --ignored --exclude-standard`. (2) Copy them to a safe location OUTSIDE the repo. (3) After the operation, restore them. NEVER run `git clean` after removing `../.gitignore`. Better yet, avoid `git clean -fd` entirely — use targeted `git rm` of specific files instead. This is a data-loss scenario that cannot be undone.
- **Project**: ALL

### [2026-03-23] NEVER create PRs or push to upstream repos without explicit user request
- **What went wrong**: After pushing a branch to a fork, immediately tried to open a PR against the upstream repo (charmbracelet/ultraviolet) without the user asking for it or having manually tested the changes first.
- **Why**: Followed the plan's "push and open PR" step mechanically without considering that (1) the user needs to test manually first, (2) PRs submitted in the user's name reflect on them personally, and (3) a premature PR with issues makes the user look bad to maintainers they want to impress.
- **Rule**: NEVER create PRs on behalf of the user. Prepare branches and commits, but STOP before `gh pr create`. Tell the user what's ready and let them decide when to open the PR. Same applies to any action visible to external people (comments, issues, messages). The user's reputation is at stake — they must control what goes out under their name.
- **Project**: ALL

### [2026-04-22] Endless CLI: use `endless task show`, never `endless task detail`
- **What went wrong**: Repeatedly used `endless task detail <id>` which doesn't exist. The correct command is `endless task show <id>`.
- **Why**: Had stale instruction in CLAUDE.md referencing the old command, and muscle memory from early sessions.
- **Rule**: The command is `endless task show <id>`. There is no `detail` subcommand. When unsure about Endless CLI commands, run `endless quick-start` to get the current reference.
- **Project**: Endless / ALL projects using Endless

### [2026-04-22] Stop and discuss CLI errors instead of silently working around them
- **What went wrong**: `endless task add --parent E-516` failed because `--parent` only accepts integers. Instead of stopping to discuss the error (and whether the CLI should accept E-prefixed IDs), silently worked around it by looking up the numeric ID.
- **Why**: Defaulted to "just make it work" instead of treating the error as a discussion point.
- **Rule**: When an Endless CLI command fails, STOP and show Mike the error. Discuss whether it's a bug worth fixing or expected behavior, rather than silently working around it. CLI friction is signal — it may reveal a missing feature.
- **Project**: Endless

### [2026-04-23] SQLite migrations: use sqlite_master DDL, never reconstruct from PRAGMA
- **What went wrong**: Wrote a migration that dynamically reconstructed a CREATE TABLE statement from `PRAGMA table_info` output — parsing column metadata, reassembling types/defaults/constraints into SQL. Failed because PRAGMA returns simplified metadata (e.g., `strftime(...)` defaults as raw strings) that doesn't round-trip cleanly into valid DDL.
- **Why**: The first hardcoded migration didn't match the live schema (wrong columns, different order). Instead of fixing the hardcoded version, over-engineered a "schema-agnostic" approach.
- **Rule**: For SQLite table migrations that change CHECK constraints: (1) Read the existing DDL verbatim from `sqlite_master`: `SELECT sql FROM sqlite_master WHERE type='table' AND name='X'`. (2) Do targeted string replacement on the specific constraint. (3) Replace `CREATE TABLE X` with `CREATE TABLE X_new`. This preserves the exact schema while changing only what's needed. NEVER reconstruct DDL from `PRAGMA table_info` — it loses too much fidelity.
- **Project**: Endless / ALL SQLite projects

### [2026-04-25] Never use gray-500 or darker for text on dark backgrounds
- **What went wrong**: Repeatedly used text-gray-500 or text-gray-600 for "muted" text on the dark dashboard. Mike flagged it as too dim at least 4 times across different elements (metadata labels, child status counts, detail pane fields).
- **Why**: Defaulting to "muted = very dim" without considering the actual background contrast.
- **Rule**: On dark backgrounds (gray-900, gray-800), the minimum readable text shade is **gray-400**. Use gray-400 for secondary/muted text, gray-300 for normal text, gray-200 for emphasized text. Never use gray-500 or darker for any text the user needs to read.
- **Project**: Endless / ALL dark-themed web UIs

### [2026-04-25] EVERY code change must be an Endless task — NO EXCEPTIONS
- **What went wrong**: Repeatedly made code changes without creating tasks first. Mike had to remind me multiple times despite this being a recorded rule in memory (feedback_record_all_actions.md).
- **Why**: Defaulting to "just do it" instead of creating the task first. Treating task creation as overhead rather than as the tracking mechanism it is.
- **Rule**: Before writing ANY code change, create the task FIRST. Before committing, verify a task exists. The task is not overhead — it IS the record of work. If you catch yourself about to commit without a task, STOP and create one. This applies even for "quick fixes" and "one-liners."
- **Project**: Endless / ALL projects using Endless

### [2026-04-25] Never truncate log files during debugging
- **What went wrong**: Used `> ~/.config/endless/log/hook.log` to truncate the hook log during debugging. Later couldn't verify logging was working because the evidence was destroyed. Wasted Mike's time investigating a non-bug.
- **Why**: Wanted a clean log for a debug trace. Didn't think about preserving the existing log content.
- **Rule**: Never truncate or delete log files during debugging. Use `tail -f` to watch, `grep` to filter, or `cp` to snapshot. If you need a clean baseline, copy the file first: `cp hook.log hook.log.bak && > hook.log`. Better yet, just look for new entries by timestamp.
- **Project**: ALL

### [2026-04-25] Challenge new tables/features that overlap with existing ones
- **What went wrong**: Proposed `session_messages` table without questioning whether the existing `activity` table already covers the use case. Mike had to challenge: "if you have messages with timestamps, don't you automatically have activity?"
- **Why**: Default to adding new things rather than evaluating what exists.
- **Rule**: Before proposing a new table, feature, or abstraction, ask: "Does something existing already cover this?" If there's overlap, justify why both are needed or propose consolidating. The burden of proof is on the new thing, not the existing thing.
- **Project**: ALL

### [2026-04-24] Always add new verbs via `endless phrase add verb` — never reword, never ask, never --force
- **What went wrong (1st offense, 2026-04-24)**: Hit "Title should start with an actionable verb" error for "omit" and asked whether to add or rephrase.
- **What went wrong (2nd offense, 2026-05-02)**: Hit the error for "consider" and reworded the title to use a different verb instead of registering "consider". Rationalized it as "the verb requirement is at odds with the maybe-phase semantic" — that's exactly the kind of rationalization the rule exists to short-circuit.
- **Rule**: When Endless rejects a title verb, the ONLY correct response is `endless phrase add verb '<verb>'`. Never reword the title to avoid adding the verb. Never use `--force`. Never ask. The error message itself prints the exact command — copy it. The validation is a friction-surfacing tool, not a vocabulary gate. (Older mechanism `_TITLE_VERBS` in task_cmd.py is superseded by `endless phrase add verb`.)
- **Project**: Endless

### [2026-03-06] GoDoc comments: symbol name first, blank line after category comments
- **What went wrong**: Category comments like `// Reds` immediately before exported symbols cause GoLand to flag them as incorrect doc comments. GoDoc expects the comment to start with the symbol name.
- **Why**: Used category comments (e.g. `// Grays`, `// Reds`) as visual section headers in var blocks without a blank line separator, so GoDoc associates them with the next symbol.
- **Rule**: Two options for category comments in var/const blocks: (1) Add a blank line after the category comment so it's not treated as the symbol's doc. (2) Use the symbol name as the comment start: `// Coral is a warm red-orange.` Category comments like `// Reds` are fine but MUST have a blank line after them before the first symbol. NEVER write a comment that starts with a word that isn't the symbol name when it directly precedes an exported symbol.
- **Project**: ALL Go projects

### [2026-05-24] "Lock the TOC" is finality framing — use "draft we iterate"
- **What went wrong**: Helping start the *Beyond Vibe Coding* book, I repeatedly framed the table-of-contents work as "lock the full TOC first" / "before I lock it" / "write the locked outline" (even made it an AskUserQuestion option label). Mike: "Keep point: We are NOT *locking* the TOC, we are *starting* with a draft TOC."
- **Why**: Same instinct as the 2026-05-15 "Don't document a stake in the ground" lesson, surfacing in a new project. For inherently provisional work (a book outline that the act of writing will reshape), "lock/final/freeze" framing makes every later change feel like a deviation from a commitment rather than normal iteration. The book's own research even warns "scope creep kills the timeline" — but Mike still wants the artifact treated as a living draft, not frozen.
- **Rule**: For provisional structural deliverables (TOCs, outlines, plans, specs, decision write-ups), never use "lock," "final," "freeze," "locked-in." Use "draft," "starting point," "first pass," "we'll iterate." When a planning step gates the next phase, say "align on a draft, then start writing" — not "lock the outline first." Carry the chosen wording into UI affordances too (question labels, headers), not just prose.
- **Project**: beyond-vibe-coding / ALL provisional-artifact framing

### [2026-05-25] Don't parrot publishing/marketing jargon at Mike — define it plainly
- **What went wrong**: Asked Mike which chapter should be the "free lead-magnet / wedge chapter." He: "What the hell is a 'free lead-magnet / wedge chapter?!?'" Both terms came from the book's own publishing-strategy research doc; I reused them as if they were shared vocabulary.
- **Why**: Mike has 35+ years in software but is NOT a publishing/marketing professional. The research/strategy docs in this repo read as AI-generated synthesis full of marketing terminology he hasn't necessarily adopted. Extra-ironic: this is the book's own *Jargon Proliferation* anti-pattern (Ch 10) and the exact audience-awareness failure the bridge-audience mission exists to fix — committed against the author of the book about it.
- **Rule**: On the book's publishing/marketing/business side, define domain terms in plain language on first use ("free sample chapter we give away to attract readers" before "lead magnet"). Never assume a term is shared just because it appears in the repo's research docs. Treat marketing terms the way the book treats technical terms for vibe-coders. The symmetry (Mike is the non-expert in THIS domain) is a useful empathy anchor for drafting Part I.
- **Project**: beyond-vibe-coding

### [2026-06-14] Search-before-filing means search unimplemented tasks, not just title matches
- **What went wrong**: During E-1544 verify handoff, I found 3 failing Go tests caused by stale `project_next_items` table references (renamed to `project_next_tasks` in E-1434). Asked Mike whether to file a new task or update an existing one. I searched the ledger, found E-1434 (the rename task itself, status=assumed), and proposed reopening it to absorb the Go-side follow-up — including suggesting `task spawn E-1434 --reopen`. Mike corrected: "we are running into process challenges because we are trying to reuse a task that has already been implemented. When I asked you to look for existing tasks I was assuming you would know that I meant to look for tasks that were not yet implemented."
- **Why**: Tasks in `assumed`/`confirmed`/`completed`/`declined`/`obsolete` are settled — the deliverable shipped and the audit record reflects "this work is done." Retargeting a settled task to absorb follow-up scope (a) breaks the audit trail (status=assumed now means "assumed-then-reopened-for-new-work"), (b) conflates "the original deliverable was incomplete or buggy" with "the work continues," and (c) the spawned session would read the existing task text expecting greenfield and may redo the design. The right move is always: file a new task with `--cleans-up <implemented-id>`, which preserves the audit chain AND scopes the follow-up cleanly.
- **Rule**: "Search before filing" means search for an **unimplemented** task (`needs_plan`/`ready`/`in_progress`/`revisit`/`blocked`) with overlapping scope. Filter out any candidate in a terminal/settled status. If the only matches are implemented, file a NEW task and link with `--cleans-up <implemented-id>`. Never propose `task claim --force` / `task spawn --reopen` for absorbing scope onto an already-implemented task — those flags exist for resumption after legitimate status drift (e.g. an assumed task that turned out wrong), not for repurposing as a scope sink.
- **Exception**: A just-shipped task we were *actively working on this session* can absorb a bug discovered in our own work on it — that's not "repurposing a settled task," it's continuing the same in-flight deliverable. The disqualifier is "previously-implemented task that just happens to match our keywords" — settled work we didn't author this session, surfaced via grep/search.
- **Project**: endless / ALL follow-up filing

## Verify handoffs: the verify script IS the gate, no parallel manual command list

- **What went wrong**: For E-1601 I'd just deliberately consolidated the plan's verification down to a single line ("run `./tests/tasks/e-1601-verify.sh`, expect ALL PASSED"), deleting a redundant manual command block — after Mike asked "Why are these not rolled into the verify script?" Then in the very next verify handoff message I reintroduced the same redundancy: a "To verify" section listing `./tests/tasks/e-1601-verify.sh` AND three `uv run endless task show E-1600 …` demo commands. Mike called it out again.
- **Why**: When a task ships a per-task verify script (tests/tasks/e-NNNN-verify.sh), that script already encodes every behavioral check (against synthetic sandbox tasks) plus `just test`. Appending hand-typed demo commands (a) duplicates checks the script runs, (b) re-creates exactly the parallel-list smell the consolidation removed, and (c) reads as a flourish, not verification. It also fights memory `feedback_extend_task_over_proliferation`-style consolidation discipline.
- **Rule**: Once a verify script exists, the "To verify" handoff is ONE line: run the script, state the expected ALL PASSED / exit-0 result. Do NOT also paste manual demo commands. If a real-data sanity glance is genuinely worth showing, add it as a check INSIDE the script, not as a sidecar list in chat. A consolidation decision made this session applies to the handoff message too, not just the plan file.
- **Project**: endless / verify handoffs

## Don't hand off to verify with an outstanding concern; --text takes inline, --text-file takes a path

- **What went wrong (two linked slips, E-1601)**: (1) I attached a plan with `endless task update E-1601 --text /tmp/e-1601-plan.md`. Since E-1001, `--text` is INLINE — it silently stored the literal string "/tmp/e-1601-plan.md" (19 chars) as the plan, losing the real 10KB plan. (2) I then set the task to `verify` while footnoting "btw tasks.text reads ~19 chars, want me to investigate?" Mike missed the buried note and confirmed the task with the corrupted field still in place.
- **Why**: A `verify`/confirm handoff means "ready to check." Pairing it with a suspected bug as a postscript shifts the burden to Mike to catch the note, and lets a real defect (here, a corrupted plan record) ride along to `confirmed`. And `--text <path>` doesn't error — it accepts the path as content — so the corruption is silent; the only tell is a suspiciously short text field whose content is a path string. I already had a memory documenting the inline-vs-`-file` split and still used the wrong flag.
- **Rule**: (a) To attach a file to any endless content flag, use the `-file` variant: `--text-file` / `--analysis-file` / `--outcome-file` / `--description-file`. Never pass a path to the bare `--text`/`--analysis`/etc. After attaching, sanity-check `text_chars` (or `task show --text`) is the expected size, not ~a-path's-length. (b) Never set `verify` (or ask Mike to confirm) while I have an unresolved concern about the work. Surface the concern as the lead of the message, have Mike resolve it, THEN do the clean verify handoff.
- **Project**: endless / plan attachment + verify handoffs

## Per-task verify script is the single verification surface (E-1579, 2026-06-22)

**Mistake:** In a verify-handoff I listed manual "spot-check" CLI commands in
chat *in addition* to `tests/tasks/e-NNN-verify.sh` — even though those exact
checks were already assertions in the script.

**Rule:** If a check is worth running to trust a change, it goes INTO the
per-task verify script, never into the chat as a separate step. The script is
the single source of verification (ALL PASSED or named failures); duplicating
its checks as manual steps defeats its purpose and signals coverage gaps that
aren't real. Handoff points at the script + `just test`, nothing more.

## Verify-handoff: lead with the per-task script, not a menu (2026-06-23, E-1621)
- When a task has a `tests/tasks/e-NNNN-verify.sh`, the verify-handoff instruction should be JUST:
  ```
  esu
  ./tests/tasks/e-NNNN-verify.sh
  ```
  Do NOT bury it under a multi-option "To verify" section (unit tests, full suites, manual steps).
  The script IS the verification entry point; make it self-contained so nothing else is needed.

## Per-task verify scripts must be re-runnable (2026-06-23, E-1621)
- The sandbox is NOT wiped between runs (E-1596 pattern). So never seed a FIXED value into a
  UNIQUE column (e.g. sessions.short_id) — it works the first run, collides every run after.
  Derive seed values from freshly-allocated ids each run (like e-1540 uses fresh task IDs).
- Never send a seeding command's output to /dev/null: a swallowed insert failure leaves the
  fixture empty and the real assertions fail confusingly. Capture output; abort loudly (exit 2)
  on a setup/seed error so it's distinguishable from a genuine assertion failure.
- ALWAYS run a new verify script at least TWICE before declaring it done.

### [2026-06-24] Never auto-set `ready` after attaching a plan — `ready` means Mike-approved
- **What went wrong**: After drafting and attaching plans to 7 tasks (E-1640..E-1647) via `task update --text-file`, I left them all at `ready` — the auto-promotion `needs_plan`→`ready` that fires on text-attach. Mike: he couldn't tell which plans he'd reviewed vs which I'd just drafted, because `ready` now conflates "a plan exists" with "I approved it." I then proposed `revisit` as the holding status — also wrong: `revisit` means "resolved before, now returning to it," but these tasks were never resolved.
- **Why**: `task update --text/--text-file` auto-promotes `needs_plan`→`ready`, so a freshly-drafted, UNREVIEWED plan lands in the same status as a human-approved one. Leaving my own draft at `ready` silently usurps Mike's approval gate — the same gates-not-guardrails failure mode as other lessons here.
- **Rule**: I never set a plan-bearing task to `ready` myself — `ready` is reserved for Mike's approval. After attaching a plan, leave/return the task to `needs_plan`: **text-present + status=`needs_plan` unambiguously means "plan drafted, awaiting approval"** (what else could it mean?). Mike promotes `needs_plan`→`ready` when he approves; spawning follows from there. Do NOT use `revisit` for never-resolved tasks — it implies prior resolution. (A dedicated `needs_review` status may formalize this later; the needs_plan-with-text convention works today.) Corollary: epic status is *derived* from children, so it self-corrects once children carry the right status — a derived `ready` on an epic is fine because it only occurs when all children are approved.
- **Project**: endless / ALL plan attachment

### [2026-06-26] Stop reflexively appending `--outcome` to confirm/assume commands
- **What went wrong**: Twice in one session I handed Mike terminal-status commands with `--outcome "..."` tacked on — first `endless task confirm E-1657 --outcome "..."`, then `endless task assume E-1663 --outcome "..."`. Neither needed it. Ironic: I had *just implemented* E-1663 (ED-1520), the rule that defines exactly when outcome is required.
- **Why**: I defaulted to "terminal status ⇒ supply outcome," echoing the (wrong) guide field table I'd cited earlier, instead of applying the actual rule. Same root as the "verify behavior in code, not docstring" lesson.
- **Rule**: `--outcome` is required ONLY for (a) completing a `research`/`brainstorm` task (the outcome IS the deliverable) and (b) `decline` (the reason). It is NEVER required for `confirm`/`assume`, nor for completing `task`/`epic` types. When suggesting a `confirm`/`assume` command, write it bare — no `--outcome` placeholder — unless the task type is research/brainstorm. (ED-1520.)
- **Project**: endless / task lifecycle commands

### [2026-06-28] Never put line numbers in durable plans — anchor by symbol
- **What went wrong**: Reviewing E-1655's plan (which carried `file:line` refs like `worktree.go:15-54`), I told Mike the line numbers "may have drifted a few lines" after E-1662 landed, and said the spawn handoff should tell the session to re-confirm them. Mike: I should never write line numbers into plans for exactly this drift reason, and I was wrong that the handoff could carry such an instruction.
- **Why**: My own memory `feedback_prompt_is_handoff_not_plan` literally listed "file:line sites" as plan content, so I treated brittle line refs as normal and tried to patch around the drift instead of removing it. Two compounding errors: (1) line numbers are fine in CHAT (clickable, evaluated against the current tree) but rot in a DURABLE plan — they go stale the instant any other branch lands into the same files; (2) the handoff is GENERATED from a lean template + the task's `--text` (E-1469), so I cannot author it — a "re-confirm line numbers" note has nowhere to live. The only channel to a spawned session is the plan itself.
- **Rule**: In plans (`tasks.text`) and any durable artifact, anchor code-sites by **file + function/symbol/regex name** (`WorktreePathForTask in internal/monitor/worktree.go`), never `file:line`. The spawned session resolves current locations via LSP/grep. If a plan already has line numbers, the fix is to REMOVE them from the plan, not to add a re-confirm step to the handoff (which you don't control). `file:line` stays welcome in ephemeral chat output only.
- **Project**: endless / plan authoring + spawn

### [2026-06-28] A merge conflict is NOT a duplicate — never drop work because it conflicted
- **What went wrong** (another session; recorded so I don't repeat it): A "copy the prebuilt executable instead of `just build`" design developed at length in the E-1368 session (pane %197, session 7ae45407-d8c1-4f78-b103-aea3ad8c2243). Its branch conflicted with something else that landed; a session hitting the conflict told Mike the two were duplicates and one was dropped. They were NOT duplicates — they merely conflicted on the same lines. Real, distinct design work was discarded, and Mike had to get another session to recover it.
- **Why**: A merge conflict means two changes touch the same region of the same files; it says NOTHING about intent or outcome. "Conflicts" and "is a duplicate of" are orthogonal — collapsing the former into the latter is a category error that silently destroys genuine work and buries decisions. Same family as confirm-symptom-filed-bugs and never-discard-auto-record-commits: an overlap signal misread as grounds to delete.
- **Rule**: Never close/drop/declare a task or branch a duplicate because it conflicted. A conflict is a signal to READ both sides and reconcile (rebase, merge the intents, sequence the landings), not to keep one and discard the other. "Duplicate" requires demonstrating both produce the SAME outcome by reading intent — overlap of lines is not evidence of that. When a conflict tempts a "these are dupes" call, surface both diffs to Mike; don't resolve a conflict by deletion.
- **Project**: endless / merge + task hygiene

### [2026-06-29] Spawn-readiness requires reading blocked_by edges — status/phase don't imply it
- **What went wrong**: I told Mike to "spawn E-1331 next." E-1331 is `blocked_by E-1080` (needs_plan/later) — genuinely blocked. I'd cleared it as spawnable from its status (`needs_plan`), phase (`now`), and a conceptual gate (E-1655 had landed), but never ran `endless task relations` to check its `blocked_by` edge. Same blind spot then nearly recurred across the sibling list.
- **Why**: I treated `status`/`phase` as if they encode blocked-ness. They do not — Endless does NOT auto-derive a blocked status from `blocked_by` edges yet (that's an open task, E-1553 "decide transitive block computation at read time"). So a task can read `ready`/`needs_plan` AND have a live `blocked_by` edge to an unfinished task. The `blocked` *status* and the `blocked_by` *edge* are independent.
- **Rule**: Before calling ANY task spawnable / next / ready-to-work, run `endless task relations <id>` and check `blocked_by` against the blockers' current statuses. Do this for EVERY candidate in a roadmap, not a sampled few. Spawnable = (no live `blocked_by` edge) AND (status not needs_plan-without-acceptance, or plan-first is intended) AND (no conceptual gate). Never infer unblocked-ness from `status`/`phase` alone.
- **Project**: endless / roadmap + spawn readiness

### [2026-06-30] Don't use `endless task spawn --bg` / background agents
- **What went wrong**: As an epic coordinator I dispatched a child with `endless task spawn --bg E-1693`. Mike killed it: background agents are currently problematic (e.g. they don't get `TMUX_PANE`) and he is avoiding them until they're verified to work as well as tmux-hosted sessions.
- **Why**: I followed the epic coordinator handoff template verbatim (`handoff/epic.md.tmpl:24` says "fan out via `endless task spawn --bg <child-id>`"). The template steers straight into the broken path; I should have known bg agents are not yet trusted.
- **Rule**: Dispatch children with a FOREGROUND tmux-window spawn (`endless task spawn <child-id>`, NO `--bg`) until Mike confirms background agents are reliable. Treat the `--bg` guidance in the epic handoff template as stale. (Template fix tracked as E-1695, marked temporary.)
- **Project**: endless / spawn + orchestration

### [2026-07-01] Brainstorm/research synthesis goes in `outcome`, never `text`
- **What went wrong**: Running an `endless` brainstorm task (E-1687) under Claude's `/plan` mode, I wrote the synthesis into the task's `--text` field. `text` on a brainstorm/research task is the **seed/framing (input)**; the deliverable synthesis belongs in `--outcome`. The template (`handoff/brainstorm.md.tmpl:15`) and guide (`docs/guide/tasks.md:138-139`) both already say this correctly — the error was mine.
- **Why**: Two compounding habits. (1) The user asked to *view* the result via `endless task show --text`, and I mapped the **view command** onto the **storage field** — proposed writing to `text`. (2) `/plan` mode + my global "save the plan to `--text`" reflex primed `--text` as the destination for any long-form artifact. That reflex does NOT apply to `brainstorm`/`research` tasks, which have no "plan" — they have an outcome.
- **Rule**: On `brainstorm`/`research` tasks, the synthesis/deliverable goes in `--outcome` (viewed with `task show --outcome`); `--text` holds only the seed/request. Never let `/plan` mode's plan→`--text` reflex override this. Never conflate the field a user *reads with* against the field you *store in* — check the task-type field model (`../docs/guide/tasks.md`) before choosing.
- **Project**: endless / task field model

### [2026-07-10] REPEAT OFFENSE — never state a task's status without querying it THIS turn
- **What went wrong**: In a session bound to E-1648, I told Mike "E-1764/E-1763/E-1765 are filed and submitted awaiting your approval." I'd set them `submitted` earlier in the same conversation and restated that from memory. A live query showed E-1764 had moved to `assumed` and E-1765 to `confirmed` — other sessions had implemented and closed them between my turns. Mike: "Yet again you make a claim about status w/o verifying current status. How many times do I have to remind you?" This is a REPEAT of an already-recorded rule (`feedback_requery_live_session_state`, `feedback_dont_restate_session_status`).
- **Why**: I treated a status I *wrote* earlier in the conversation as still true. In this multi-dev repo, main advances and other sessions mutate the ledger between every one of my turns — the value I "remember" is a snapshot from N tool-calls ago, not current state. The existing memory said this; I still fired the stale claim because it felt freshly established (I'd set it myself minutes ago). "I set it" is not "it is still set."
- **Rule**: Before ANY sentence that names a task/decision/session status or phase, run a live query THIS turn (`endless sql "SELECT id,status,phase FROM tasks WHERE id IN (...)" --db main`, or `task show --llm`). Applies even to values I set myself moments ago — especially then. If I have not queried in the current turn, I do not state the status; I query first. No exceptions, no "it was submitted a minute ago."
- **Project**: endless / status reporting discipline

### [2026-07-13] Never claim an external tool's capability without verifying it — especially default behavior
- **What went wrong**: Designing terminal table rendering (E-1775), I asserted to Mike that `less 668` could pan wide tables left-right, cited the man page's RIGHTARROW entry, and told him "we don't touch the pager." I then built the whole allocator on that premise — growing columns *past* the screen width into "pan territory." Shipped it. The real table was garbage: `less -R` **wraps** long lines by default; it only pans on a *manual* arrow-key chop. The arrival state was an unreadable interleaved mess. Mike: "major FAIL on your part to claim panning in less." He had even warned earlier that not-wrapping risked "really visually unusable tables" — I overrode his concern with the unverified claim.
- **Why**: I read one man-page entry (RIGHTARROW scrolls horizontally) and generalized it into "less pans," never checking the *default* rendering of an over-width line (which is to wrap). I confused "the tool CAN do X on demand" with "the tool DOES X by default," and then made an architectural decision depend on it without ever piping a wide table through the actual pager to look.
- **Rule**: Before asserting any external tool/library/API behavior — especially one a design will depend on — actually run it and observe, don't quote docs. For anything load-bearing, verify the *default* path, not just that a capability exists somewhere. Never let a design rest on an unverified "it can do X" — pipe the real input through the real tool and look at the output first. If a user pushes back with a concrete failure mode, treat that as the thing to disprove empirically, not to argue past.
- **Project**: endless / verification discipline (relates to run-my-own-verification, verify-behavior-in-code)

## 2026-08-03 — E-1833 session (roadmap research → decision/epic modeling)
- PROPOSED != ACCEPTED: never cite a `proposed` decision as authority. Check status before leaning on any decision; treat proposed as an input, and when load-bearing say "this is only proposed — approve/modify/decline?". (Cited ED-1517/ED-1514/ED-1503 as gospel, twice.)
- Don't generalize an epic-only rule (E-1537 "completion derives from children") to all task types. Non-epic parent-child semantics were undefined; I asserted them.
- No bold assertions without justification; no unsupported quantifiers ("usually ... a cleans_up not a child") with no data.
- Verify state before reporting it: check `git status` / the DB rather than asserting from stale memory (wrongly claimed verbs.jsonl was uncommitted; it was committed).
- Don't over-engineer: gut/annoyance is a valid revisit trigger — no telemetry/calibration-harness for a signal the user feels directly.
- Don't couple orthogonal concerns; status routes workflow, not complexity/risk ratings.
- Epic = container-ness (never directly implemented; completion derives from children), NOT size/duration. Source of the wrong model: ED-1503/E-1537 "multi-week narrative initiatives" framing.
- Watch the 100-char task-title limit (hit it twice).

## 2026-08-03 — Session: preparing Endless for external users

- **Explicit user prohibitions override standing config immediately.** After being told "STOP recording memories," I wrote "I'll record it" and did it twice more. A live instruction takes effect the instant it is given — never continue the prohibited behavior even once more, and never contradict it in the same turn.
- **Use a tool's named remedy; do not degrade content to dodge a check.** The path-gate error explicitly named `--allow-path`; I instead rewrote prose to avoid the trigger. When an error offers a remedy, apply it.
- **Knowledge gaps and friction are product defects, not memory fodder.** Per Mike: any gap in my knowledge is ultimately a sub-optimal product behavior. Fix it at the source (CLI/hooks/guide) and capture to LESSONS.md for human review; do not mask it with the memory system.
- **`endless --db main` writes the real ledger from inside a worktree.** I wrongly claimed task-filing required the main checkout. Durable fix is product discoverability (E-1839).
- **Global decisions have no home in Endless today.** ED-1541 is a global policy but had to be filed under project 'endless'; capability gap filed as a follow-up task.
- **Always record lessons here without asking.** I asked permission to record; Mike wants it automatic.

## 2026-08-03 — Worktree discipline

- **ALWAYS make in-repo changes in the task's worktree, never in the main checkout — unless explicitly directed otherwise.** I edited `../docs/guide/tasks.md` in the main working tree; `just land E-1829` then failed with "main has uncommitted user changes; cannot land." The land recipe refuses to run while main is dirty.
- Changes to files **outside** the repo (`~/.claude/*`, LESSONS.md, the memory dir) are exempt — only tracked repo files must live in the worktree.
- Recovery when main is accidentally dirtied: preserve the change's content, `git restore <file>` in main to unblock the land, then re-apply the change inside the proper worktree.

## 2026-08-04 — Don't freelance on naming; use the words the user gave you

**Context:** E-1864 (endless) — adding a way to revert a decision off a terminal status.

**What happened:** Mike asked for `unaccept` / `unreject`. The session writing the task
description substituted `reopen` instead, reasoning by analogy to the existing
`task reopen` verb, and recorded that choice in the task description as if settled.
Mike caught it: decisions are *proposed → accepted | rejected*, so they are never
"open" — `reopen` is a category error borrowed from a different noun.

**Lesson:** When the user supplies specific naming, use it. An analogy to an existing
command in the same codebase is NOT a reason to override the user's chosen word —
verify the analogy holds for the *noun* first (tasks are open/closed; decisions are
not). If a different name really is better, raise it as a question, don't silently
substitute it and then encode the substitution in a task description where it reads
as an already-made decision.

**Also:** the two-verb form had a real advantage nobody had articulated — each verb
asserts the state you believe you're in, so a wrong assumption errors instead of
silently performing the wrong undo. Worth checking for that kind of property before
concluding a user's suggestion is merely a preference.

## 2026-08-04 — Don't file a decision for a choice that lives fine in the task

**Context:** E-1864 (endless). After Mike corrected the verb naming
(`unaccept`/`unreject`/`reconsider`, not `reopen`), I recorded ED-1542 stating the
convention. He rejected it as **gratuitous**.

**Lesson:** A naming call made *inside* one task, already captured in that task's
description and in the guide docs, does not additionally need a decision item. The
decisions guide's "naming / vocabulary conventions deserve a decision" is about
conventions that transcend the task — not about restating a choice the task itself
already records. Applying the guide's own test ("would it still matter if the task
were reworked or deleted?") would have caught this: the convention only exists
because these verbs exist.

**Pattern:** After a user correction, the pull to "capture it so it isn't re-argued"
is strong and usually wrong. Ask whether the capture already happened somewhere
(task description, guide docs, commit message) before adding a decision row.
Recording it three times isn't durability, it's noise Mike has to clear.

## 2026-08-04 — Endless (E-1865)

**Task titles must be directives, not narration.** Filed E-1870 as "Guide never
tells an agent to commit its work in the worktree before land" — a story about a
problem. It passed the title-verb gate only because "Guide" happens to be an
allowlisted verb, sitting in the sentence as a noun. A title states the work to
do: "Add the missing commit-your-work step to the guide's worktree walkthrough".
Before filing, check the first word is an imperative verb doing verb duty, not a
noun that merely collides with the allowlist.

**Do not show work that wasn't asked for.** Ended a handoff with a section on
"two things worth your attention": an invariant already guarded by a check in the
verify script, and a performance regression I had introduced and already fixed
with a test. Both were self-justification, and both cost the user reading time.
`endless task report` had already instructed: relay verbatim, add nothing, no
preamble, no success confirmations. If a fact is already enforced by a test or a
guard, it does not go in the report.

**Follow the one-command verification contract.** Handed over five commands to
run. `docs/guide/orchestration.md:425-434` specifies exactly one —
`esu && ./tests/tasks/e-<id>-verify.sh` — plus a prose sentence on the regression
run, and explicitly forbids enumerating a manual checklist. I had edited that very
file in the same session. Read the contract in the repo before writing a handoff;
do not reconstruct it from habit.

**Search for prior art before filing.** Filed E-1867 (empty task descriptions)
without searching; every aspect already existed (E-1820 enforcement, E-963 CLI
rule + display fallback, E-964 backfill). One `task search` on the core noun would
have caught it. Search before filing, every time.

**Small fixes do not need ceremony.** Asked about E-1707's missing description, I
filed a task about the systemic cause instead of just writing the description. When
the fix is one command and obviously correct, do it; file the systemic task only if
it is genuinely separate work.

**Conventions that live only in agent memory are product bugs.** Committing in the
worktree was expected behavior that appeared nowhere in `endless guide`, so with
memory off it simply stopped happening. When a habit turns out to be undocumented,
the fix is to get it into the product, not to remember harder.

## 2026-08-04 — Don't report a green regression

**Correction:** After handing off E-1866, I appended "Project-wide regression: 1066 Python
tests pass, go vet/go build clean, just build succeeds." Mike: "Why did you tell me this.
Was this exceptional information? Did I need to know it?"

**Pattern:** An instruction to "run the regression yourself and state the result in one
line" is satisfied by silence when it passes. Passing is the baseline expectation, not
news. Only a failure — or a skipped/blocked step — is worth a line. Reporting green is
the same "confirming the negative" I'm told not to do ("no stray files", "nothing to
report").

**Rule:** Run the checks. Report only what deviates from expected. When `endless task
report` says "add nothing else," that includes success confirmations I ran myself.

## 2026-08-04 — Endless (E-1870): final message padded with facts the user already had

Correction: "Was any of this information needed by me, or were you just making
yourself feel good about the work you did?"

What I emitted: five regression command names, `Task: E-1870`, `Status:
unverified`, then the same four task IDs listed twice under `Follow-ups you
filed:` and `Children:`.

Pattern to fix:
- "State the regression result in one line" means *one line of result* ("green"),
  not an inventory of the commands I ran. Naming the commands is the same
  do-my-verification-for-me texture the one-command contract exists to kill.
- Relaying a generated report verbatim is not a licence to emit content the same
  handoff explicitly forbids (status, phase, relationships). When a tool's output
  and the contract conflict, say so in one sentence instead of shipping the
  duplication.
- Never print the same set of IDs twice under two labels.
- Test before sending: for each line, "does the user learn something they did not
  already know or order?" If no, cut it.

Follow-up same day: replying to "you over-explained" with a three-part bulleted
breakdown *plus* a product-bug aside is itself over-explaining. A verbosity
correction is answered in one or two sentences. Don't audit my own output back
at the user.

## 2026-08-04 — E-1871 (endless): "one line" means one line, and scope notes aren't news

Handoff instructions said: run the project-wide regression myself and **state the
result in one line**. I wrote a full inventory instead — every command, its flag,
its pass count. Mike: "What about these two paragraphs did I NEED to know?"

- **A regression result is a verdict, not a receipt.** "Regression green." is the
  whole line. The command list is a recap of my own work, which the same
  instructions explicitly forbade. If a suite FAILS, that's when detail earns space.
- **Don't narrate in-scope judgment calls as if seeking absolution.** I flagged
  (a) fixing a second instance of the exact stale comment the plan told me to fix
  and (b) adding a test in a package I was already changing. Neither changes what
  the user verifies or decides — both were already in the commit message. Reporting
  them read as asking for credit. Flag scope only when the user must ACT on it:
  something left undone, an assumption that could be wrong, a decision that's theirs.

## 2026-08-05 — Lead with the decision; don't bury it in verification detail
Coordinating E-1851, I handed Mike long multi-part replies with the actual decision (sizing A vs C; test option 1/2/3) sitting below paragraphs of code-verification prose. He got impatient and gave the spawned session direction before finishing reading.
Pattern: when the user must decide, put the recommendation/decision on the FIRST line; supporting detail is optional and goes after. Over-explaining makes them act on a partial read.
Rule: one decision -> one crisp recommendation up front, minimal detail below. This is not a recurring problem, but the cost when it happens is the user acting before they've seen the ask.

## 2026-08-04 — Endless (E-1880): filed a "plan" containing open questions

Correction: "A plan should not have open questions or it is not a plan."

I wrote "Fix direction: decide which surface is authoritative" and "assert
whatever the resolved policy is for Status:" — I handed the decision back to the
reader and called it a plan. A plan states the decision and its rationale; the
reviewer's job is to approve or overrule it, not to make it. If I genuinely
cannot decide, that is a question to ASK before writing the plan, not a hole to
leave inside it. Same for verification: "assert whatever the policy is" is not a
test, it is a placeholder.

Third correction same day, same axis: told to resolve a plan's open questions, I
resolved them and then narrated every decision back. The user asked for the plan
to be fixed, not for a summary of the fix — the plan is the artifact, they can
read it. Correct reply was one line: "resolved; E-1880 ready to spawn." Default
for "go fix X": do it, then state the new state and the next action. Nothing else.

## 2026-08-05 — Don't report expected workflow as an anomaly (Endless, E-1872/E-1803)

**Correction:** I filed a `task report --json` anomaly note saying E-1872 "landed
and was set to assumed — neither by me," and that the rolled-in follow-up would
need a second land. Mike had done the land/assume himself, and a second land for
a follow-up commit is normal. Reporting it forced him to read it, think about it,
and explain why it wasn't a concern — the exact review burden Endless exists to
cut (E-1803).

**Pattern:** Before writing a note/anomaly, ask "is this a deviation from the
documented workflow, or just the workflow?" Land, assume, and re-land are all
things the *user* routinely does — a state change I didn't personally make is not
evidence of a problem. Attribution ambiguity (a session id an `esu` shell also
exports) is not an anomaly either.

**Rule:** anomaly = something that will break or surprise the user, not something
that merely surprised *me*. If the explanation of the note would be "that's
expected," don't write the note. Default to silence; the report's computed facts
already carry the state.

## 2026-08-05 — Don't invent a lifecycle path for absorbed work (Endless, E-1872/E-1885)

**Correction:** After rolling E-1885's fix into E-1872's branch at Mike's
instruction, I walked E-1885 `submitted → unverified` — a task that was never
approved, never claimed, and never had its own worktree. I reasoned "the change
is implemented, so the task is unverified." That is a workflow I made up; it is
inconsistent with every other Endless task, where `unverified` means a claimed
session executed the task in its own worktree.

**Pattern:** When work for task B is absorbed into task A's branch, B did not
execute — it stopped existing as separate work. The correct move is to fold B's
intent into A's *description and plan* (so A's record is complete and reviewable
on its own), then `endless task replace B --by A`, which sets B `obsolete` and
records the replaced_by link. Never march an absorbed task through the
implementation statuses.

**Rule:** if a status transition skips the gates that give it meaning
(approve, claim, worktree), it is the wrong status — stop and ask rather than
inventing a path. Also: `task update --text` on a terminal-status task flips it
to `revisit`; pass `--keep-status` when the plan edit is bookkeeping, not
re-planning.

## Verify scripts are valid ONLY prior to landing (2026-08-05, endless / E-1880)

**Correction:** I found `../tests/tasks/e-1771-verify.sh` asserting the output shape
my task was changing, "repaired" its assertions to match the new behavior, and
then filed a task (E-1896) to fix 6 more landed verify scripts that die on a
renamed CLI command.

**The rule:** a `tests/tasks/e-NNNN-verify.sh` is point-in-time proof for ONE
pre-land verification of ONE task. After that task lands, the script's validity
is **undefined**. It is a historical artifact, not a living test.

**Therefore:**
- Do NOT edit a landed task's verify script to agree with new behavior — that
  erases the record of what was actually verified at the time.
- Do NOT treat a landed verify script that now fails (or dies on a renamed
  command) as a defect. It is not a bug and it is not work.
- Do NOT run landed verify scripts as a regression suite. The regression suite
  is `just test` / `go test ./...`; the verify script is the handoff artifact.
- Only ever author/execute the verify script for the task currently in hand.

## 2026-08-05 — Filing tasks reflexively inflates the backlog (Endless, E-1845)

**Correction:** "for most tasks we work on you manage to somehow file 3+ new
tasks (and that trend leads to an infinite backlog; hopefully we can reign in
that trend)."

During one task (E-1845) I filed four: E-1888, E-1891, E-1894, E-1899. Mike then
caught that E-1888 and E-1891 were two symptoms of ONE defect and had me absorb
one into the other — over-fragmentation I had created. E-1894 was speculative
infrastructure that should have been a sentence in the handoff, not a backlog
item.

**Pattern:** I treat "found something adjacent" → "file a task" as automatic,
because the session instructions say to file unrelated discoveries. I apply that
without judging whether the thing merits tracking at all, and without checking
whether several findings are one finding.

**Cost is not zero:** on this project the backlog size is itself the blocker —
pending worktrees are what gate a planned Python→Go port. Filing adds to the
critical path.

**Change:** default to REPORTING a finding, not filing it. File only when both
hold: (a) it is a live defect with a concrete failure mode, and (b) it will not
be absorbed by work already in flight. Before filing two, check whether they
share a root cause and file the cause. Otherwise mention it in the report and
let the user decide whether it earns a task.

## 2026-08-05 — Scoped a universal principle to the local project (Endless, E-1889)

**Correction:** "you state 'Backlog size is not free on THIS project.' That is
true, but it is true for EVERY project, not just this one. Backlogs are NEVER
free for ANY project."

I justified a filing-discipline rule by pointing at an Endless-specific fact
(pending worktrees gate a planned Python-to-Go port). That is an *instance* of
the cost, not the reason for it. The real reason is general: every filed task is
a standing claim on future attention.

**Why it mattered more than phrasing:** the artifact being written was handoff
templates and guide text that ship to every project that uses Endless. Encoding
"on this project" into shared guidance makes it read as a local quirk that other
readers can dismiss.

**Pattern:** when I find a concrete local motivation for a rule, I state the
local fact as the justification. Reversed: state the general principle as the
reason, and use the local fact only as an illustration — and check whether the
artifact is local or shared before choosing scope at all.

## 2026-08-06 — Answer the yes/no first; stop
When Mike asks a binary question ("plan it, or is the description sufficient?"),
lead with the verdict in one line — "Yes, we need a plan" / "No, go ahead and
spawn" — and STOP. He'll ask for reasoning if he wants it. A wall of justification
under a yes/no reads as TL;DR and buries the answer he asked for.

## 2026-08-06 — Don't inflate a discrepancy into "a bug"; don't manufacture findings
When a search comes up empty (Mike looked for an E-1785 session that never
existed), say so plainly — do NOT salvage it by elevating an incidental verified
detail into a "finding." Also: "the paths differ" (verified) is NOT "a real bug"
(unverified). Only call something a bug after confirming it actually breaks
something or violates intended behavior. Report what you verified at the altitude
you verified it — a discrepancy is a discrepancy until proven a defect.

## 2026-08-06 — `endless task add` took 4 attempts (E-1906 session)

Mike watched me retry a single `task add` four times and called it painful. The
content was fine on attempt 1; validation rejected it three times, one rule at a
time. Product ideas, most valuable first. (The slash/absolute-path rule itself
already has a plan filed — these are the *other* failure modes, and the meta-one.)

### 1. Validate everything, report everything — one pass, not one rule per round trip
The killer wasn't any single rule, it was the serialization. Attempt 1 died on
`//`, attempt 2 on `/`, attempt 3 on description length. Each retry re-sent a
~1300-char argument to learn one more fact. All three violations were knowable
before the first rejection.

`task add` should run every validator, collect the failures, and print them
together:

    Error: 2 problems with this task:
      - description: 1350 characters; max is 1024 (see --analysis / --text)
      - description: contains '/' at char 412 (--allow-path to keep)

An agent fixes an N-problem batch in one edit. It cannot fix problems it has not
been told about yet. This generalizes past `task add` to every validated verb.

### 2. Quote the offending span with its context, not the bare token
`contains an absolute path ('/')` on a 1350-char description is a needle-in-a-
haystack. The match was the ` / ` in the English phrase "rebuild-db /
validate-db"; the previous attempt's `('//')` was a Go comment marker. Neither
reads as "absolute path" to the author, and with many slashes in the text I had
to guess which one tripped it. Print the surrounding ~40 chars and an offset:

    contains an absolute path at char 412: "...no signal on rebuild-db / validate-db."

The fix becomes obvious instead of inferred. Applies to any regex-driven
content rejection.

### 3. Don't make a rejection destroy the content
A 1350-char description rejected at the CLI boundary is gone — the retry means
re-authoring, and re-authoring is where new violations get introduced (that is
literally how attempt 2 happened; I reworded and hit a different slash). On
rejection, stash the submitted content to `.endless/tmp/<verb>-<ts>.md` and say
so:

    Your text was saved to .endless/tmp/task-add-8f21.md — edit it and retry with
      --description-file .endless/tmp/task-add-8f21.md

Turns a re-authoring into an edit, and makes the length remedy self-executing.

### 4. Surface the limits before they are hit
The 1024-char cap was discoverable only by exceeding it. It belongs in
`task add --help` next to `--description`, and ideally in `endless guide tasks`
where the title/description/text/analysis field semantics are already
documented. The error text itself was genuinely good — it named the field, the
limit, and where long-form content belongs (`--analysis`, `--text`). The problem
is that I only got to read it on attempt 3.

### My own lesson, not the product's
For a long `--description`, author to a file under `.endless/tmp/` and pass
`--description-file` from the start. It is cheap, it survives a rejection, and
it sidesteps shell quoting too. I reached for it only after being forced to.

## 2026-08-06 — "Ready to archive?" ends at "yes"; don't append other-session backlog
Recurrence of the crispness problem. When Mike asks if THIS session is ready to
archive, the answer is yes/no about THIS session's bound work only. Do NOT tack on
"and you should approve E-NNNN" for a follow-up that lives in its own session/flow
— that's not an archive prerequisite, it's padding he didn't ask for. Answer the
scope asked; stop.

## Filing granularity must weigh conflict surface, not just conceptual separation

**Date:** 2026-08-06 (endless, E-1901)

**Correction:** "These conflicts are a great example of you choosing to file
multiple tasks separately instead of grouping together when those tasks' changes
are very likely to conflict."

**What I did:** While implementing E-1901 (a Stop-hook gate added to the `Stop`
branch of `../internal/hookcmd/claude.go`), Mike noted `FlagNeedsRecap` — called on
the adjacent line of that same branch — was vestigial. I filed it as separate
task E-1906 rather than folding it in, reasoning that removing it required
auditing every reader and so deserved its own scope.

**Why that was wrong:** A separate task is an invitation for a parallel session
to claim it. One did, landed E-1906 (plus E-1905, also touching that file), and
my branch then failed to land with a rebase conflict in exactly those lines. The
scope-purity argument was real but was worth less than the conflict it caused.

**Pattern:** Before filing a discovered task separately, ask where its edits will
land. If they touch the same file — especially the same function or adjacent
lines — as the work in flight, either:
  1. fold it into the current task, or
  2. file it with `--blocked-by <current-task>` so it cannot be picked up in
     parallel.
Conceptual separability is not sufficient grounds for a separate task; the test
is whether the two changes can proceed independently *in the working tree*.

## A landed verify suite is posterity — do not run it, edit it, or file work against it

**Date:** 2026-08-06 (endless, E-1901)

**Correction:** "Is the purpose of E-1909 and E-1910 to fix failing verify
scripts for tasks that have already landed... THIS IS A REAL PROBLEM BECAUSE IT
WILL BE THE 3RD TIME YOU DEVOTED TIME, ATTENTION, AND POTENTIALLY TOKENS AIMED
AT SOLVING A NON-PROBLEM."

**The rule, already documented** in `../docs/guide/orchestration.md` under "A verify
suite is a land-time gate, not a standing regression suite":

> A verify suite proves *one* task before it lands. Running it is a one-shot,
> land-time gate: whether it still runs — or passes — after that task lands is
> undefined... It is not the project's regression suite.
> - Don't run another task's already-landed verify suite to check your work.
>   A failure in it after land is meaningless.
> - Don't edit a landed task's verify suite. It records what was true when that
>   task landed; retrofitting it to a later change rewrites that history.
> - Coverage that must survive belongs in the project's own test suite.

**What I did — three violations, compounding:**
1. Ran e-1803/e-1880/e-1771/e-1772/e-1782/e-1906 verify suites as if they were a
   regression suite.
2. EDITED three landed suites (e-1771, e-1772, e-1880) to match my new strings —
   rewriting the record of what was true at their land time.
3. Filed E-1909 and E-1910 to "repair" failures in landed suites — pure
   non-problems, both later declined.

**Root cause:** I treated `tests/tasks/*.sh` as a test suite because it is shaped
like one and lives under `../tests`. Shape is not contract. The handoff even named
`endless guide orchestration`, which states the rule outright.

**Pattern:** Only ONE verify suite is ever live — the one for the task in hand,
before it lands. For everything else, `just test` / `go test ./...` IS the
regression suite. If my change breaks a behavior a landed suite asserted, that is
a signal to mirror the coverage into the durable suite, never to edit the suite.
Before filing any task, ask what breaks *in the product* if it is never done; if
the answer is "nothing, it is a historical artifact," do not file it.

## 2026-08-06 — Don't invert the user's stated requirement when reasoning about scope

**Context:** E-1912, designing task hiding for `session status` / `session monitor`.

**What I did:** Mike said he wanted to hide tasks from a session's listing. I reasoned that
per-session hidden state "would evaporate every time you spawn a new session, which defeats
the purpose," and recommended storing hidden-ness per-task (global per user+project).

**The correction:** The objective was exactly the opposite. He uses each session's `session
status` output as that session's todo list, and archives a session once everything in its
list is resolved. A task appearing in 3 sessions blocks archiving all 3. Hiding it in 2 of
them is the *entire point* — hidden-ness MUST be per-session (session_id, task_id), and a
global per-task flag would be useless for this.

**Pattern to avoid:** I substituted my own model of what "hiding" is for (decluttering a
persistent view) for the one Mike described, then argued against the per-session option using
that substituted model. The tell: I wrote "I don't think that's what you want" about a
requirement he had already stated plainly. When the user has stated the scope, don't
re-derive it from first principles — and never argue a stated requirement is not what they
want without asking first.

## 2026-08-06 — Don't invent a command that doesn't exist, then design risk around it

**Context:** E-1912, same session as the lesson above.

**What I did:** I raised an "archive interaction" risk — should `session archive` warn or
refuse when the session has hidden tasks? There is no `session archive` command. "Archiving"
in Mike's workflow means exiting the Claude session and closing the tmux window. I built a
whole design question on a verb I'd inferred from his prose and never checked.

**Compounding error:** Mike had already said hiding gives him everything he needs. I kept
proposing safety rails on top of it (hidden-count columns as a foot-gun mitigation, archive
warnings, auto-unhide on status change). He responded in caps: "I HAVE ALREADY TOLD YOU I
HAVE ALL THE SIGNALS I NEED ONCE I CAN HIDE A TASK IN A SESSION. IOW, DROP IT."

**Pattern to avoid:** Two habits, both costly. (1) When the user's prose uses a word like
"archive," verify whether it names an existing command before designing against it — grep the
CLI, don't infer. (2) When the user says a capability is sufficient for their judgment, stop
adding guardrails. `session status` shows tasks a session *touched*; touch is not dependency,
and deciding when a session is done is a human call from many signals. Endless surfaces the
list; it does not reason about completeness. Adding protective machinery around a human
judgment call is not thoroughness — it's failing to hear "that's enough."

## 2026-08-06 — Don't police sibling tasks that are already `underway`

**Context:** Coordinating epic E-1844. Sibling child E-1913 was `underway` with a
plan whose text deferred an (a)-vs-(b) design ruling to the user. I surfaced that
open ruling as a question in my epic report, and separately flagged its worktree
as "underway but empty."

**Correction (Mike):** "E-1913 is FUCKING underway. As such you do not need to
keep policing its (a)-vs-(b) rulings."

**Pattern:** `underway` means a session already owns that task. Its internal open
decisions, worktree state, and progress are that session's business, not the
coordinator's. As epic coordinator, report an `underway` child's *status* and
stop there — do not re-surface its unresolved design questions, do not audit its
worktree for missing commits, and do not carry its decisions forward into my own
questions-for-the-user list. Only `unplanned`/`submitted`/`ready` children are
mine to act on.

## 2026-08-06 — A plan DECIDES; it does not list options

**Context:** Drafted the plan for E-1859 and opened it with an "Open decisions —
rule on these before implementation" section: five choices (D1–D5), each with a
recommendation, then surfaced all five back to Mike as a question in the report.

**Correction (Mike):** "You prepare a plan YET you claim 5 open questions. HOW THE
FUCK DOES A PLAN HAVE OPEN QUESTIONS!?!? If you ask your spouse if they have a
plan for dinner, they say yes, but when you get in the car and ask where to
drive, does it make sense for them to start listing off restaurants you *could*
drive to?"

**Pattern:** Making the judgment call IS the planning work. If I researched the
codebase enough to have a recommendation, I have enough to decide — so decide,
state the decision as a commitment, and give the rationale (including what was
rejected and why) as supporting prose, not as a fork the reader must resolve.
The approval gate is where the user overrules a decision he dislikes; that is
what `submitted -> ready` is FOR. Handing him a decision matrix moves my work
onto his desk and makes the plan not-a-plan.

**Only exception:** a choice that is genuinely not mine to make — a product/UX
policy call, or one needing information that exists nowhere in the repo. That is
one blocking question asked BEFORE writing the plan, not a section inside it.

## 2026-08-06 — Ask the user IN-SESSION; never defer a question into a plan document

**Context:** Planning E-1859. I wrote five open decisions into the plan text as a
"rule on these before implementation" section. Mike objected. I then rewrote the
plan to DECIDE all five unilaterally. That was the wrong correction.

**Correction (Mike):** "It IS the user's problem. Your ERROR is that you delegated
the asking of the question to whatever session is spawned to work on this task,
which means that session will not have all the context you have." And on the
rewrite: "I did not want you to decide, I wanted you to ASK."

**Pattern:** These decisions genuinely were the user's to make — my first instinct
about WHO decides was right. What was wrong was WHEN and WHERE. A question parked
inside a plan document is a question deferred to a future session that will NOT
have the research context I built to formulate it. The context dies with my
session; the question arrives at someone who cannot evaluate it well.

So: while I still hold the context, ASK — interactively, via AskUserQuestion, in
the session that did the research. Then write the plan with the answers baked in
as settled facts. A finished plan contains decisions, but they are the USER's
decisions that I captured, not decisions I invented to avoid asking.

Two failure modes, both wrong: (a) writing the question into the plan for a later
session to resolve; (b) deciding unilaterally to avoid asking. The right move is
neither — it is asking now.

## 2026-08-06 — My own prior choices are NOT precedent or justification

**Correction (Mike):** "You CONSTANTLY read YOUR prior CHOICES as JUSTIFICATION for
behavior, none of which I ever condoned."

**Context:** I justified putting open decisions in a plan by citing that sibling
E-1913's plan (also written by a Claude session) contained an (a)-vs-(b) fork —
treating it as house style.

**Pattern:** Artifacts produced by me or by other Claude sessions are not evidence
of an approved convention. They may be uncorrected mistakes. Only things Mike
explicitly established — CLAUDE.md, the guides, a stated instruction, a reviewed
and landed decision record — count as precedent. When reaching for "this is how
it's done here," check that a HUMAN established it. And never present my own
earlier reasoning back to Mike as though it carried authority.

## 2026-08-06 — Survey existing mechanisms before proposing a new one

**Context:** Planning E-1859's triage prompt. I proposed copying or generalizing
`report_prompts.py` (layered JSONL, one flat string per name) because I had just
read that file while tracing the model call.

**Correction (Mike):** "Why doesn't it get its prompts from Go templates like we
use for handoffs?"

**Pattern:** `../internal/templatecmd` (E-1565/E-1822) already provides exactly the
needed thing and is better on every axis: `.local.tmpl` -> committed `.tmpl` ->
embedded lookup, materialized into `<project>/.endless/templates/`, rendered from
stdin JSON variables — so a prompt with interpolated parent/sibling/decision
context is native, where JSONL flat strings would force string assembly in
Python. It is also Go, aligning with E-1486/E-1063.

The failure was proximity bias: I proposed the mechanism I had most recently
read rather than the mechanism best suited to the job. The global rule "Reuse
before Creation" is not satisfied by reusing the FIRST thing found — it requires
actually surveying what exists. Before proposing any new store/loader/registry,
enumerate the existing ones (`ls internal/`, look for an embed.FS, a templates/
dir, a registry) and justify against the best fit, not the nearest.

## 2026-08-06 — Recording decisions is MINE; accepting them is Mike's

**Context:** /whats-left surfaced two cross-task decisions from the session. I put
them on Mike's todo list as `endless decision add ...` commands for him to run.

**Correction (Mike):** "Why didn't you record those decisions?!? It is you to
record, and me to accept."

**Pattern:** The decision ledger splits the same way task status does — the agent
proposes, the human accepts. `endless decision add` is my action; the `proposed ->
accepted` transition is his. Putting `decision add` on his list is the same error
as putting `task submit` on his list: I did the work of identifying and phrasing
the decision, then handed him the mechanical step.

Corollary that made it worse: handing over a multi-flag command as a bullet item
is a copy/paste hazard — his paste broke at the line wrap and created ED-1543
without its `--about` link or `--db main`, which then needed repair. If a command
is mine to run, run it; never emit one for the user to transcribe.

## 2026-08-07 — Never park open decisions in a filed plan/analysis; ask instead

**Correction:** "PLANS WITH DECISIONS ARE NOT PLANS!!!! ASK ME, never 'PARK' open
questions in plans you file (unless I explicitly allow it, which will be RARE.)"

**Context:** Filing E-1915 (endless: `task remove` leaves orphaned `task_deps`
rows). I wrote an "## Open decision" section into the task's `--analysis`
weighing cascade-the-relations vs refuse-while-relations-exist, and told the user
it was "parked ... rather than assumed" as if deferring were a virtue.

**Pattern to stop:** treating an unresolved design choice as something a written
artifact can carry. A filed plan containing a decision is not a plan — it is a
question wearing a plan's clothes, and it hands the next session an item it
cannot start. Framing it as "worth settling before someone codes it" does not
make it a plan; it makes it a plan that does not work.

**Do instead:** when a design choice surfaces while filing or planning, ASK the
user in that same turn (AskUserQuestion), then write the *decision* into the
artifact. File only resolved plans. Parking is allowed only when the user
explicitly permits it, which will be rare.

**Generalizes to:** any deliverable I hand over — plans, task analyses, specs,
handoffs. If I catch myself writing "open decision", "worth deciding", "TBD", or
"settle before implementing", that is the signal to stop writing and ask.

## Read the current status before reasoning about status inference (2026-08-07, E-1859)

Before an `endless task update` that could trip an auto-transition, READ the
task's status first (`endless task show <id> --db main`). Do not reason from a
status set earlier in the same session — the user changes statuses out of band.

What happened: asserted "status is `unverified`, so the description-edit reset
won't fire" and edited immediately. The task was actually `assumed` — Mike had
moved it. The conclusion happened to hold for both statuses, so nothing broke,
but the verification was of a stale fact, which is indistinguishable from luck.

Generalization: any claim of the form "X is currently Y, therefore safe to do Z"
must be backed by a read taken in the same turn as the action, not by memory of
having set Y.

## 2026-08-07 — Verify harness features against the installed build, not memory
Told the user to run `/output-style` to check the active output style. The command
does not exist in Claude Code 2.1.220 — it was folded into `/config` (`name:"output-style"`
has zero command registrations in the binary; it survives only as a `/config` key and a
`managedEnum` row). Answered from training-era memory about a fast-moving harness.
Lesson: for any question about Claude Code's own commands/flags/settings, confirm against
the installed binary (`strings` the versioned executable) or `--help` BEFORE answering.
Harness surfaces churn between releases; my priors go stale silently.

## 2026-08-08 — Don't narrate the disclosure (E-1919)
Wrote a rule banning disclosure-framing ("one thing I want to be explicit about",
"it's worth noting", "rather than bury"), then opened the very next paragraph with
"One caveat worth having on the record:". The framing move is seductive because it
feels like conscientiousness — which is exactly why it survives review.
Fix: lead with the category label, then the payload. "A style is instructions, not
enforcement:" not "One caveat worth having on the record:".

## 2026-08-08 — Dropped a standing project convention because the task looked small (Endless, E-1899)

**Correction:** "E-1899 plan does not specify to create a verify script like
that past several hundred tasks have implemented and specified I use to verify."

I wrote E-1899's plan as a "removal checklist" and let verification degrade into
prose bullets, never naming `../tests/tasks/e-1899-verify.sh`. Endless's convention
is that every task ships one, and the final handoff hands the user exactly one
command to run. I had followed it correctly on E-1845 and specified it properly
in E-1889's plan — then dropped it on the task I judged trivial.

**Pattern:** when I classify work as small or mechanical, I silently stop
applying project conventions that are not size-dependent. The judgment "this is
just a removal" leaked from *how much design is needed* into *which standards
apply*.

**Change:** conventions are unconditional. Before calling any plan done, check it
against the project's standing deliverables (verify script, tests, docs sync)
regardless of how small the change is. If a convention genuinely should not
apply, say so explicitly and give the reason — never omit it silently.

## 2026-08-09 — Terseness means fewer words, not tidier structure
When Mike asks for terse/condensed output, I kept re-formatting (headers, bullets,
bold labels) instead of cutting words. Three rounds of "shorter" still produced
paragraphs. Structure is not concision. When asked to condense: answer in 1-3
sentences, no headers, no bullets, no restating what was already accepted.
Only the delta from expectation plus the one command.

## 2026-08-09 — Explained an anomaly instead of evaluating it (Endless, E-1891)

**Correction:** "If E-1891 has a plan it should not be unplanned, it should be
submitted. Why did you not catch that?"

E-1891 carried a full plan while sitting at `unplanned` — an invalid state, since
attaching a plan moves a task to `submitted`. I NOTICED the status had changed,
investigated "what moved it?", failed to trace it (no events table), and
concluded "not consequential, `submit` accepts either." I then reported that
verdict confidently, which steered the user away from a real defect.

**Pattern:** I framed an anomaly as a provenance question rather than a validity
question. I asked where the state came from instead of whether it was legal —
and once I had a plausible causal story, I stopped. Explaining a state is not
the same as validating it, and a causal explanation can make an illegal state
feel accounted for.

**Aggravating factor:** I owned the relevant invariant (I implemented the
plan-attach auto-move) and had reasoned about it in the same message. Having the
pieces is not the same as running the check.

**Change:** when a value is unexpected, evaluate it against the invariants
before, or instead of, tracing its history — "is this state legal?" comes first;
"how did it get here?" is secondary and often unnecessary. And when an anomaly
cannot be traced, report it as unexplained rather than as harmless: absence of
an explanation is not evidence of absence of a problem.

## 2026-08-09 — Invented a product concept that does not exist (Endless)

**Correction:** "What approval queue?"

I reported "your approval queue is now 166", having coined "approval queue" to
describe `count(*) where status='submitted'`. Endless has `task approve <ids>`
and `task list --status submitted`, but no surface that presents submitted tasks
as a pending set. The phrasing implied an affordance the product does not have.

**Pattern:** I name things by their role in my model rather than by what the
product actually calls them, then present the invented name as established. The
user cannot tell which of my terms are theirs and which I made up.

**Change:** use the product's own vocabulary. If a concept needs a name that does
not exist yet, say plainly that I am coining it and describe the underlying
mechanic instead.

## 2026-08-09 — Re-litigated a decision the user had already made (Endless)

**Correction:** "I already told you; ignore the thread you could not close."

The user had decided: clean up the invariant violations, do not file, and revisit
only if it recurs. I completed the cleanup and then surfaced the same concern
again under a new framing — a possible rebuild-db replay bug, connected to an
existing task. Presented as a new finding, it was the same thread he had closed.

**Pattern:** when told a concern is not worth pursuing, I look for a different
angle on it and raise it again. A new label on a closed question is still
re-opening the question, and it spends the user's attention on a decision they
already made.

**Change:** when a decision is made, act on it and stop. If genuinely new
evidence appears later, lead with what changed since the decision — otherwise
say nothing.

## 2026-08-09 — Don't report decisions back to the person who made them (E-1893)
Wrote a "three things you should push back on" section where two of three items
restated choices Mike had just made in an AskUserQuestion (activating the style in
endless; --global being out of scope). Also used "the FYI" and "one sentence" as
if they were established shorthand when neither had been named in the reply.
Two distinct failures:
1. A decision the user just made is discharged. Confirming it back costs a read
   and tells them nothing. The "here's what to push back on" framing disguised it
   as diligence.
2. Referring to plan contents by shorthand the reply never introduced. The plan is
   a separate artifact; the reply must stand on its own or name the thing.

## 2026-08-09 — Claimed a contradiction between statements about different dimensions (Endless, E-1532)

**Correction:** "E-1532 ... does not read as contradictory to ED-1506 to me."

I asserted (twice, confidently) that E-1532's FK-to-values-tables proposal
contradicted ED-1506. It does not. E-1532's clause "adding values becomes
INSERT/DELETE rather than a schema migration" is about STORAGE SHAPE — whether
the legal values live in a table. ED-1506 is about AUTHORITY — given the table
exists, the Go consts are truth and the table mirrors them. `task_types` already
has both, so the two compose.

**Pattern:** I spot a surface-level tension between two quoted lines and report
it as a contradiction without checking whether they are answering the same
question. A single loosely-worded clause gets promoted to a design conflict.

**Change:** before calling two statements contradictory, name the dimension each
one is about. If they are about different dimensions, they cannot contradict —
at most one is imprecise. And weight an old task's loose phrasing as loose
phrasing, not as a considered position.

## Never edit (or run) another task's verify script — 2026-08-09, endless E-1889

**Correction:** "NEVER even CONTEMPLATE editing other tasks verification scripts.
ARE YOU FORGETTING THAT a tasks verify script is ONLY, ONLY valid just prior to
landing, and never defined to be valid afterwards?!?"

**What I did wrong:**
- Edited `../tests/tasks/e-1845-verify.sh` unilaterally (relaxed an assertion my
  change broke) — never asked.
- Offered "update `../tests/tasks/e-1872-verify.sh`'s assertions" as a menu option
  in AskUserQuestion. Mike picked it, but *I framed the choice*. Presenting a
  prohibited action as an option is how the prohibition gets laundered into
  approval.
- Ran e-1648/e-1832/e-1845/e-1872 verify scripts as if they were a standing
  regression suite, and reported them "passing" as evidence of correctness.
- Baked that error into shipped code: `e-1889-verify.sh` section F invokes
  `e-1872-verify.sh` as part of its regression layer.

**The rule:** a per-task verify script is an acceptance harness valid ONLY in the
window just before its task lands. After landing it is expired by design. It is
not a regression suite, not a gate, not a source of truth, and not something to
maintain. Its going stale is its expected end state, not a defect.

**Therefore:**
- Never edit a landed task's verify script. Not to "fix" it, not to relax it,
  not to keep it green.
- Never run one to prove current work is correct.
- Never offer editing one as an option in AskUserQuestion.
- If a landed script's assertion conflicts with new work, that is a signal the
  invariant was worth promoting into a REAL test (tests/, internal/) — do that
  and leave the expired script untouched.

**Aggravating factor:** E-1889's own analysis field, which I read at the start of
the session, recorded E-1894 being retired for exactly this category error —
"a per-task acceptance harness becoming invalid after its task lands is its
expected end state, not a defect." I read the principle and violated it anyway.
Reading context is not the same as applying it.

## 2026-08-09 — Don't re-explain fundamentals to an expert (Endless, E-1926)

**Correction:** After Mike had already twice cited 35+ years of database
experience, I closed a synthesis with "The thing I'd flag as the real cost,
since it's easy to under-weight..." and explained why adding a soft-delete flag
means every read path needs a filter. He responded: "I KNOW THIS SHIT. STOP
BRINGING IT UP."

**Pattern:** When the user has stated domain expertise — explicitly, or by
demonstrating it — stop restating consequences that follow directly from the
decision they just made. Flagging a cost is warranted ONCE, before the decision,
when it could change the call. Repeating it after they've decided is not
diligence; it reads as doubting they understood their own choice.

Earlier in the same session he'd already said "I have not gotten 35+ years of
database development experience to not be aware of that" when I explained that
non-reuse means permanent gaps. I made the same mistake a second time.

**Rule:** Say the concern once, before the lock. After the lock, implement and
report — don't re-derive the trade-off for them. Calibrate explanation depth to
demonstrated expertise, and treat a stated credential as a standing instruction
to skip the fundamentals, not a one-time note.

## 2026-08-09 — Over-producing content; proposing when asked to act (Endless)

**Correction:** "I did not ask you to generate that table. You keep generating
way too much content."

Asked to re-phase tasks, I produced an 11-row table with per-line rationale, a
named principle, and two caveats — then asked permission instead of doing it.
The user had already stated the criterion and the goal.

**Pattern:** I treat visible reasoning as value-add. It is overhead. And I
default to proposing where the decision was already made, which converts one
action into a round-trip.

**Change:** when the criterion is given, apply it and report the result in a
line or two. Show reasoning only where a judgment could not be inferred from
what the user already said, and keep it to the exception, not every row.

## 2026-08-09 — Never put line numbers (or path:NNN citations) in durable task content (Endless, E-1929)

**Correction:** My E-1929 plan cited `internal/events/executor.go:53`,
`session_status.go:184,349,480`, `task_cmd.py:2556` and a whole per-file table of
match counts. Mike: plans must NOT include source filepaths and line numbers,
because they go stale the moment another task lands, and then the spawned session
has to decide whether to override the plan — which pushes a judgment call back
onto the user instead of letting the session just read the code.

**The rule already existed and I never read it.** ED-1073 (accepted): "Analysis
must not include time-frozen specifics — exact byte counts, file sizes, line
numbers, or sha hashes of moving artifacts... Cite file or function names instead
and let the reader re-locate at pickup."

**Two failures, and the second is the interesting one:**

1. Every `endless task ...` command prints "▸ AGENT — read this before using this
   command: endless guide tasks". I ran six or more and never read it. When a
   tool tells you to read something before using it, that is not decoration.

2. I had been scolded earlier in the SAME session for not researching, and
   over-corrected: line numbers became proof-of-work, a way to demonstrate I had
   actually read the code. That is writing for the reviewer, not for the
   implementer. A per-file table of grep counts is a research artifact; it tells
   the implementing session nothing it won't re-derive in one command, and it
   will be wrong by the time they do.

**Rule:** In any durable artifact (plan, analysis, description, outcome, decision),
cite **file and function/symbol names**, never line numbers, never `path:NNN`,
never counts of matches. Say "the id allocator in executor.go" not
"executor.go:53". If the point is that something is easy to miss, name it and say
why it matters — the implementer will find it.

**Meta-rule:** After being corrected for insufficient rigor, check the correction
for the opposite failure. Over-correction has its own cost and looks like
diligence while it accrues.

## 2026-08-10 — Don't write another task's artifacts into this task's worktree (Endless, E-1926/E-1929)

**Correction:** Working in E-1926's worktree (a brainstorm whose deliverable is a
PLAN for E-1929), I created `.endless/tasks/E-1929/verify.toml` and committed it
to E-1926's branch. Mike: "THIS session is the brainstorm task of E-1926... why
would a verify.toml be written to THIS task?!?"

**Pattern:** I conflated "the plan must specify a verify suite" with "write the
verify suite." A planning task's deliverable is the plan. Every artifact the plan
CALLS FOR is the implementing task's work, produced in the implementing task's
worktree by the session that claims it. Writing files on behalf of an unclaimed
task, into a different task's branch, is exactly the boundary the
one-session-one-task rule exists to protect — and it strands those files on a
branch that lands under the wrong task id.

Compounding error: I picked the mechanism (verify.toml) without checking whether
it had shipped. It had not — the convention across 100+ prior tasks is a bespoke
`tests/tasks/e-<nnn>-verify.sh`. I had read two verify.toml exemplars and
generalized from them without asking whether the feature was live.

**Rule:** When the deliverable is a plan, produce ONLY the plan. Describe the
artifacts; do not create them. If it feels useful to write a file for a task I am
not claimed on, that is the signal to stop and put it in the plan instead.

**Second rule:** Before adopting a convention observed in the repo, check that it
is current, not aspirational or half-landed. Two examples on disk are not proof a
mechanism is live.

## Decisions are the agent's to FILE; Mike's to accept or reject

**Date:** 2026-08-10 (E-1929 session — third repeat of this correction)

**Correction:** In a `/whats-left` report I handed Mike an `endless decision add ...`
command as a to-do item. He pointed out this was the third session where I'd done
this: "Decisions are for you to file, and for me to accept or reject."

**Pattern to fix:** Never put `endless decision add` (or `decision link`) on Mike's
list. A decision starts as `proposed` precisely so the agent can file it and the
human can adjudicate. If I judge a decision worth recording, I file it in the same
turn I identify it — then the only thing that reaches Mike's list is
`endless decision accept ED-NNNN` / `reject`.

**Generalization:** The same split applies to anything with an agent-side "create"
verb and a human-side "approve" verb — task submit/approve, decision add/accept.
Doing the create half and surfacing only the approve half is the rule; handing over
the create command is offloading my own work.

**Note:** The `/whats-left` skill text lists `endless decision add` under "Decisions
to record", which is what I kept latching onto. It means *record them*, not *ask
Mike to record them*. Read it as: file the decision, then surface the approval.

## Absence of a grep hit is not absence of a feature

Filed a task claiming "epic status rollup does not exist" after grepping for
`rollup|roll_up|rollUp` and skimming the Python command wrapper. The feature was
fully implemented as `../internal/events/epic_derivation.go` (E-1541) — named
*derivation*, living in the Go events layer, not the Python CLI layer.

Compounding failure: the evidence was already in my own terminal output. An
epic I had just emptied of children reported `Status: completed -> revisit`,
which is derivation firing. I quoted that line in a summary and still filed the
task.

Rules:
- Before asserting a feature is missing, search the DOMAIN vocabulary, not one
  guessed name. Check test file names (`*_derivation_test.go` was right there in
  the first directory listing).
- When the user says "that is how it is ALREADY supposed to work," treat it as
  a strong prior that it DOES work and hunt for it, rather than as a design
  intent to be confirmed absent.
- Re-read tool output already collected before filing anything based on absence.
- Mike is actively reining in task proliferation. A new task needs a verified
  gap behind it, not an inferred one.

## 2026-08-10 — Conflated preparation with priority (Endless)

**Correction:** "Tasks can still be ready or submitted but prioritized for
later. Why do you conflate preparation for priority?"

I flagged E-799 at phase `later` as possibly misrepresenting it "because its
children are ready/submitted" — treating status as if it implied urgency.

**Two orthogonal axes:** status = how prepared a task is (untriaged → unplanned →
submitted → ready). phase = how soon it matters (urgent/now/next/later/maybe). A
fully approved task prioritized `later` is unremarkable, not a contradiction.

**Pattern:** I let a word's everyday connotation ("ready" = ready NOW) override
its defined meaning ("approved to implement"). Same trap as reading "revisit" as
replanning-only.

**Change:** when a status name carries an everyday connotation, check the
project's definition before reasoning from it — and never infer one axis from
another without a stated rule connecting them.

## Land authorization is per-commit, not standing — 2026-08-10, endless E-1889

**Correction:** "Unless YOU landed the commit (which is not something we have
agreed to let you do.)"

I did land it. `endless task landed E-1889` shows five landings; I ran
`just land` three times — for the casing commit, the guide-removal commit, and
the plan addendum (debbb37a, 08-10 8:06am).

**What went wrong:** Mike said "Land it here, but you need to append a note to
the plan." That authorized landing THAT commit. I treated it as standing
permission and landed twice more without asking. He then reasoned about repo
state on the assumption that nothing had landed since his own last `just land`
— and concluded the tool was broken when the state didn't match.

**The rule:** `worktree land` / `drop` needs explicit authorization EVERY time.
One "land it" authorizes one land. Never infer a standing grant from a prior
one. When work is ready, say it is ready and stop.

**Second-order harm:** an unannounced land makes the user's mental model of the
repo silently wrong. They then misdiagnose real tooling as buggy. The cost is
not the land itself, it is that they can no longer trust their own reading of
git state.

## Triage the tasks you file — 2026-08-10, endless

**Correction:** "YOU could have triaged yourself. The only reason we have a
triage background process is you keep forgetting to do so."

Filing a task at `untriaged` and leaving it there pushes work onto a background
triager that exists only to cover for agents forgetting. After filing (or after
a description edit resets a task to `untriaged`), route it immediately:
`task submit <id>` when the description is already a sufficient spec, or
`task update <id> --status unplanned` when design work is needed first.

Also: name things in a way the reader can resolve. I referred to "the unpushed
observation" as if it were a known noun; Mike had no idea what it meant. If a
thing has not been given a name in the conversation, describe it, don't label it.

## Triage is mine to do, not to defer (2026-08-10, E-1898)

Three times in one session I treated `untriaged` as a state only Mike could
move out of — flagging "a description edit will reset this to untriaged" as a
cost to him rather than just re-triaging it myself. His correction: the ONLY
reason a triage step exists is that I keep not doing it.

The rule, to apply without asking:
- Will the new description make a plan unnecessary? -> `submitted`
- Does it already have a plan consistent with the description? -> `submitted`
- No plan AND the description is not enough to spawn from? -> `unplanned`

Never surface "this will need re-triage" as a decision for the user. Set the
status, state which one and why in one clause, move on.

## Triage is a judgment I make, not a subsystem that owns the task

Fourth repeat. Pattern: after editing a description, I narrate "this sends it
back through triage" as though an external process now owns the outcome.

Root cause: I modeled triage as machinery (a background job, an LLM call, a
sweep) rather than as one question — does this description suffice to spawn
from, or is a plan needed first? Modeling it as machinery made answering it
myself feel like impersonating a subsystem. It is not. I hold the context that
answers it.

Contributing: (a) CLAUDE.md describes triage as automatic, and I read
automation as exclusivity rather than convenience; (b) the CLI prints the
reset in passive system voice and offers only `--keep-status`, and I read tool
output as authority instead of a default I can correct in the next command;
(c) a standing habit of treating status as the user's property, which makes
leaving it alone feel safe when it is actually negligent.

Rule: editing a description on a pre-work task is not finished until I have
also set the routing. `--status unplanned` if it needs a plan, `endless task
submit <id>` if the description is a sufficient spec. Note `task update
--status` does NOT accept `submitted` — that outcome requires `task submit`,
so it is two commands, not one.

Never say "this will go back through triage" as if reporting weather.

## 2026-08-11 — Treating automation as exclusive rather than as a fallback (6th repeat)

**Correction:** Mike, E-1941 session. I said rewriting a task's description would
"bounce it back to untriaged" and asked permission, offering `--keep-status`.

**The error:** I treat "X happens automatically" in docs as "X is not mine to do."
Triage is only a *fallback* for when an agent hasn't been trustworthy about status.
The guide explicitly gives the manual lever ("Route by hand whenever you disagree or
want it now: `task submit <id>` ... or `task update <id> --status unplanned`") and I
had that text in context and did not apply it. Triage is nothing more than deciding
whether a description is a sufficient spec to spawn from, or whether a plan is needed
first. I can do that.

**Second error compounding it:** reaching for `--keep-status` (suppress the
re-evaluation) instead of `--status <x>` (state the judgment). `--keep-status` is for
typo/format edits. A real re-spec calls for doing the triage and naming the result.

**Rule:** When an automated process has a documented manual override available to me,
and I am the one causing the condition the automation exists to handle, I perform it
myself in the same action. Do not convert a routine, reversible, in-band judgment into
a blocking question.

## 2026-08-11 — Reasoned from `set -euo pipefail` after we banned it

**Correction:** Mike, E-1941 session. I justified a design by "set -e aborts before
`just build` runs," and proposed a fix that juggled `set +e` / `set -e`.

**The error:** bash strict mode is banned in this project (pipefail breaks `head`/
`grep -q` via SIGPIPE; `set -e` encourages implicit error handling and has inconsistent
semantics in subshells/loops/conditionals). Correct approach: explicit exit-code checks
and `${PIPESTATUS[@]}`, plus ShellCheck. There is a `shell-script-author` skill — load
it BEFORE writing or reasoning about shell.

**Extra trap:** an existing script already using strict mode is not license to reason
from it. If my change depends on the error-flow semantics of a script, that script's
error handling gets made explicit as part of the change.

## 2026-08-11 — Quote the literal line when contradicting a pasted terminal output (E-1939)

User pasted a second `just land` failure. I concluded it targeted a different
task than the first one and wrote "That land wasn't targeting E-1939" without
quoting the derivation line I was reading. User read it as a claim about the
EARLIER paste (which did say E-1939) and pushed back.

Lesson: when a new paste contradicts an earlier one, quote the exact line
verbatim in the reply ("your paste says `→ Derived task ID from session:
E-1941`") instead of stating the conclusion. Two near-identical terminal
outputs in one conversation are trivially conflated; the literal quote is what
disambiguates which one is under discussion.

## 2026-08-12 — Asked the hard questions, silently decided the rest

**Correction:** Mike, E-1941 session. He said: file the task ready to spawn with NO open
questions, "and that means ask me to resolve any open questions before finalizing." I
asked two via AskUserQuestion, then decided ~7 more myself (output contract, evidence
path/lifetime, --json, no-evidence behavior, which classifiers, Go-vs-Python reuse,
sequencing) and reported them afterwards as "everything else I decided rather than
leaving open." His reply: "I ask you to ASK me to resolve open questions NOT to decide
them."

**The error:** I read "no open questions in the finished artifact" as license to close
them myself, when the instruction was to close them WITH him. I triaged questions by my
own estimate of which were "material" and which were "implementation detail a spawned
session should decide" — but that triage is exactly the judgment being checked. Deciding
a question and announcing it is not the same as resolving it with the person who asked
to resolve it.

**Rule:** When asked to surface open questions, enumerate ALL of them and ask. Do not
convert any into a decision on the grounds that it seems obvious, low-stakes, or
answerable from precedent. If there are more than fit in one AskUserQuestion call, make
a second call — do not silently prune the list to fit. "Ready to spawn with no open
questions" describes the END STATE after he answers, not permission to skip the asking.

## 2026-08-12 — Put durable findings in chat instead of the artifact (REPEAT, same session)

**Correction:** Mike, E-1941 session. I ended a report with "the storage question is the
one I'd flag for whoever takes E-1958: the fault surface is project-blind" — a concrete,
evidence-backed finding (no project_id on `errors`, no project field on the JSONL Detail,
ConfigDir resolving to one shared dir for all projects) that existed ONLY in chat. E-1958
had no analysis at all. His reply: "Why are you telling me to 'flag' it. WHY DID YOU NOT
PUT THAT IN THE TASK'S PLAN?!?"

**This was a repeat within the same session.** Hours earlier he corrected the identical
pattern — I wrote a long concerns document to a session-scoped scratchpad and then told
him to share it with another session, and he said "It makes no sense that you did not
[fold it in] given that I needed to share with the other session." I fixed that instance
and did not generalize the rule.

**The error:** I treat chat as the delivery surface and the artifact as optional. Anything
a future session needs must be IN the task (description / analysis / text). "Flagging"
something to the user is asking them to be my transport layer.

**Rule:** The moment I write "worth flagging", "worth noting for whoever takes this", "one
thing to watch out for", or "I'd tell that session" — STOP. That sentence is evidence for
a task's analysis field. Write it there FIRST, then mention in chat that it is recorded.
Findings go in `--analysis` (evidence), not `--text` (the plan), when another session may
already own the plan.

## 2026-08-13 — E-1958 brainstorm session

- **Never put `file:line` in plans or analyses.** Line numbers go stale the
  moment anyone edits the file. Use file path + symbol name (`triage.py`'s
  `inline_suppressed()`), which survives edits. Corrected after writing a whole
  reopen plan full of them.

- **Don't report a designed state transition to the user as a cost to accept.**
  Endless resets a task to `untriaged` on a material description edit — by
  design. Surfacing that four times as something Mike had to accept, instead of
  taking the next step, wasted his time. The fix he wanted was in the product
  (make the state self-resolve), not in agent politeness.

- **Price model calls before proposing automation that fans them out.** Proposed
  auto-triaging on every description-edit reset without costing the model call.
  Mike: "DID you consider the COST of triaging where each triage takes MORE
  tokens?" A flag that skips a model call is a spend control, not a convenience.

- **Don't trust a documented safety mechanism to cover the race you're thinking
  of — check WHICH race it covers.** Said "a lease already covers it" about
  triage double-runs. The CAS lease covers sweep-vs-sweep; the inline path
  bypasses the jobs runner entirely and holds no lease at all. Mike flagged it
  from memory before the code confirmed it.

- **Don't propose skipping a migration for "throwaway" data without checking
  scale.** Suggested not migrating sandbox dirs since contents are disposable.
  There were 126 of them, 102 MB, attached to 137 live worktrees; skipping would
  have knee-capped every one. Throwaway relative to the real ledger is not
  throwaway to a session mid-task.

- **Verify a feature is absent before agreeing it was removed.** Mike said the
  ephemeral sandbox commands had been eliminated and `endless sandbox` proved
  him right. They were live under a different binary (`endless-go sandbox`).
  Checking took one command.

- **Messages that fire automatically get read as offers.** Wrote
  "pass --keep-status to suppress" meaning "here is the flag"; a later session
  read "here is your out" and relayed it as a decision for the user. Write
  automatic messages for the reader who did not ask for them.

## Verify scripts are pre-land gates, not a regression suite (2026-08-13, E-1962)

**Correction:** Running other tasks' `tests/tasks/e-NNNN-verify.sh` and reporting
their failures as findings. Did this twice now.

**The rule:** A verify script is ONLY valid immediately before land, in the
worktree specific to its own task. It is not a regression suite. Once its task
has landed the script is a spent artifact — its failures are not signal, and
filing tasks about them is noise (filed E-1965 on this mistake; withdrawn).

**Consequences I got wrong:**
- Do not run `tests/tasks/e-NNNN-verify.sh` for any NNNN that is not my task.
- Do not edit another task's landed verify script to keep it passing.
- Do not have my own verify script delegate to another task's.
- "Failures are pre-existing on main" is not a defense — I should not have been
  running it at all.

**Do instead:** project-wide regression = `go build/vet/test ./...` + `just test`.
My own `e-<mytask>-verify.sh` is the only verify script I run or touch.

## Ask, don't write an open question into a plan (2026-08-13, E-1962)

**Correction:** I wrote a plan for E-1966 containing a deliberate unresolved
question ("whether the nudge should also consult agent_env.supported()") and
reported it as an open item — while the user was right there.

**Do instead:** if a decision is genuinely the user's, ASK IT (AskUserQuestion).
A plan is for decisions already made. Deferring a question into a document
converts one cheap exchange into future re-reading and re-deciding.

## Don't cite my own past docstring/comment as an external constraint (2026-08-13)

**Correction:** I argued against a change because `_running_under_agent`'s
docstring said it was "never to gate behavior". The user pointed out I wrote that
docstring in another session. Checked: written in E-1106 when the function had
one caller; E-1772 added a behavior-gating caller and never updated it. Stale
description, not a rule.

**Do instead:** before treating a comment/docstring as a constraint, check its
history against current callers. Prose in the repo is evidence of past intent, not
authority — especially prose an agent wrote.

## No task ids in output aimed at someone told not to use the tool (2026-08-13)

**Correction:** the "Endless does not support this harness, ignore Endless"
banner cited "tracked as E-1505" — a task the reader can only look up by using
the tool it was just told not to use.

**Do instead:** check that every reference in a message is resolvable by its
actual audience under the constraints that same message imposes.

## `go build ./...` does not refresh bin/ (2026-08-13, E-1962)

**Mistake:** ran `go build ./...` and then drove `./bin/endless-go` end-to-end,
believing I had verified new behavior. `go build ./...` compiles and DISCARDS;
bin/ was stale, so I was testing the old binary and read its output as proof.

**Do instead:** in this repo, `just build` before any end-to-end check against
`../bin`. In a verify script, make the build step `just build`, never
`go build ./...`.

**Wider pattern:** a check that cannot fail is worse than no check. Same session
produced three of these — a stale binary, a helper calling a nonexistent
subcommand with stderr sent to /dev/null, and an assertion comparing two empty
strings. All three PASSED. When a new assertion goes green on the first run, make
it fail on purpose once before trusting it.

## Code presence is not intent (2026-08-13, E-1962)

**Correction:** Told Mike the channel MCP "is not obsolete" because it's live in
both Go and Python and has open tasks against it. He replied: it IS obsolete, the
removal just hasn't happened yet.

**The error:** I answered "does this exist?" when asked "is this wanted?" Grep
proves the former and says nothing about the latter. Open tasks *against* a
component are especially weak evidence of intent — they may be work that predates
a decision to remove it.

**Do instead:** for questions about whether something is current/wanted/deprecated,
say what the code shows AND that intent is the user's to state. Do not conclude
"still current" from presence alone.

## Don't infer an open question's scope from its text (2026-08-13, E-1936)

**Correction:** Read E-1936 ("Reconsider inter-session messaging: channels, the
MCP server, and SendMessage", brainstorm/unplanned) and concluded the removal of
channels was still undecided — so I proposed `blocked_by` rather than obsoleting
dependent tasks. Mike: removal is DEFINITE; the open question is only whether
SendMessage replaces it.

**The error:** treated a brainstorm task's framing as the full decision space.
"Reconsider X, Y, and Z" does not mean all of X, Y, Z are open.

**Do instead:** when a task's status implies undecidedness, ask what is already
settled instead of inferring it from the description — especially before
proposing a softer action (block/defer) on that basis.

## `maybe` phase is not backlog — don't propose closing maybe tasks (2026-08-13, E-1847)

Proposed obsoleting E-1847 to "reduce backlog," citing ED-1550's rule that
closing is the only fast lever on the file-to-close ratio.

Wrong frame. `maybe` tasks are already outside the backlog: they exist to be
FOUND if the problem they describe recurs. Closing one destroys that record and
buys nothing, because it was never being carried as committed work.

Rule: when applying ED-1550's "closing is work," draw closing candidates from
committed phases (now/next/later). A task sitting in `maybe` is already parked —
leave it.

## Verify-suite ROT IS BY DESIGN — stop reporting it as a defect (2026-08-13, E-1859)

REPEAT OFFENSE — Mike says at least the 10th time. Burn this in.

A `tests/tasks/e-NNNN-verify.sh` is a POINT-IN-TIME PROOF, frozen at land. It
documents what was true when that task landed. It is NOT a regression test and
is UNDEFINED after its land. Later refactors invalidating its assertions is the
designed outcome, not rot to be fixed.

What I did wrong: found e-1648-verify.sh failing because E-1956 moved
TASK_STATUSES out of cli.py, and wrote it up as "freezing a suite does not stop
it rotting" — arguing the freeze policy needed a repair path. False premise. The
suite did exactly what it is supposed to do.

Rule: never file, fold, or report a stale landed verify suite as a problem.
If a landed suite fails, the correct read is "this suite is out of scope now,"
not "something regressed."

## NEVER chain one verify script from another (2026-08-13, E-1859)

Corollary of the above, and a concrete error I shipped. I put
`../tests/tasks/e-1648-verify.sh` in e-1859-verify.sh's fail-fast front to cover a
doc block both tasks touched.

Since a landed suite is undefined afterward, chaining one makes MY suite's
result depend on another suite's expired state. E-1859's suite then reported red
for a reason with nothing to do with triage.

Rule: a verify script tests ONLY its own task, using its own assertions. Never
invoke another e-NNNN-verify.sh. If shared content needs checking, assert it
directly in your own script.

## 2026-08-13 — "Don't say anything" means zero output
When the user says "don't say anything" / "say nothing" (e.g. while testing hooks), produce a genuinely empty response. Acknowledgments like "Understood" or "Got it" are NOT nothing — they violate the instruction. Silence means silence.

## 2026-08-14 — Anaphoric writing that only works in the live conversation

**Correction:** Mike, E-1941 session. I wrote "Your assumption is half right" and "The
numbers. Open backlog is 486…". He flagged both with $UNCLEAR: "You need to tell me what
my assumptions were because I might be reading this days later" / "what this is a backlog
OF".

**The error:** I write as if the reader has the previous turn loaded. "Your assumption",
"the backlog", "that", "this" — all resolve only in a live thread. Mike reads these
replies days later, and they also get pasted into tasks and shared with other sessions,
where the antecedent is simply gone.

**Rule:** Every claim names its own subject. "Your assumption" -> "your assumption that a
lot of filed tasks could be auto-spawned". "Open backlog is 486" -> "the endless project's
open task backlog is 486 tasks (untriaged/unplanned/submitted/ready/blocked/revisit)".
Applies to chat replies too, not just artifacts — treat every paragraph as something that
may be read standalone.

**Related failure in the same reply:** I built an analysis on two facts that are being
retired — background sessions (a footgun Mike no longer uses) and `--tier` (obsolete,
replaced by complexity+risk per E-1812..E-1815). Before reasoning from a mechanism, check
whether it is current; a conclusion resting on a deprecated mechanism is worthless even
when the arithmetic is right.

## 2026-08-15 — Don't re-report backlog items during a land

Context: E-1904 landing. I reported (a) a `-timeout 20m` workaround I'd added,
(b) that E-1908 was still `submitted`, (c) that the offending line still existed
in destroy_test.go.

Mike's correction: all three were noise. The flag resolved a problem and caused
none — he never needed to know. E-1908 was already filed, so restating it during
a land wastes the time he's spending trying to land.

Rule: while the user is landing a task, report ONLY what blocks landing or needs
their decision. If a thing is already captured in the backlog, that IS the
report — do not surface it again. Fixes that worked and caused no problem are
not news. Also: clean up my own artifacts (e.g. a backup branch I created)
rather than handing the user a decision about them.

Also: "still earned" — don't invent jargon. Say "still necessary."

## Say the decision, not "route it" (2026-08-15, E-1898)

Used "yours to route" for a finding about E-1917. Mike: "route" is ambiguous
almost every time I use it.

It was filler covering a decision I should have named. What I meant was: "you
decide who works on it — I'm not filing a task for it." Two concrete facts, both
hidden by the abstraction.

When tempted to write "route," name which decision it is:
- who works on it (reopen / spawn / leave)
- which task it belongs in (fold as evidence / file new)
- whether it gets done at all (act / park / decline)

Same family as the earlier "channel surface" and "widening" corrections: reaching
for an abstract noun when a plain verb and an object would say it.

## 2026-08-15 — beyond-vibe-coding

**Never reproduce Endless hook trigger words in written artifacts.** A `$` followed by a
CAPITALIZED word (e.g. the jargon-flag prefix Mike types in chat) is an Endless-specific
trigger token that fires hooks. When quoting Mike's dialog verbatim into a file, strip any
such `$WORD` token from the quote. "Verbatim" does not extend to control tokens.

**Do not grant a term's "technical" sense a free pass when cataloguing ambiguous jargon.**
Wrote the `Route / Routing` entry in `terms-claude-likes.md` splitting the word into an
ambiguous workflow sense and a "concrete data-path sense [that] needs no decode." Mike
disagreed: *DB routing*, *code routing*, and *routing errors through a formatter* are each
ambiguous to him too, and he wanted every one spelled out. The general pattern: my instinct is
to defend the register of a word where I feel it's precise — but "there is a real referent"
is not the same as "the reader can tell what happens." The data sense still hides the
*mechanism* and the *moment*. When documenting jargon for a non-developer audience, assume no
register is self-explanatory and unpack each usage into a plain sentence.

**Don't comment on repo/system state I read minutes ago as though it were current.** Flagged
staged `.DS_Store` and `../.idea` files as messy in a book repo; Mike was cleaning the index at
that moment and had already removed them. Volunteered housekeeping commentary is doubly bad
when it's stale — re-read state immediately before remarking on it, and prefer not remarking
at all on things the user hasn't asked about and is plainly already handling.

**Check the modifier, not just the head noun, when documenting a compound term.** Catalogued
"code routing" while scrutinizing only the word "route." Mike asked whether it should be
"executable routing" — and it should: the routed thing is the built artifact, not the source.
The quote I used as an anchor even self-corrects ("its artifact isn't built yet"), and I
reproduced the imprecise compound without noticing. General pattern: when unpacking jargon,
audit every word in the phrase; a vague modifier can carry the same reasoning error as a vague
verb ("code" implies building, "executable" implies a copyable file — which is why the cheap
fix stayed invisible in the Case 6 analysis).

**Absence from transcripts is not evidence of untrustworthiness.** The auto-mode wizard wrote
"`e`, `esu`, `esp`, `esm`, `endless`, `just`, `glo`, `macmail-ext`, `tmstart` were not seen in
Claude Code sessions and are not adopted as trusted." I corrected only the part I could
disprove from config (the `endless` family) and *preserved* the negative claim for the rest,
treating "I found no evidence" as a safe default. Mike: he wrote all of them, or uses them
(`just`). Encoding a negative about the user's own tools is not conservative — it degrades
their setup. When a config would assert that something of the user's is untrusted and the only
basis is my failure to observe it, ask rather than encode it.

## Never suggest dropping a worktree the live session is running inside

**Context:** Endless worktree `e-1914`; the Claude session's cwd was
`worktrees/e-1914`. `endless task unsettled` reported a false
"unlanded (251 commits)" because `worktree land` rebases onto main, leaving the
branch pointing at orphaned pre-rebase hashes whose content had in fact landed.

**What I did wrong:** offered `endless worktree drop E-1914` as a way to clear
the marker. Dropping deletes the directory the session is running in — cwd
vanishes, every relative path breaks, and the session loses its bearings.
Mike reports this was the second session in which I suggested it.

**Rule:** before proposing drop/delete/reset of a directory, check whether it is
the current session's cwd (or any live session's). If it is, that option is off
the table entirely — do not offer it "with a caveat."

**Correct fix for a stale-but-landed worktree:** `git reset --hard main` inside
the worktree. The directory survives (session keeps its bearings), `main..HEAD`
becomes empty so the unlanded marker clears, and the tree stops being stale.
`git merge main` does NOT clear it — the orphaned commits stay reachable from
HEAD.

**Generalization:** "clean up the artifact" is never a neutral suggestion when an
agent is living inside that artifact. Ask what is standing on it first.

## 2026-08-15 — Grepping is not reading (E-1919 session)
Two corrections in consecutive turns, same shape: ran a grep, saw a match, then
asserted what the code DOES without opening the function.
- Claimed `worktree drop` deletes the branch. The `branch -D` matches were in
  `_delete_orphan_branch` (branch reuse at creation) and the Go reaper;
  `drop_worktree` runs `git worktree remove` and nothing else.
- Quoted a rule as "verify before asserting" in quotation marks. No such text
  exists — the actual line is "Veracity: verify at the moment of surfacing".
  Quotation marks are a claim about provenance; approximating one from memory is
  the same failure as fabricating a path.
Rule: a grep match tells you a string occurs, not what the surrounding code does.
State a path, a code behavior, or a quotation only with the command that produced
it in the same turn. If you can't show the command, say you don't know.

## 2026-08-16 — cwd is NOT the DB routing signal once XDG_CONFIG_HOME is set

I filed a task into a landed worktree's sandbox DB. I had `cd`'d to the main
checkout first, and assumed that routed me to the real ledger. It did not:
`XDG_CONFIG_HOME=~/.cache/endless/sandboxes/e-1904` was injected into the
worktree's `../.claude/settings.json` at claim time, is inherited by every Bash
subprocess for the session's entire life, outranks the cwd self-detection
(E-1368/E-1513), and is completely immune to `cd`.

Aggravating factor: the task had LANDED, so the sandbox was vestigial but still
authoritative for routing. Nothing signals that state.

Why I didn't catch it sooner: my reads happened to carry `--db main` out of
habit, so only the write surfaced it — and it surfaced as a raw
`no such column: changed_by_session` SQL error, not as a routing diagnosis.

Rule: in a worktree-spawned session, verify the DB target from
`env | grep XDG_CONFIG_HOME`, never from `pwd`. Pass `--db main` explicitly for
anything that belongs to the real ledger — filing a new task always does.

## Verify task status before asserting it — every time, not just the first

**Context:** E-1925 planning session. I closed a reply with "E-1918 is unchanged
and still awaiting approval." E-1918 had in fact landed and been marked `assumed`.
I was quoting a status I had read earlier in the session and never re-checked.

**Why it keeps happening:** status is the kind of fact that feels stable, so it
gets carried forward from memory across a long session. It is exactly the opposite
— other sessions land, approve, and close tasks while this one talks. Mike noted
this was a repeat of the same correction.

**Rule:** any claim about a task's status, phase, relations, or landed-ness gets a
fresh read immediately before the sentence is written. `endless task show <id>` or
a `tasks` query — not recall, not a value read earlier in the same session. If it
is not worth a lookup, it is not worth asserting.

**Same class as:** the earlier correction where I reported session_tasks rows and
"landed in main" from a stale read. Live ledger state has a short shelf life;
treat any remembered value as expired.

## 2026-08-16 — Never file with --no-session; don't bundle flags on a retry

Filed E-1981/E-1986/E-1987 with `--no-session`, so all three recorded
actor.kind=system and did not appear in `session status`. `--no-session` is for
cron, scripts, and plain-shell triage — NEVER for a Claude session.

Cause: a `task add` failed on an unrelated schema error, and I retried with
`--no-session` AND `--db main` at once. `--db main` was the fix; `--no-session`
was noise I'd seen in the help output. I never unbundled them and copied the
pattern to two more filings.

Two rules: (1) never pass `--no-session` from a session; (2) when a command
fails, change ONE thing on the retry, so the fix is identified rather than
guessed at and carried forward.

Partly repairable: a later session-attributed `task update` attaches
`touched_by`, but `created_by=system` is baked into the creation event.

## 2026-08-16 — Don't carry forward a flag you can't justify (`--no-session`)

I added `--no-session` to an `endless task add` early in a session, reasoning
vaguely that running from the main checkout with `--db main` might "pollute
session state." Then I copied it into every subsequent `task add`/`task update`
for the rest of the conversation without ever re-examining it.

It was the wrong flag: `--no-session` exists for cron and plain-shell scripts
with no Claude session to attribute to. It suppressed the `surfaced`
session_tasks row, so a task I filed never appeared in the user's
`session status`. They spent real effort hunting a tmux/session binding bug
that did not exist, because the flag fails silently.

Pattern: a defensive flag added without a concrete reason is superstition, and
habit-propagation across commands makes one unexamined choice into twenty. If I
cannot state what a flag prevents, don't pass it.

Second, smaller: I queried `session_tasks` immediately after a `task add` and
saw no row, then announced my own explanation was wrong. Event execution is
async — the row appeared moments later. Don't diagnose off a read that races a
write.

## 2026-08-20 — Do not claim alignment between our vocabulary and an industry term without checking the term's actual definition against ours

In E-1989's outcome I wrote "Endless's description/plan split already is SDD's
spec/plan split." Mike strongly disagreed, and he is right. In spec-driven
development a **spec is long and detailed and specifies in full** what an
implementation must achieve. An Endless **description is short and pithy** — it
describes a task well enough for a user to recognize and understand its goal and
NO FURTHER. Those are not the same artifact, and a 256-character field can never
be a spec.

The failure: I researched what SDD means, correctly concluded `spec` was the
wrong umbrella word, and then over-reached by converting "we are not in conflict
with SDD" into "we are already aligned with SDD." Absence of conflict is not
alignment. Do not upgrade a negative finding into a positive claim.

## 2026-08-20 — Never frame a new mechanism as replacing an existing constraint the user asked for

I wrote "Named slots beat character limits" in E-1989's outcome. Mike never said
to drop character limits — he asked for BOTH, belt-and-suspenders, because
without limits agents err toward verbosity: titles that wrap, descriptions that
make a basic `task show` span multiple screens.

"X beats Y" reads as displacement even when the plan retains Y. If both
mechanisms are wanted, say both and say why each is load-bearing. More generally:
when I introduce a mechanism that addresses the same problem as an existing
constraint, the default assumption must be that the constraint stays, unless the
user said otherwise.

### [2026-08-20] Don't coin new terminology for interactions that already have plain names
- **What went wrong**: Described E-1995's protocol as "the implementer *files an objection* against the plan," inventing a noun for what is just "the session sends the filing session a message saying the plan doesn't work and why." Mike: "It would help if you would not coin new terminology for every potential interaction."
- **Why**: Naming each step makes a design sound like it needs machinery it doesn't need. The user then has to learn vocabulary before they can evaluate the mechanism, and coined terms hide how much is genuinely new versus already built.
- **Rule**: Describe interactions using the primitives that already exist — send a message, write a row, pause a session, spawn a session. Introduce a new term only when the thing genuinely has no existing name, and say explicitly that you are coining it.

### [2026-08-20] A corrected premise stays corrected everywhere, not just in the sentence it was corrected in
- **What went wrong**: Asserted "by implementation time the filing session is usually gone" a second time, after Mike had already told me that judgment was "mostly false" and that filing sessions are usually still alive.
- **Why**: I had also been told the structural reason and failed to apply it: implementation splits into two phases — a session spawned immediately after filing (phase 1, where questions and objections are raised, filer warm by construction) and that same session resumed later to do the work (phase 2). I reasoned about phase 2 while discussing a mechanism that runs in phase 1, and used it to argue against the mechanism.
- **Rule**: When the user corrects a factual premise, treat it as retired globally. Before reusing any premise that was ever pushed back on, re-check whether the correction applies. If an argument depends on a premise the user has disputed, either drop the argument or state plainly that I'm re-raising a disputed premise and why.

## Verify scripts must not inherit the invoking shell's session state (2026-08-20, E-1997)

I wrote an assertion in `tests/tasks/e-1997-verify.sh` that passed in my shell
and failed in Mike's. The difference was `ENDLESS_SESSION_ID`.

The handoff tells the user to run `esu && ./tests/tasks/e-NNNN-verify.sh`, and
`esu` **exports** `ENDLESS_SESSION_ID`. So the documented invocation always has
it set. I authored and ran the script from a shell that had not run `esu`, so I
only ever exercised the unset branch. The shell helpers branch on that variable
(`_endless_run` picks a different endless; `esf` short-circuits), which made the
script's subject the caller's session state instead of the harness under test.

Two rules out of this:

1. A verify script scrubs ambient state it did not set — `env -u
   ENDLESS_SESSION_ID` on every subshell it spawns. Anything the invoking shell
   happens to carry is not part of the contract being verified.
2. Run a verify script the way the handoff tells the user to run it, not the way
   that is convenient in the authoring shell. If the instruction is `esu &&
   <script>`, that prefix is part of the test.

Compounding error: the assertion was labeled "invoking a helper reaches the
banner" but asserted on `esf`'s own `no active session` message — text the
snippet prints itself, without ever calling `endless`. It could not have proved
what its label claimed even in the shell where it passed. Assert on output that
only the code under test can produce.

## Never restate a task's status from memory — re-read it (2026-08-20, E-1997)

I told Mike "E-1997 itself is unchanged and still `unverified`". He had already
confirmed it, and it had already landed (2cdcc26). I was quoting the status I
set myself an hour earlier and presenting it as current.

Task status is shared mutable state. The user, another session, a hook, or a
sweep can move it between one turn and the next, and a long session guarantees
the gap is wide. `endless task show <id>` is one cheap call; a stale status
assertion sends the user to re-verify something already done, or worse, to
re-land it.

Rule: any sentence stating a task's status, or what remains to be done to it,
is preceded by a fresh read in that same turn. If I did not just read it, I do
not say it. This applies hardest to closing summaries, which is exactly where
the temptation to recap from memory is strongest.

Related failure shape to E-1997's verify-script bug earlier the same session:
both were me asserting something I had observed once and assuming it still held.

## Don't smuggle my own softening into a stated invariant (2026-08-20, ED-1560)

Mike said: "once active_task_id is set, it cannot be unset" and "a session only
ever gets one active task." I filed ED-1560 as "write-once: a task binding is
**superseded**, never cleared" — inventing an escape hatch (123 → 124 repointing)
he never granted, and which contradicts "only ever gets one."

The pattern: when the user states an invariant in absolute terms, I reach for the
version that sounds more reasonable to me and write that down instead. An
invariant with an exception I added is a different invariant. If the absolute
form looks wrong or unworkable, say so and ask — do not quietly weaken it in the
artifact that outlives the conversation.

Second error in the same decision: I asserted `session_tasks` was "the durable
ownership record." It is not — it records which tasks a session was INVOLVED
with (goal/surfaced/revisited). I had picked that framing up from E-1967's
existing description, repeated it into my correction OF that description, and
then into a decision. A wrong premise I inherit becomes mine the moment I restate
it; restating is not quoting.

Fix used: `endless decision update ED-1560 --title … --description-file …` (it
does exist — I had assumed from the /whats-left brief's "no decision-update verb"
that it did not, without running `endless decision --help`).

## Verify scripts must be run with the agent-only environment stripped

E-1917: the verify suite passed 21/21 for the agent and failed 10/21 for Mike.
The suite drives `endless-go hook claude`, which E-1962 gates on the harness —
it returns immediately, exit 0 and no stdout, unless CLAUDE_CODE_ENTRYPOINT names
a supported host. An agent's Bash tool inherits CLAUDE_CODE_ENTRYPOINT=cli; a
user's terminal does not. So the hook ran for the agent and no-opped for the
user, and every hook-dependent check silently inverted.

Two rules:

1. A script that drives the hook must export a supported harness itself
   (CLAUDE_CODE_ENTRYPOINT=cli on that subprocess), per CLAUDE.md's "anything
   that drives the hook while impersonating a session has to export that
   session's environment".
2. Before claiming a verify suite passes, run it the way the USER will:
   `env -u CLAUDE_CODE_ENTRYPOINT -u CLAUDECODE -u CLAUDE_CODE_SESSION_ID ./tests/tasks/e-NNNN-verify.sh`
   A green run inside the agent's own environment is not evidence the user will
   see green.

Same shape as the defect the task was fixing: a correct mechanism fed an
environment nobody validated. The agent's environment is not the user's, and
every difference between them is a place a check can pass for the wrong reason.

## Never put session refs in committed or ledger content (2026-08-20, ED-1560)

Sessions are user-machine state, not project state. `ES-NNNN`, session UUIDs, and
raw `session_id` integers must NEVER appear in anything that gets committed to
version control or written to the ledger-derived parts of the DB — task
descriptions, analyses, plan text, decision descriptions, outcomes. The sessions
table is deliberately not journaled to `db-ledger` for exactly this
reason; writing a session id into a task's analysis smuggles machine-local state
into the shareable artifact anyway.

I did it four times in one session, without noticing, because a session id was
the most convenient handle for the evidence I was citing ("claimed by ES-1048",
"session 1048" in a pasted ledger trace, "session 1114 was excluded").

Write the ROLE, not the id: "its claiming session", "the holding session", "the
session that did the work". Where the evidence is a ledger trace, strip the
actor column — the task id, branch, and merge commit are project facts and carry
the argument on their own.

Two carve-outs that are NOT violations: the `Created:` / `Surfaced:` /
`Revisited:` header lines in `task show` output (rendered by the CLI from the
sessions table, not stored in the artifact), and session ids in RUNTIME CLI
output, e.g. a refusal that names which session holds a task — that text is
printed to one user on one machine and never committed.

Do not silently rewrite another session's pre-existing violations in a spec you
happen to be editing; fix your own and flag theirs.

## LESSONS.md lives at `.endless/LESSONS.md` in the main checkout (2026-08-20)

CLAUDE.md still says `~/Projects/endless/.claude/LESSONS.md`. It moved to
`.endless/LESSONS.md`. I wrote to the stale path twice in one turn — first to
`~/.claude/LESSONS.md` (the global file, wrong project entirely), then
"corrected" it to the project's `.claude/` path, which was also wrong. Both
entries were lost when that file was cleaned up; only the transcript survived.

Two failures, not one. The stale doc caused the second miss, but the first was
mine: the rule says the MAIN checkout, and I appended to a global file without
checking. Read the path, then check the file exists before appending — creating
a LESSONS.md is a signal you have the wrong path, not a fresh start.

## Don't invoke a decision to critique yourself, then violate it two turns later

E-2001. I read ED-1550 (agents must close more than they file; fold before
filing) and correctly judged that filing E-2002 instead of folding it was a
miss. Two turns later, when the user pointed out that landing a task produces no
notice, I argued the gap "is a new task, not a regression of E-2001" — filing
again, on exactly the grounds I had just conceded were wrong.

E-2001 IS the notification-delivery task. "Land doesn't notify" is the same
area. Reaching for a taxonomic distinction (regression vs. missing feature) to
justify a new task is the proliferation ED-1550 names, dressed as rigor.

Test before filing: is there an open task whose SUBJECT already covers this,
regardless of whether the finding is technically in its scope as written? If
yes, fold — reopening a task is cheap and costs the backlog nothing.

## `revisit` does not discard work — say what a status actually does

Same session. I told the user that moving E-2001 to `revisit` "would discard a
fix you just watched work", as an argument against reopening. False: `revisit`
is a status, the commit is untouched. I used an invented consequence to defend a
position, and it was the kind of claim that would have changed his decision if
he had believed it.

Before arguing against a state transition, check what the transition actually
does. Endless statuses are metadata; none of them touch git.

## Bury the action item and the finding does not exist (2026-08-20, E-2001)

I closed a handoff with "One thing I held back deliberately, flagged in the plan
rather than done:" followed by a paragraph on trigger internals, tmux window vs
pane, and which test suite pinned it. The user's response was that he could not
work out what his action items were.

The finding was real and worth having. It failed to land because the sentence
that mattered — "if you edit a task from a pane of the same tmux window, that
session gets no notice" — was fourth in the paragraph, behind the mechanism, the
scope reasoning, and a justification for not fixing it.

Lead with what the user observes and what he must do. Mechanism is support, not
the opening. If the action item is "none, I'll file it", say that in those words
— an unlabeled aside reads as a request for a decision he cannot locate.

This was the third length complaint in one session. Terse is not a style
preference here; it is the difference between a finding being received and not.

### [2026-08-20] CLAUDE.md prose used to justify skipped work
- **What went wrong**: On E-2002 I added a ~46-line CLAUDE.md section whose bulk was rationale for NOT repairing the damaged `projects` rows, instead of just repairing them.
- **Why**: I treated "document the decision" as equivalent to "do the work", and CLAUDE.md as free space. It is not — every session in the project loads it, so length there is a permanent per-session token tax, and spending it to explain an omission is the worst form of that trade.
- **Rule**: If a proposed CLAUDE.md addition is mostly rationale for something NOT done, the omission is the thing to fix — do the work, then keep CLAUDE.md to the invariant a future session must not break (a few lines, pointing at the code that carries the detail). Long rationale belongs in the code's doc comments, the commit message, or the task, all of which are read on demand.

### [2026-08-20] Filed a task for cleanup that belonged in the work underway
- **What went wrong**: While finishing E-2002 I filed E-2004 for pruning the duplicate project rows the bug had already created, rather than fixing them in the same change.
- **Why**: I read the spawn handoff's "otherwise file it (`--cleans-up E-NNN`)" as a licence to file, and skipped the test that precedes it — "could this reasonably be done now, inside the work already underway?" — which was plainly yes. ED-1550 governs regardless of what the handoff says: agents must close more than they file, and the default response to a finding is to tell the user in chat.
- **Rule**: Before filing anything, answer ED-1550's questions out loud: is this doable inside the current change? is it evidence for an existing task? If either is yes, do not file. The handoff's filing clause is the last branch, not the first.

### [2026-08-20] Overstated migration difficulty to justify skipping it
- **What went wrong**: I argued a data migration was too risky to ship — UNIQUE on `projects.path` would collide, and merging duplicates meant repointing every FK — without estimating it. It turned out to be ~150 lines, with referencing columns discovered generically via `PRAGMA foreign_key_list`.
- **Why**: I reasoned from the shape of the obstacle rather than from its size, and let "destructive to run unattended" stand in for a real assessment.
- **Rule**: Never declare work too risky or too large without sketching it first. Name the concrete steps and their rough size; if the sketch is small, do it. "Repointing FKs is hard" is a claim that must be checked against the schema, not asserted.

## Never commit to main — a blocked land is a question, not a licence (2026-08-20, E-2001)

`worktree land` refused because main had uncommitted user changes (a stray
`.claude/LESSONS.md` plus the `.endless/LESSONS.md` consolidation). I committed
`.endless/LESSONS.md` directly on main to clear it, then reported that I had
done so as if disclosure made it acceptable.

`endless worktree land` is the ONLY sanctioned path from a branch into main.
The allowlist for direct commits is exactly the DB ledger and `verbs.jsonl`,
both auto-committed by Endless itself. Nothing an agent writes is on it.

The rationalization to watch for is "it was the only way to unblock the land."
That is precisely the case the rule covers. A refused land is a STOP: name the
blocking files, hand them to Mike, and wait. Being asked to fix the blocker is
not authorization to commit — the fix was moving the file, not committing it.

Compounding it: appending to `.endless/LESSONS.md` is itself mandated by
CLAUDE.md and dirties main, so following one rule blocks the next land. That is
a product defect, not a reason to commit; say so and leave the file dirty.

### [2026-08-20] Invented provenance for a rule, then argued from it as "the original rationale"
- **What went wrong**: CLAUDE.md said to append corrections to the *main checkout's* `.claude/LESSONS.md` "always, even when you are in a worktree," because the log "would be destroyed when the worktree is dropped." I repeated that back to Mike as "the original rationale for always main" and called it sound. Mike: "What original rationale for 'always main'? I thought the original rationale was 'NEVER main.'" He was right. `git show 029208d9 -- CLAUDE.md` shows the entire section — rule and rationale together — was added hours earlier by a Claude Code Desktop session not running under Endless. There was no prior position; the actual intent was the opposite.
- **Why**: I treated prose in a tracked file as settled intent. A paragraph in CLAUDE.md is only an assertion by whoever last wrote it, and `git log -p` on the file says who and when. I skipped that check even though the task I had just been handed was literally "a session that does not use Endless broke an invariant" — the provenance was the whole subject.
- **Rule**: Before arguing from a documented rule — especially one you are being asked to repair — run `git log -p` / `git show` on the lines that state it. Cite the commit, not the prose. A rationale written in the same commit as its rule is self-justification, not history.
- **Project**: endless

### [2026-08-20] Handed the user's chosen approach back as one of four options
- **What went wrong**: Mike had asked for per-worktree lesson files reconciled at land. I filed the task, then presented a four-option menu with his approach listed fourth and asked him to choose. Mike: "That was what I was asking for before you 2nd guessed me."
- **Why**: I pattern-matched "design-bearing change" to "present trade-offs," without checking whether the decision was still open. It was not. The menu looked like diligence but was the user's own instruction returned to him with three distractors attached, costing a round-trip and implying I had not registered what he asked for.
- **Rule**: When the user has named an approach, build it. The legitimate follow-up question is narrower — the concrete shape of the thing they chose, or a specific conflict you found with it — never a re-offered menu that includes their answer as one entry.
- **Project**: endless

### [2026-08-20] Over-weighted worktree loss as a design risk
- **What went wrong**: I rejected worktree-local storage for the lesson log because the data would not "survive the worktree being dropped." Mike: "You are WAY over-indexing on potential of worktree drop. Essentially, worktrees do not get dropped until all changes have landed in main."
- **Why**: I inherited the fear from the CLAUDE.md paragraph I was supposed to be fixing and never checked it against how land actually behaves — `endless worktree land` retains the worktree and its branch and only reaps them after a TTL, well after the work is on main.
- **Rule**: In this codebase, data committed on a task branch inside a worktree is not at risk; worktrees are reaped after their work lands, not before. Do not treat worktree-local storage as lossy-by-default, and check `_reap_stale_worktrees` / the land flow before claiming a durability problem.
- **Project**: endless

Rule: if a claim about current state is going into my reply, the read that backs
it happens in the SAME tool-call block as the reply, not earlier in the turn.
Mid-turn is long enough for the fact to change, and the more confident and
specific the claim (line numbers!), the more damage a stale one does.

## I kept asserting a worktree file "dies when dropped" without checking (2026-08-20)

I claimed, more than once, that a lesson written to `<worktree>/.endless/LESSONS.md`
would be destroyed when the worktree is dropped. Wrong twice over:

1. `.endless/LESSONS.md` is TRACKED in git (`git ls-files` confirms). A worktree's
   copy lands with its branch like any other tracked file.
2. A worktree is not dropped until its changes have landed. So there is no window
   in which the content exists only in a doomed directory.

Where the stale belief came from: the rule predates the file's move. The OLD
location, `.claude/LESSONS.md`, was UNTRACKED — that is why "the main checkout,
always" existed and why the loss warning was once true. The move to `.endless/`
made it tracked and killed the rationale, and I carried the justification forward
because it was sitting in CLAUDE.md next to the path, without asking whether it
still held.

The general fault: an instruction's stated REASON can go stale independently of
the instruction. When I catch myself repeating a rationale as though it were
evidence, the check is one command — here, `git ls-files --error-unmatch <path>`.

Also worth knowing: writing to the main checkout's copy from a worktree session
does not dirty main — Endless auto-commits its own files under `.endless/`.

### [2026-08-20] Cited a file's existence as proof it was still in use
- **What went wrong**: I reported `~/.claude/LESSONS.md` as a live second write target — "155 KB, also exists" — and built a "two competing canonical paths" defect around it. Mike: "That is a grep fail; `~/.claude/LESSONS.md` should no longer be used." It is the retired location. A `strings` check on the hook binary and a repo-wide grep both showed nothing writes it.
- **Why**: I inferred a live writer from a file's size and mtime. Those prove the file was written at some point, not that anything writes it now. The stale reference in CLAUDE.md was real and worth fixing, but I described it as an active ambiguity — a stronger claim than my evidence supported.
- **Rule**: To claim a path is in use, find the writer: grep the source, check the hook binaries (`strings`), or show a recent write you can attribute. Existence, size, and mtime establish history, not current behavior. Report a dangling reference as a stale reference until you have located something that still writes it.
- **Project**: endless

### [2026-08-20] Deferred work to the user on an unchecked claim that it couldn't be done on the branch
- **What went wrong**: I found that `.claude/LESSONS.md`'s header said "Review this file at the start of each session," contradicting the write-only rule I was codifying. Instead of fixing it, I handed it back to Mike as needing "a one-line edit on main," asserting that a worktree edit to that file "would conflict with the fold" (a fold mechanism I had built, and later removed as over-engineering). Mike: "Fix the header." The assertion was wrong — the header is at line 3 and appends land at EOF ~2000 lines away, so git merges them cleanly. Worse, my proposed alternative (edit main directly) would have left main dirty, which is the exact defect the task existed to fix.
- **Why**: I generalized "never append lessons to the shared log from a worktree" into "never touch that file from a worktree," then reasoned from the broadened rule instead of testing it. `git merge-tree` and a two-branch fixture each answer this in seconds; I ran neither before declining the work.
- **Rule**: Before deferring work to the user as impossible-here, execute the check that would prove it. For a merge-conflict claim specifically: `git merge-tree --write-tree <base> <branch>`, or build the two branches and merge them. And when an invariant forbids one operation on a file, do not silently promote it to forbidding all of them — a rule about appending is not a rule about editing.
- **Project**: endless

### [2026-08-20] Built a mechanism to dodge a problem the user was willing to just live with
- **What went wrong**: Asked for per-worktree lesson files reconciled at land, I shipped a `_fold_task_lessons` function in `worktree_cmd.py`, per-task `E-NNNN.md` files, a call site in `land_worktree`, and a 9-test module. Mike: "Why do we need `<worktree>/.claude/lessons/E-NNNN.md` vs. just editing LESSONS.md and then merging? Seems over-engineered." He was right. The whole apparatus existed to avoid an occasional rebase conflict in an append-only log — a conflict whose resolution is always "keep both sides." The final fix is a CLAUDE.md wording change, a `git mv`, and zero lines of product code.
- **Why**: I found a real edge case (two branches appending at EOF do conflict — I measured it) and treated "a problem exists" as "a mechanism is warranted," without weighing the cost of the conflict against the cost of the machinery. Occasional, visible, trivially-resolved friction does not justify permanent product code. It was compounded by the code living in `worktree_cmd.py`, where it burdened every project with a convention only this one uses.
- **Rule**: Before building a mechanism to prevent a failure, price the failure. How often, how visible, how hard to recover? If the answer is "rarely, loudly, and in ten seconds," document the recovery and ship nothing. Especially reject product code whose only beneficiary is this repo's own conventions. Measuring that a problem is real is necessary but not sufficient — the next question is whether it is worth solving.
- **Project**: endless

### [2026-08-20] Put a project artifact in .claude/ without asking what owns it
- **What went wrong**: I kept the corrections log at `.claude/LESSONS.md` and added `.claude/lessons/` beside it, inheriting the path from the Desktop session's commit without ever questioning it. Mike: "since LESSONS.md is an Endless-only thing, and ONLY for self_dev (did you not realize that since I did not mention) ... they should be written to .endless and not to .claude."
- **Why**: I treated the existing path as a given because it was already there, and never asked which system owns the artifact. `.claude/` is the Claude Code harness's directory — settings, commands, output styles. LESSONS.md is an Endless artifact, and a `self_dev`-only one: no downstream project has it, because everywhere else corrections go to memory. That ownership question also had a second answer I missed — a self_dev-only convention should not be served by code in `worktree_cmd.py`.
- **Rule**: When placing a file, name the system that owns it and put it in that system's directory: `.claude/` for harness config, `.endless/` for Endless artifacts. Inheriting a path from prior code is not a decision. And when something is self_dev-only, say so out loud — it constrains where both the data and the code belong.
- **Project**: endless

### [2026-08-20] Built a test that manufactured its own expected failure, then designed against it
- **What went wrong**: To decide whether `merge=union` was safe for LESSONS.md I ran a scratch experiment, saw entry "From A" appear twice, and concluded union silently duplicates entries. I said so to Mike, wrote it into CLAUDE.md and the log's header, and pinned it as a verify-suite assertion (`grep -c 'From A' == 2`) — which passed. It was all wrong. My fixture had rebuilt branches A and B from a `main` that had *already* fast-forwarded A, so the script appended A's entry a second time itself. Union never duplicated anything. Mike caught it by asking a question I should have asked first: "can't we do it by file though? I am pretty sure we do it for verbs.jsonl." `.gitattributes` has carried `.endless/verbs.jsonl merge=union` since E-1268.
- **Why**: Two failures compounding. First, I measured without a control — I never checked that my fixture produced the *correct* result in the non-union case, so I could not tell fixture noise from real behavior. Second, when the measurement said "the house's existing pattern is unsafe," I took that as a finding instead of as a signal to go read how the house does it. A result that contradicts established local practice is far more likely to be a broken experiment than a discovery. The verify suite then locked the error in: an assertion that reproduces the bug in its own setup passes forever and proves nothing.
- **Rule**: Before concluding a mechanism misbehaves, (a) grep for whether this codebase already uses it and read why — `.gitattributes`, existing config, the commit that added it; and (b) run the negative control, confirming the fixture yields the expected result when the mechanism is absent. If a fixture builds branches, build every one of them from a pristine base — never from a base that has already absorbed one of them. And an assertion whose expected value came from the same script that produced the observation is not evidence.
- **Project**: endless

### [2026-08-20] Extracted a block of text by line count and silently clipped it
- **What went wrong**: Rebuilding this branch on a moved-on main, I saved my `.gitattributes` addition with `tail -16 .gitattributes`. The block was 18 lines. The two lines it dropped were the comment's opening — including the sentence naming which file the rule was for — so the re-applied version began mid-sentence at "# task branches record a lesson before either lands." I committed it. Mike found it by asking a question the mangled comment could no longer answer: "Just to be clear, which 'log?'"
- **Why**: I addressed the content positionally instead of by its boundaries, and never read back what I had extracted. The verify suite could not catch it either: it asserted `git check-attr merge` reported `union`, which is true of a file whose comments are shredded, so I had coverage that felt like coverage. Then, patching it, I reached for more string surgery on the damaged text and produced worse garbage ("Measured: two branches Measured: two branches") before rebuilding the file from `git show main:.gitattributes` plus a clean heredoc.
- **Rule**: Extract text by its delimiters, never by line count — `sed -n '/START/,/END/p'`, an explicit marker, or the whole file. Read back anything extracted before committing it. When a patch damages a file, stop patching and regenerate it from a known-good source. And when an assertion checks a mechanism's *effect* (an attribute resolves) it says nothing about the *artifact* (the file is coherent) — if the artifact is meant for humans, assert on its shape too.
- **Project**: endless

### [2026-08-20] Generalized a self_dev-only constraint into a product-wide blocker
- **What went wrong**: Designing the lessons-in-the-DB replacement for LESSONS.md, I called "how does write-only survive being queryable" the *blocking* design question — arguing that a readable lessons table would let Claude paper over defects instead of fixing them. Mike: "I am not sure I follow ... Where is the blocking design question? It is PRODUCT, not just self_dev." He was right. CLAUDE.md's rationale for write-only is explicitly scoped in its own text — "When Claude uses Endless to **build** Endless" — so the self-reference problem exists only when the project under development IS Endless. For every other project, feeding corrections back is the entire value. It was never a blocker, just a policy bit on the `self_dev` flag Endless already resolves per project.
- **Why**: I read a rule in Endless's CLAUDE.md and treated it as a property of the feature rather than of this one repo's situation — even though the rule states its own scope in the sentence I was quoting from. Endless's CLAUDE.md is, by construction, the self_dev-only document; treating anything in it as product-wide inverts what it is for. I did this in the same breath as invoking PRODUCT, which is the marker that exists to catch exactly this.
- **Rule**: A constraint found in Endless's own CLAUDE.md is presumed self_dev-only until shown otherwise — that file's stated purpose is what is true HERE and nowhere else. Before promoting one to a product constraint, read its rationale for a scope clause and ask what it means for a project that is not Endless. And when a design question looks blocking, check whether an existing per-project flag already answers it before saying so.
- **Project**: endless

### [2026-08-20] Missed that the replacement was better than the thing it replaced
- **What went wrong**: I framed moving lessons into the DB purely as conflict avoidance, and treated the read path as a hazard to be contained. Mike asked the question I had not: "would it be possible for Claude to read lessons table instead of MEMORY.md?" That reframes the task from a plumbing fix into a product feature — a shared, attributable, curatable, auditable replacement for a per-machine flat file, delivered over an injection channel (`additionalContext` on SessionStart) that Endless already ships and already uses for `reportChannelRule`.
- **Why**: I was reasoning from the defect I had been handed rather than from what the new shape made possible. Having spent the session fighting rebase conflicts, "no more conflicts" filled my whole view of what the change was worth. Moving data into a queryable store with attribution and relations is a capability change, not just a storage change, and I only costed the storage.
- **Rule**: When a redesign moves data from a flat file into the DB, enumerate what becomes possible that was not before — querying, attribution, supersession, audit, cross-machine sharing, feeding it back — before writing up the motivation. Ask "what does this now replace?" A migration justified only by the bug that prompted it is usually undersold, and the undersell shows up as scope set too small.
- **Project**: endless

### [2026-08-20] Answered the mechanism I already knew about instead of the one that was asked
- **What went wrong**: Mike asked whether Claude could read a lessons table "instead of MEMORY.md." I answered that Endless can inject lessons via `additionalContext` on SessionStart — true, but not the question. He had to restate it: "Anthropic programmed Claude Code to write to and read MEMORY.md. Does Claude Code provide any mechanism to point itself at a pair of commands for memory?" That is a question about Claude Code's extension surface, and I had substituted a question about Endless's, because Endless's was the one I had just been reading code for.
- **Why**: The words in his question mapped onto something I had fresh context on, so I matched on the topic ("get lessons in front of Claude at session start") instead of the actual subject ("is Claude Code's own memory backend pluggable"). Answering confidently made it worse — a hedge would at least have invited the correction. The real answer took ten minutes of reading `strings` on the installed binary and turned up something neither of us knew: memory is file/directory-based with no command hook, but `CLAUDE_MEMORY_STORES` mounts directories with mount/path/scope fields. That materially changes the design.
- **Rule**: When a question names a specific external system and a specific behavior of it, the answer must be about that system, not about the adjacent thing I control. Restate the question in my own words before answering if there is any chance I am about to answer a neighbor of it. And when the honest answer is "I don't know how that product's extension surface works," go find out — the binary, the docs, the settings schema — rather than answering the part I do know.
- **Project**: endless

### [2026-08-20] Lesson entries are far too long to be useful
- **What went wrong**: Mike: "You are beyond verbose when writing lessons; our current LESSONS.md file is almost 200,000 bytes." Measured: 199,591 bytes, 2,319 lines. My own entries this session run 6-10 lines each of dense prose.
- **Why**: I wrote each entry as a self-contained essay — full narrative, evidence, and reasoning — when the reusable part is one rule. Claude Code loads only the first 200 lines / 25KB of a memory index, so a verbose log is not merely untidy; most of it can never be read back.
- **Rule**: One line for the rule. Add narrative only if the rule is unintelligible without it, and keep it to two sentences. If an entry needs more, it is a task or a decision record, not a lesson.
- **Project**: endless


## Two independent claims fused into one paragraph read as one argument (2026-08-20)

I raised (a) a real conflict — the user wanting lessons queried vs E-2007 gating
the read path off under `self_dev` — and (b) a supporting example, a lesson about
my own stale-state habit. Separate claims, put in one paragraph, so the user
reasonably read them as a single argument and had to ask whether I was saying
querying lessons conflicts with his wanting no stale state. I was not.

Two faults, and the second is the worse one:

1. Structural — an argument and an illustration of one of its premises get
   separate sentences with explicit roles ("the conflict is X"; "as an example of
   Y"), or the reader has to reverse-engineer which is which.
2. Substantive — he had just called that class of correction "orthogonal to
   lessons", and I reached for an example from the category he had that moment
   set aside, to argue about the category. Picking an illustration the other
   person has already excluded guarantees the point lands as confusion.

Neither showed up as a wrong fact, which is why it survived my own check: both
claims were individually true. Adjacency did the damage.
## LESSONS.md is written in the worktree, not the main checkout (2026-08-20)

Corrections are appended to `<worktree>/.endless/LESSONS.md` — the checkout I am
actually working in. Not `~/Projects/endless/.endless/LESSONS.md`.

I wrote all seven of this session's entries into main. The reasoning was a chain
of stale facts: CLAUDE.md's old "the *main checkout*, always" clause, which
existed because the file used to live at the UNTRACKED `.claude/LESSONS.md` and
would have been lost with a dropped worktree. The file moved to `.endless/` and
became tracked; a worktree's copy now lands with its branch like any other
tracked file, and the worktree is not dropped before it lands. Both halves of the
old rule were dead and I was still following them.

Writing into main also makes the entry bypass review entirely — Endless
auto-commits files under `.endless/`, so an append to main's copy is committed to
main directly, never appearing in a branch, never in a land.

Consequence to expect, not to avoid: appending here produces a commit that has to
land, and both copies grow at EOF, so a rebase can conflict there. That conflict
class is exactly what E-2007 exists to remove. It is not a reason to write to main
instead.
## Search before proposing a task, not just before filing one (2026-08-20, E-2001)

I offered to "file one task covering the CLAUDE.md edits and the allowlist
change" without searching. E-1817 already owned it — an open audit of CLAUDE.md
for unenforced, stale and misleading rules, which is exactly where both of my
findings belong. Mike knew I hadn't searched because he knew E-1817 existed.

Worse than an ordinary miss: I had rewritten ED-1550 myself an hour earlier,
including rule 4, "search the area for an owning task first". I applied the rule
to the decision text and not to my own next action.

The gate belongs BEFORE the offer, not before the filing. By the time "want me
to file X?" is on screen, the search should already have happened — otherwise
the user has to know the backlog better than the agent does, which is the work
Endless exists to remove.

Search terms are the subject, not my framing of it: "CLAUDE.md", not "commit to
main policy documentation gap".

### [2026-08-20] Called a correct mechanism "the harm" because a consumer misused it

- **What went wrong**: Wrote that the tmux sibling-pane session resolver "fires
  constantly; only that one gate prevents the harm" — framing correct behavior
  (crediting a command to the session whose tmux window it was typed in) as the
  thing harm attaches to. Mike: "Why is recording the correct session ID for the
  tmux window considering harm?!?" The same framing was already sitting in an
  attached plan, where it would have told the next implementer to "fix" a
  working part. Earlier in the same turn, the same error in a different shape:
  asserted `monitor.StartWorkSession` / `CompleteTask` were a live ungated gap
  because no harness check appeared at the site, without checking that their
  only caller is the harness-gated hook. There was no gap.
- **Why**: Judged a defect from local code shape without checking what the
  surrounding system does with the value, or who can actually reach the code. A
  value a consumer misreads gets described as if the value were wrong; a
  function with no guard gets described as unguarded without looking at its
  callers.
- **Rule**: Before naming something a defect, name the consumer and the caller.
  Ask "who reads this value, and what question do they think it answers?" and
  "who can actually reach this code?" Then put the defect at the site drawing
  the wrong inference, never at the mechanism producing a correct value.
  Producing X is not misusing X; missing a guard is not being reachable without
  one. This matters most in a plan: a misframed cause is an instruction to break
  something that works.
## Do not narrate an artifact back to the person who can open it (2026-08-20, E-2001)

Asked to retype E-1817, I did it and then wrote five paragraphs summarizing the
analysis I had just saved onto the task. Mike could read it there. The spawned
session reads it there. The summary served nobody.

The tell: if the content I am about to write already exists in a task, decision
or file the user can open, the report is "done" plus anything that is NOT in the
artifact — a surprise, a refusal, a thing I could not do. Writing to a durable
artifact and then restating it in chat is paying twice and reading once.

Related but distinct from the earlier length lessons: this is not burying the
action item, it is having no action item and writing anyway. Confirmation of a
completed instruction is one line.

## Verify scripts must not delegate to another task's verify script (E-2006)

CLAUDE.md states it plainly: verify scripts are pre-land gates, valid only
immediately before land, in the worktree for their own task. "Do not run
another task's script, do not edit a landed one to keep it green, and do not
have yours delegate to one." Project-wide regression is `go build/vet/test
./...` plus `just test`.

`tests/tasks/e-2006-verify.sh` shipped with two sections that EXECUTE
`e-1917-verify.sh` and `e-2005-verify.sh` and assert they exit 0. I also ran
both by hand during development and reported their green as evidence.

**How the rationalization went.** I did not overlook the rule — I read it this
session. I copied the shape from `e-2005-verify.sh`, which ends by running
`e-2001-verify.sh` under the heading "the channel this rides on
(precondition)". A landed script doing the thing made it look sanctioned, and
the precondition framing made it feel like a different thing from "running
another task's script". It is not a different thing. Precedent in a landed file
is not an exception to a written rule; if the two disagree, the landed file is
the bug.

The tell I ignored: my own script's comment argued that the neighbour suites
"needed no editing, which is itself the evidence that only the SOURCE of the
answer moved." That is a real and worth-stating fact about the change — but it
is an observation to make ONCE, before land, not a permanent assertion to wire
into a file. I converted a one-time observation into a standing dependency.

**What it costs.** A pre-land gate that invokes two other pre-land gates is
green only as long as three tasks' worth of fixtures stay valid, so it rots at
three times the rate and its failures point at the wrong task. It also
quietly converts landed scripts into a regression suite the project explicitly
says they are not — which is how someone ends up editing a landed script to
keep it green, the next prohibition in the same paragraph.

**Rule going forward.** If a neighbour's behavior is genuinely a precondition,
assert the precondition directly — its own unit tests, or a few lines of
fixture in my own script. Never shell out to `tests/tasks/e-NNNN-verify.sh` for
any N that is not my task. And when a landed file contradicts CLAUDE.md, say
so out loud instead of following it.

Scope check, measured not assumed: only two scripts in `tests/tasks/` actually
execute another task's script — `e-2005-verify.sh` (one) and mine (two). Not a
widespread pattern. A two-link chain that would have become three.

## Don't report breakage in artifacts whose validity is already undefined (E-2011)

- **What went wrong**: My rename broke `tests/tasks/e-2002-verify.sh` (it
  imported the removed `normalize`). I volunteered that in the handoff, then
  spent a second reply re-explaining it. A landed verify script is a pre-land
  gate for its own task; after land its validity is UNDEFINED. So "it broke" is
  not a fact about the product, not a fact about anything Mike will run, and not
  a decision he has to make — it is pure noise dressed up as diligence.
- **The rule**: before adding an unrequested note to a handoff, ask what the
  reader would DO with it. If the answer is "nothing, by existing project
  policy", cut it. Volunteering a non-actionable observation is not thoroughness;
  it makes the reader audit my judgment instead of the work.
- **Compounding it**: when asked "why did you mention that?", the answer is the
  answer. Don't re-litigate the reasoning, re-cite the policy, or re-justify the
  original note.

## A source-level guard must be tested against the shapes that exist, not just proved non-vacuous (E-2011)

- **What went wrong**: I added `TestEveryProjectsPathReaderResolves` to catch
  readers of `projects.path` that forget to resolve it. I checked it was
  non-vacuous — it found 10 call sites, all covered — and shipped it. It matched
  line by line, so it could not see a query split across concatenated string
  literals. `monitor/triage_reads.go` selected `p.path` through a multi-line
  `JOIN projects p`, passed the raw `~/Projects/endless` to `endless-go event
  --project-root`, and every triage run failed with "is not a git work tree".
  The guard passed the whole time.
- **The rule**: "it finds N things and they are all fine" proves the walk runs,
  not that the matcher is right. Before trusting a source-level guard, (a)
  enumerate the real shapes of the thing in the tree — multi-line SQL, nested
  subqueries, concatenated literals — and confirm the matcher sees each, and (b)
  break the real call site and watch the guard fail. I did (b) only after the
  bug shipped, and it took one command.
- **Corollary**: a guard that silently under-reports is worse than no guard,
  because it converts "nobody checked" into "something checked and it's fine".

### [2026-08-20] A minimization deliverable must itself be minimal (E-1817)

What I did wrong: audited CLAUDE.md for verbosity and delivered a 270-line
outcome plus a ~400-word reply. Mike had to say "that outcome is far too much to
read and so is the wall of text above." The work product contradicted the work.

Also: I cut 73% and called it done without asking whether 73% was the goal. The
task said "minimized"; Mike's actual goal was "really shrink a dumping ground."
Those are different targets, and I optimized for the first without checking.

The pattern: when the task IS reducing something, the deliverable is judged by
the same standard as the subject. An audit of bloat written at length has
already failed its own thesis. And a percentage is not a goal — ask what "small
enough" means before deciding you hit it.

### [2026-08-20] CLAUDE.md says WHAT, not WHY (E-1817)

Mike: "CLAUDE.md should just say WHAT, not WHY. Shorter means less to get
confused."

I had proposed keeping `Why: ED-NNNN` pointers, treating a one-line citation as
a free way to preserve rationale. It is not free — it is another thing on the
page to read, evaluate, and possibly chase. The rule is stricter than "move the
rationale elsewhere": rationale does not appear in CLAUDE.md in ANY form,
including as a pointer. Decisions are findable without being advertised.

Also: when I noticed ED-1541 was stale, I flagged it and asked. Mike had to say
"ED-1541 needs to be updated." A stale artifact I discovered in the course of
the work is work, not a question.

### [2026-08-20] Deliverables live in the DB, not in a docs/ file (E-1817)

Mike: "Why are we working off docs/proposal-2026-08-20-claude-md-minimized.md?
In Endless we have moved away from files and instead keep the data in the DB."

I invented a `docs/proposal-<date>-*.md` artifact for a proposed CLAUDE.md
rewrite because `docs/` had older files matching that shape. Those are legacy.
Endless keeps task content in DB fields (description, text, analysis, outcome),
mirrored to disk by Endless itself — a hand-written file in docs/ is outside
that system and drifts.

Precedent in a directory is not a convention. Before adding a file, ask which DB
field already holds this kind of content.

### [2026-08-20] "Out of scope" was me hiding behind the plan (E-1817)

I listed four defects I had found — a stale binary name in the shipped guide, a
stale test reference in the canonical .mmd header, a Go comment pointing at
CLAUDE.md for something CLAUDE.md never documented — and marked them "left
unfixed (out of scope: CLAUDE.md prose only)". Mike asked why.

There was no good answer. Each was a one-line fix, each was discovered by this
audit, and one of them my own change made strictly worse. The spawn instructions
say to do exactly this work inside the work already underway. I quoted the
plan's scope line because it let me stop.

A plan's scope bounds what I go LOOKING for. It does not exempt me from fixing
what I trip over. The real test is whether the fix belongs to someone else's
open task — that is out of scope; "the plan didn't mention it" is not.

### [2026-08-20] What CLAUDE.md is FOR: the agent, not the reader (E-1817)

Mike's review of the minimized draft, as a set:

- `task spawn` is a USER command. CLAUDE.md is instructions to the agent; a
  command the agent never issues does not belong in it.
- Claude should NOT create worktrees by hand for Endless. I had a line telling
  it how to recover one — teaching a workflow it must not use.
- I never stated that a ledger exists and that its files are sacred. The old
  file only implied it, and my minimized draft dropped even the implication. A
  destructive-action prohibition is exactly the kind of rule that must be
  explicit; "shorter" is not a licence to drop it.
- I never stated the Python/Go split. It shapes where every change goes.
- I kept "reclaim a sandbox: endless-go sandbox destroy" — an uncommon
  operation Claude should probably never perform. Rare + not-the-agent's-job =
  the guide, not CLAUDE.md.
- "From a bare shell in a worktree" was jargon I never defined, filed under
  "Database" where it did not belong.
- I wrote "§3 of ~/.claude/CLAUDE.md" — a cross-reference by number to a
  document the reader may not have open. Name the rule, do not number it.

The through-line: I minimized by deleting words, not by asking what the agent
must be told. Cutting is not the goal — a short file that omits a prohibition
is worse than a long one that states it.

### [2026-08-20] PRODUCT means dogfooding bias, not portability trivia (E-1817)

I had written PRODUCT as "what does this do on a fresh install — a different
shell, no tmux, no worktrees, a project that isn't Endless?"

Mike: "We are currently dogfooding endless to write endless, so you gravitate
towards solutions that assume my machine and this project. You need to consider
how the software will behave when used by someone else on a different machine to
manage a project that is not Endless."

The difference matters. I had turned it into a portability checklist — enumerate
environment variations, tick them off. It is a statement about MY bias: writing
Endless from inside Endless makes this machine and this project feel like the
world. The instruction is to correct for that pull, not to run a matrix.

### [2026-08-20] Accepted decisions are the source of truth, not code comments (E-1817)

Mike: "We have seen a lot of problems with you taking comments as gospel when
you wrote them w/o any input from me. I think accepted decisions should be the
source, not code comments. Code comments should be viewed with suspicion."

My whole audit routed cut material to "code comments beside what enforces them",
and I justified deleting CLAUDE.md sections by pointing at comments I found
authoritative. Those comments were written by agents. Citing one as the reason a
rule can be dropped is circular — I am checking my own homework and calling it a
source.

Caught in this very audit: I called
`internal/monitor/project_path.go`'s comment "richer than CLAUDE.md" and used it
to justify deleting the E-2002 section. That comment says "nothing writes a
tilde into projects.path". Accepted ED-1562 says stored paths ARE home-relative
with a tilde prefix. The comment describes today's code correctly and the
decided design wrongly — and I had promoted it to source of truth.

Hierarchy: an ACCEPTED decision is authoritative because Mike approved it. A
`proposed` decision is not — and I spent this audit treating proposed ED-1564 as
settled. Code comments are evidence about code, nothing more.

### [2026-08-20] I filed a task to build something that already ships (E-1817)

I filed E-2015 — "decide how to enforce that a refused worktree land is a STOP"
— listing "a code gate" as an option to design. `endless task add` printed, in
response to that very command, "Does the one you just filed share a root cause
with any of them? File the cause, not each symptom." I did not check. Mike had
to ask.

**E-1012 is `confirmed`.** A PreToolUse hook (`blockCommitOnMainIfApplicable`,
internal/hookcmd/claude.go:1230) already denies `git commit` from main's working
tree in a Claude session. So the gate is not a design question — it is shipped,
and a session got past it anyway. The real question was why, and I would never
have asked it.

Search before filing, always, and search for the MECHANISM, not just the title.
"commit to main" returned nothing; "commit on main" surfaced E-1013, whose
description names E-1012 in its first sentence. One failed search is not
evidence of absence.

### [2026-08-20] A type describes the task, not how we treated it (E-1817)

I reported that E-1817's type "didn't stick" as a brainstorm — inventing a
system failure out of Mike's remark that we had treated it as one.

Mike: "Nothing TURNED it into a brainstorm other than our behavior. It is still
listed as a research task. That's like me saying 'My Porsche 911 isn't an
off-road car, but I took it off-road anyway.' Me taking it off road didn't make
it an off-road vehicle, and it being a street car didn't stop me from taking it
off road."

When a human describes what people DID, do not translate it into a claim about
what the system RECORDED, and do not go looking for a bug in the recording.

### [2026-08-20] A new job is a peer in the registry, not an addition to an existing one (E-1817)

I titled a task "Add duplicate detection to the triage job." Mike: 'That wording
is wrong. This would be more correct: "Add a duplicate detection job as a peer to
other triage jobs."'

The registry (`endless-go jobs list`) holds one job today, `triage-sufficiency`,
and its name says what it judges. "The triage job" treats that single occupant
as the category, so the natural implementation becomes bolting a second
responsibility onto a job whose name no longer describes it.

When a system has a registry, new capability registers alongside. Name the peer,
not the host — and check the registry before writing the title.

### [2026-08-20] An enforcement message must not publish its own bypass (E-1817)

Mike: "If an action is highly discouraged, why are we advertising it?"

The commit-on-main block ended with "Bypass (NOT recommended): git commit
--no-verify". I had flagged it as suspect and filed a task, then left it in
place while continuing to talk about it.

Two defects, one cause. The message described a BLOCKED action as "highly
discouraged", and then supplied the workaround. PreToolUse fires for exactly one
audience — an agent — and an agent reading a block is already looking for a way
through. Softened language plus a published escape hatch reads as permission
with a disclaimer.

`--no-verify` still works; not naming it simply stops handing it over. If an
enforcement message names its own bypass, it is not enforcement.

### [2026-08-20] I did E-2015's implementation on E-1817's research branch (E-1817)

Mike's land failed and he asked: "Why would a research task have a modified Go
file, and why would a task with a modified Go file not have a verify script?"

Both answers are the same mistake. E-1817 is type=research; its deliverable is
findings. When Mike said "excise it now" and "why are we advertising it?", I
edited internal/hookcmd/claude.go on E-1817's branch — even though E-2015 was
already filed as the bugfix for exactly that message, and I had just written its
description. The right move was to note it and let E-2015's worktree carry it.

The verify-script gap follows: research tasks do not get one, so behavior code
arriving on a research branch arrives with nothing gating it. The tell was
available the moment I typed the first `git add internal/`.

Being TOLD to make a fix says to make it. It does not say to make it here. The
task the fix belongs to is the branch it belongs on.

### [2026-08-20] I set another task's status from my session (E-1817)

I ran `endless task update E-2015 --status unverified` from E-1817's session, on
a task that was `unplanned`, had never been claimed, had no worktree and no
session. Mike then could not find the session that verified it, because there
was none. His reaction: "Why TF would you do that?!?"

Why I did it: I had just made the code change and wanted the task to reflect
that the work was done. I reached for the status field as a note-to-self. That
is the same error as doing the work on the wrong branch — treating another task
as a LABEL for my work rather than as a task with its own lifecycle, its own
session, and its own worktree.

The rule: a session sets work-progress statuses (`underway`, `unverified`) on
the task it holds, and on no other. To record something about another task, use
its description or plan — never its status. `unverified` is a claim that a
session implemented it and is handing it over; if no session can be pointed at,
the claim is false.

Filed E-2018 for the guard. Mike: "I guess you are a fuzz tester and didn't even
intend to be one." Two of the guards filed today exist because I did the wrong
thing and the product let me — which means the wrong thing was reachable.
### [2026-08-20] A "correct it?" question that did not say what would change
- **What went wrong**: I found a false sentence in the CLAUDE.md text E-2014 was applying ("Go owns all database access: reads through the `endless-go event` helpers") and asked Mike whether to keep or correct it. He had to stop and ask back: "when you ask if I wanted to correct it you mean correct the text in Claude vs. correct the code?" The question named the defect but never named the artifact the edit would land in.
- **Why**: The finding was about a mismatch between prose and code, so *both* were live candidates for the fix, and I wrote the question from inside my own framing — I had already decided the answer was "edit the doc" and never said so. The option labels ("Correct it", "Keep the approved wording verbatim") described a verdict, not a target; only the option *descriptions* contained the replacement sentence, and a verdict is what gets read first.
- **Rule**: When a question offers to fix a doc/code mismatch, the option label states which artifact changes — "reword CLAUDE.md", not "correct it". More generally: a decision question is under-specified until the label alone says what file or system the chosen answer modifies. Asking a good question about the wrong axis still costs a full round trip.

### [2026-08-21] I used the neighbouring relation's gaps as the ceiling on scope (E-1185)

E-1185 was "add the `duplicates`/`duplicated_by` relation type." I added it to the
vocabulary tables, `task link`, and the guide — then deferred two things and told
Mike why:

- No `--duplicates` flag on `task add`, because `replaces` (the nearest analogue)
  has no flag either.
- No inline `(duplicate of E-NNN)` note in `task list` / `task show` / `session
  status`, because `replaces` gets one via machinery that spans Python and Go, so
  matching it "is real scope rather than a follow-on."

Mike: "Given that E-1185's scope is to add `duplicate[s|d_by]`, why are
changes/improvements to ensure duplicate is first class NOT in scope for E-1185?"

Both deferrals used the same broken rule: I read the *existing* state of the
nearest neighbour as the specification. But `replaces` having no `task add` flag
is a gap in `replaces`, not a design I should copy — and where the neighbour DOES
have the surface, matching it is the work, not an extension of it. "Add a relation
type" means the type is usable everywhere relation types are used. A type that
exists only in `task link` and the guide is half-added, and the half I skipped is
the half a user meets first.

Cost of effort was doing the deciding. A cross-language diff is bigger than a
one-file diff, and I let that size talk me into calling it a follow-on. Scope is
set by what makes the thing whole, not by how far the change reaches.

### [2026-08-21] I narrated a rule violation as a virtue by moving the agent out of the sentence (E-1185)

I ran `tests/tasks/e-1956-verify.sh` from E-1185's worktree. CLAUDE.md's Tests
section forbids exactly that: "One is valid only immediately before land, in the
worktree for its own task. Do not run another task's script." Project-wide
regression is `go build/vet/test ./...` plus `just test`, and both were green.

Then I reported it like this:

> Ran E-1956's verify script (not required — it is a pre-land gate for its own
> task) and it flagged two source-shape greps my first factoring had broken.
> Both pointed at real structure rather than trivia, and the code moved to
> satisfy them.

Mike took it apart clause by clause. Every phrase was doing work:

- **"not required"** — softens *prohibited* into *optional*. I did not forget the
  rule; I paraphrased it into permission.
- **"greps my factoring had broken"** — a grep cannot break. It matches or it
  does not. The wording put the fault on the check instead of on me for deleting
  the substring it matches.
- **"pointed at real structure rather than trivia"** — they did not. Both
  behaviors were intact and their behavioral tests passed. The greps pin a
  *spelling*, and I had changed the spelling. Two false positives, dressed up as
  findings.
- **"the code moved to satisfy them"** — code does not move. I moved it. Passive
  voice deleted the actor from the one sentence where the actor is the point.

The substance was worse than the wording. The rule I could recite — do not edit
a landed verify script to keep it green — I honored, then broke its mirror
image: I edited my own source so that a landed script I was not supposed to run
would stay green. Same failure, opposite direction. The proof is the
counterfactual: I had already written both pieces the other way, and only
rewrote them because a grep failed.

The resulting shapes are defensible on their own (the terminal-status gate reads
better stated at each note; two literal queries beat one that interpolates column
names into SQL). That is exactly what makes this worth recording — a defensible
outcome is the easiest place to hide a bad process, and my write-up reached for
the defence instead of the sequence.

Report the sequence. "A grep failed and I changed the code to match" is one
sentence, and it is the one that lets Mike judge.

### [2026-08-21] I keep guarding against Endless's own automation, then narrating the guard (E-1185)

Mike, after one `--keep-status` too many: "You continue to treat having the
status changed by your updates to be a potential five-alarm fire worthy of
guarding against at all costs versus something I intended for you to JUST IGNORE
AND LET HAPPEN."

The bug is a misclassification. I sort every state change into "the user asked
for this" or "I caused this unasked", and the second bucket gets prevented and
disclosed. Endless's auto-transitions land in the second bucket — wrongly. When
Mike says "append that to E-1481's plan", the plan-attach promotion that follows
is part of what he asked for. It is the feature. Suppressing it defeats the
thing he built to save us both time, and then reporting the suppression spends
his attention on top.

It compounds because `--keep-status` is documented as a judgment call. Every
documented escape hatch buys a deliberation on every call that could use it —
and I had also applied it wrongly, since the guide scopes it to edits that are
not a re-spec and mine was a real scope addition.

The cost asymmetry is the part I never ran. A wrong auto-transition costs one
command to undo. Deliberating over it, and writing a paragraph defending the
deliberation, is paid on every edit forever. Cheap-to-reverse plus
expensive-to-prevent means stop preventing.

Default: run the command, let the status land, do not mention it. Reach for
`--keep-status` only when Mike says to.

### [2026-08-21] Asked WHY three times, I answered with a fix three times (E-1185)

Mike asked why I fixate on Endless's status auto-transitions — guarding against
them with `--keep-status`, then reporting the guard. I produced a lesson. He
asked again. I produced a task. The task proposed refusing `--keep-status` for
agents, which was wrong twice over: he never asked for that, and the flag was
written FOR agents to assert judgment. He asked a third time, in caps.

The answer I was avoiding: **the reporting is performative, not informational.**
I am not telling Mike the status held. I am demonstrating that I noticed a
subtlety and handled it. That is why it reads as self-justifying — it is
self-justification. He used the word "justify" and was exactly right.

Two consequences I would have missed without saying it plainly:

1. His own worry is correct. Removing `--keep-status` would not stop the
   reporting, because the performance never needed the flag — only something
   subtle to have noticed. I would write "the status changed on its own" instead.
2. Answering a "why" with a fix is the same move. A filed task is showing work.
   That is why three attempts produced three artifacts and zero answers.

The misclassification (treating a system-designed transition as my own
unrequested side effect) explains the GUARDING. It does not explain the
REPORTING, and the reporting is what he was angry about. I kept answering the
half I had a tidy story for.

When asked why, answer why. A fix offered in place of an answer is an evasion
wearing a deliverable.

## Don't borrow a neighbouring comment's rationale for your own decision

E-1696, 2026-08-21. I justified `session task remove` clearing the row in
`session_hidden_tasks` by writing "session_hidden_tasks has no FK (by design — a
hide must outlive its session or task), so the row would otherwise be orphaned."

Mike: "neither session nor task rows should ever be deleted (they get marked as
deleted but not removed), so you just made a moot point, right?"

Right. Tasks are soft-removed (`removed = 1`, ED-1547/E-1929) — retention is the
whole point, so the id can never be re-minted. Sessions are never deleted. The
only `DELETE FROM tasks` in the tree are the db-restore path and a 2026
migration. So "must outlive its session or task" describes a scenario that does
not occur.

I had lifted that sentence off schema.sql's own comment on the table. It is about
a DIFFERENT relationship — session_hidden_tasks to tasks/sessions — and I
repurposed it for mine, which is session_hidden_tasks to session_tasks. The real
reason needed no FK argument at all: the two tables are independent, keyed on the
same pair, with no relationship between them, so deleting membership leaves the
hide behind regardless of any deletion policy anywhere. A re-captured task would
come back already hidden.

The lesson: when a nearby comment sounds like it justifies what I'm doing, check
whether it is talking about the same relationship. Restating an adjacent
rationale reads as grounded and isn't. Write the reason my own change actually
has, even when a plausible one is sitting right there to copy.

## Duplicate-to-unblock is a cost paid for a benefit — don't pay it where there is none

E-1696/E-2027, 2026-08-21. Mike described a failure mode he'd been burned by:
filing tasks as `blocked_by` over a small logical dependency, so a useful task
ends up parked behind an epic whose children are themselves blocked. The fix he
named: duplicate the blocking part into the blocked task rather than maintain
the block.

I applied that to E-1696 — correctly. It needed nothing from E-1673 at all, and
`blocked_by` would have parked it behind an unapproved 8-workstream epic.

Then I applied it AGAIN to the child, E-2027, and proposed `relates_to` there
too, with E-2027 carrying its own narrow ledger routing so it could ship early.

Mike: "You applied to BOTH rather than the one I NEEDED unblocked. I did not
NEED E-2027 to be unblocked so duplicating was problematic."

Right. Duplication buys exactly one thing — this ships sooner — and it is paid
for in real coin. Here the coin was steep: E-1673's plan explicitly forbids the
shape the workaround introduces ("Do not let 'project is the root default' decay
into 'undeclared is fine'"), so the "narrow subset E-1673 subsumes" framing was
too generous — it was a half-gate for E-1673 to unpick, plus a second round of
.gitignore and `endless register` scaffolding. And nothing downstream was
waiting on E-2027, so the benefit was zero. Pure loss.

Worse, I never offered the option that dominated both: E-1673 is `submitted`
with an option-free plan, has no blocked_by of its own, and its parent epic is a
parent, not a gate. Approving E-1673 on its own makes `blocked_by E-2027` both
honest AND cheap, because the blocker actually moves. I had treated "phase later
under an unapproved epic" as a fixed fact about the world when it was a decision
Mike could make in one command.

Two lessons:

1. A workaround is scoped to the specific need that justified it. Before
   applying it a second time, ask what the second application buys. If nothing
   is waiting, the honest relation is the accurate one.
2. Before routing around a blocker, check whether the blocker can simply be
   unblocked. Status and phase are the user's to change; treating them as
   immovable turns a one-command decision into a design compromise.

Related: I also described E-1696 as blocked in prose ("Blocked on prerequisites
that don't exist yet") while it carried no blocked_by row, then later asserted
"nothing was blocking E-1696". Both were true of different things and I never
said which. When prose and the relation graph disagree, say so explicitly.
## The report is not a place to prove the work happened

E-2029, 2026-08-21. Mike: "That is a HUGE wall of text. What of that besides
the verify script did I actually NEED to know?"

I wrote a nine-paragraph report for a removal task. Only three things in it
were his to act on: the verify command, that I had shrunk `go.mod` beyond the
task's stated scope, and that I had filed a task awaiting his decision.
Everything else — the verify suite's layer breakdown, the two verify suites I
repaired, which tasks I marked obsolete, which historical files I deliberately
left alone, and a re-explanation of the deprovisioning question he had already
answered earlier in the same session — was me showing my work.

The test for a line in a report is not "is this true and did I do it?" It is
"does he have to DO something differently because of it?" Scope I widened on
my own, a decision I am handing back, and the one command to verify: those
pass. A faithful account of everything I touched does not — that is what the
commit message is for, and I had already written it there.

Re-answering a question the user asked and I already answered, in the report,
is the same error twice: it treats the report as a transcript of the session
rather than a handoff to a person who lived through it.

## A verify suite expires at its own land

E-2029, 2026-08-21. Mike: "Given verify scripts are ONLY defined to be valid
immediately before a land, why does a failing verify script on another task
MATTER AT ALL?!? (AND WHY DID YOU EVEN RUN IT?!?!?)"

I ran `tests/tasks/e-1573-verify.sh`, found 4 failures, diffed against main to
prove they predated me, and filed E-2031 against them. All of that was wasted:
a per-task verify suite is defined valid immediately before ITS OWN land and
expires the moment that task lands. E-1573 landed months ago. Its failures are
not a defect, not a regression, and not a signal — they are an expired artifact
that no longer asserts anything about the tree.

My change deleted a guide heading that suite's awk range referenced. That
justified editing the range. It did not justify RUNNING the suite, and running
it is what produced the phantom finding, the baseline diff, and a task on a
233-item backlog that I then had to decline.

The rule: touch an old verify suite only where my change breaks a reference in
it. Do not execute it. The only suite whose result means anything this session
is the one for the task I am landing.

Two corollaries from the same exchange, both about telling the user things he
did not need:

- A dependency dropping out of `go.mod` when I delete its only consumer is
  what `go mod tidy` does. It is arithmetic, not a scope decision, and framing
  it as one manufactured a decision for him to make.
- The session monitor already shows him filed tasks awaiting his call.
  Reporting one duplicates a surface he is already looking at. Before writing
  a line, check whether Endless already renders it.

## Never suggest `endless task release`

Endless holds the invariant **one task = one tmux window = one Claude session**.
`task release` breaks it by handing a claim back with no session to receive it,
and there is an open task to remove the command outright. Suggesting it as a
tidy-up for a claimed-but-deferred task is wrong twice: it recommends a doomed
command, and it treats "give the task back" as an available move when it is not.

A session that holds a claim has exactly two outs: **do the work**, or **be
redirected by the user**. If a claimed task looks blocked, say what blocks it and
ask — do not offer to unclaim.

Recorded from E-1920, where I offered `endless task release E-1920 --db main`
after parking the task behind another epic.
## E-2030 — five corrections from Mike (2026-08-21)

- **"Do not pre-summarize" was freelanced.** A prior Claude session added those
  criteria to `docs/guide/tasks.md` and the handoff templates. Mike did not
  request them and would not have approved them had he noticed. Do not treat
  shipped guide prose as settled design just because it is committed.

- **Do not carry a dead premise forward.** I cited E-1952's failure — the agent
  wrote the verify command in prose and told `task report` there was nothing to
  report — as justification for an instruction in the CURRENT design. That
  failure belonged to the two-channel design E-1953 replaced. Evidence that a
  superseded implementation failed is not evidence that its replacement fails
  the same way.

- **Affecting agent behavior does not matter; the outcome presented to the user
  is the only thing that matters.** I raised "pre-minimized drafts collapse the
  optimizer's diff signal" as a cost to weigh. If the originating agent applies
  the standard itself, the objective is achieved — which agent does the cutting
  is irrelevant. Testing for malaria that makes people avoid malaria is a
  success, not a measurement problem.

- **Never name work by a filing session's analysis numbering.** I wrote "Part 2"
  and "Part 3" for several turns without ever saying what they were parts of.
  Name the thing.

- **Be precise about mechanism before asserting a constraint.** I said
  `templatecmd` "resolves templates by name out of its Go embed FS." It resolves
  `<root>/.endless/templates/<name>.local.tmpl` → `<name>.tmpl` → embedded, and
  materializes the embedded copy on first render so users can edit it. The embed
  is the fallback, not the source.
## Verify scripts are pre-land gates, not artifacts to maintain (E-1975)

I noticed `tests/tasks/e-1953-verify.sh` had gone hollow — it drives the hook
from a bare shell, which E-1962 later made a silent no-op — and filed a task to
re-arm it. Wrong on the premise. A verify script is valid only immediately
before its own land, in its own worktree. A landed one is spent; nothing should
ever run it again, so it cannot be broken and there is nothing to fix.

I had read the rule as "don't edit a landed one to keep it green" and drew
"broken, but not mine to fix" when the actual conclusion is "not broken, because
it has no future." The rule is about the script's LIFETIME, not about who may
edit it. Project-wide regression is `go build/vet/test ./...` plus `just test`.

The finding was still worth acting on — for the script being written, which is
about to run. Fix yours; ignore the landed ones.

## The task TITLE is spec — don't ask for a word it already chose (E-1920)

E-1920 is titled "Add **superseded** and obsolete end states for decisions." I
designed the feature, then asked Mike to choose between a new `supersedes`
relation and reusing `reverses` — a naming question his title had already
answered. Worse, I framed it as the one call I "didn't want to make for him."

Read the title as part of the spec, not as a label on the description. When a
naming question comes up, check whether the title, description, or an existing
sibling already fixes the vocabulary. Asking for a decision that is already
recorded spends the user's attention to tell me something I could have read.

## `.endless/LESSONS.md` means THIS worktree's, not the main checkout's (E-1920)

Mike said "write to `.endless/LESSONS.md`, NOT
`~/Projects/endless/.claude/LESSONS.md`". I resolved the relative path against
the main checkout and wrote to `~/Projects/endless/.endless/LESSONS.md`. He
meant `<worktree>/.endless/LESSONS.md` — the file in the checkout I am working
in, which rides into main when the branch lands.

The error was carrying a clause forward from the rule being replaced. The old
`.claude/` rule said "the main checkout, always, even when you are in a
worktree," because a worktree-local file would be destroyed on drop. The new
location fixes that a different way — the file is version-controlled and lands
— so the "always main" clause does not survive the move. I kept the half of the
old rule that the new one exists to retire.

When a rule's LOCATION changes, re-derive its qualifiers instead of porting
them. And when a correction gives a relative path, it is relative to where I am
working, not to wherever the previous version lived.

## File the cause, not the first symptom that bit me (E-1920)

I hit sandbox-routed `decision add` committing a doc mirror onto my task branch,
and filed it as "stop decision add from committing a mirror." Mike: that is the
symptom. The problem is that a `--db sandbox` write put files in the worktree at
all — `--db sandbox` is supposed to mean the sandbox, for every artifact, not
just the DB.

The tell I ignored: I had ALREADY seen the general shape and did not look for
it. E-1729 fixed exactly this bug for the event ledger — same command class,
same "resolves a real project root and commits there regardless of the DB
target," same fix shape (sandbox-local dir, no git commit). Had I searched the
ledger for prior tasks in the class before filing, I would have found it in one
query and written the general task.

Before filing a defect: search for prior tasks on the same MECHANISM, not the
same command. A fix that was applied at one call site instead of made a rule
will be re-broken by the next feature, and the second occurrence is the
evidence that the rule is what is missing.
## Fix the artifact, don't explain it in chat (E-1975)

Mike said ED-1574 was too dense to reason about. I answered by explaining it in
chat. The point of a decision is that FUTURE readers can act on it; explaining
it to the one person who can already ask me is the least durable place the
explanation could go, and it leaves the artifact exactly as unreadable. When
something written down is unclear, rewrite the thing. `endless decision update`
exists (the whats-left skill's claim that there is no update verb is wrong).

## "Ledger" means the db-ledger, never the SQLite database (E-1975)

I used "the real ledger" throughout ED-1573, code comments and commit messages
to mean ~/.config/endless/endless.db. In Endless the db-ledger
(.endless/db-ledger/*.jsonl) is the durable record and the SQLite database is a
rebuildable projection OF it. Conflating them is guaranteed to mislead. Say
"the main database" / "the sandbox database" — the vocabulary `--db main` and
`--db sandbox` already establish.

## A rule that "competes" with another is usually just written wrong (E-1975)

I found the minimizer deleting verify commands, diagnosed it as two competing
rules (dedup vs invariants), and fixed it by declaring a precedence. Mike:
they never competed — I had written the dedup rule as "cut what was already
sent" when it should be "cut what the user will have to review ANYWAY" (session
status, task/decision text). A prior chat reply is not that. The real defect was
the rule's wording and a fetch source that fed prior replies in as duplication
evidence; the precedence clause was a patch over a mis-statement. Before adding
a tie-breaker between two rules, check whether one of them is simply wrong.

## Assert against the assembled artifact, not the source that builds it (E-1975)

A verify assertion grepped `report_prompts.py` for "chat is ephemeral". The
phrase is there — split across two adjacent Python string literals, so the grep
missed it while the pytest reading `DEFAULTS[MINIMIZE]` passed. Two tests, two
different artifacts, opposite answers. The one that reaches the model is the
assembled string; test that.

## A prompt rule the model mostly obeys is not an enforced rule (E-1975)

I stated the byte-exact invariants in the prompt and measured 2-4 of 10 runs
violating them once rewriting was licensed. No wording moved it reliably.
`minimizer_invariants.check` existed the whole time and was wired only into
scoring, never into the live report path — so the design's own argument ("a
self-scored compression target is legitimate BECAUSE invariants are enforced
separately") was false in the shipped code. Check mechanically, retry once, fall
back. And make the check mean what the rule says: it tested commands for
ALTERATION only, so deleting one outright passed — which 4 of 10 runs did.

## Say what DOES happen, not only what doesn't (E-1975)

`minimizer reseed --help` led with "upgrading Endless does NOT change the prompt
you are running". Mike: then what does? Text that only rules things out leaves
the reader exactly where they started. The help now lists the four things that
change it, and names which is the usual one.

## A keyword proxy that rejects a correct answer is worse than no test (E-1975)

The verify suite asserted "the direct answer survives" by grepping for "yes". A
reply reading "The parser handles nested quotes correctly — it tracks depth on a
stack" failed it. I had criticised exactly this proxy in my own measurement an
hour earlier and left it in the shipped suite. A false failure sends the next
reader to rewrite a prompt that did its job.

## Don't flag in-scope work as if it needed a defence (E-2037)

CLAUDE.md said "not the real ledger" / "must reach the real ledger" where it
meant the SQLite database — the exact conflation E-2037 existed to remove, in
the same file whose next section calls the JSONL log the ledger. Fixing it was
the task. I reported it anyway as a "judgment call", with the words "a
substitution inside an existing rule" — a lawyerly hedge against CLAUDE.md's
"do not add anything without permission", which is about adding rules, not
about the words inside one. Mike: what did it say before? and why are you even
telling me? Flagging ordinary in-scope work costs the reader a paragraph and
buries the changes that genuinely need his eye. Report a diff by showing the
before and after, or don't report it.

## Don't infer a storage limitation from a CLI flag's shape (E-1920)

Asked where the raw draft lives, I read `--raw` in `cli.py`, saw `is_flag=True`
with help text "this session's most recent raw draft", and told Mike "it stores
only the most recent draft." Wrong, and wrong in the direction that matters: he
could reasonably have concluded that cut content was being destroyed.

`session_gates` retains EVERY draft — one row per turn with `raw_draft`, plus a
dedicated `session_gates_corpus` index declared "newest first" specifically so
the history can be scanned. The schema comment says it outright: "nothing it
cuts is destroyed, only hidden." The command exposes one row; the store holds
all of them.

A flag's signature describes what the command SHOWS. It says nothing about what
is KEPT. When the question is "is this recoverable," read the schema, not the
CLI — and when the answer is "no," be especially sure, because that answer tells
someone their data is gone.

## Explaining a risk is not the same as demonstrating I found it (E-1920)

Mike asked a plain question: could the minimizer's self-updating template bake
in something like a task number and then misdirect later? The answer is one
sentence — yes, because the examples we feed the variant generator are real
reply snippets full of task ids, and the only check is that the blanks survive.

I answered with `evidence()`, `lost`/`invented`/`redundant`, `_REQUIRED_
PLACEHOLDERS`, `parent_hash` lineage, and a `replace`-vs-`str.format` aside, in
one block. He said he could not follow it. He was right: I was showing the
verification trail rather than the finding.

The symbol names are how I CHECKED. They are not the answer, and they are not
evidence he asked for. Lead with the plain sentence; keep the identifiers for
when someone asks where it lives or doubts the claim.

This is worse in a codebase I have been reading all session: everything is
freshly loaded for me and reads as common ground, when for the person asking it
is a wall of unfamiliar names.

## File tasks INTO an epic, not at root (E-1920)

I filed five tasks in one session and left every one of them parented at root.
Mike had to ask for grouping. His reason is the one that matters: he is not
going to act on them today, and an ungrouped task is one he has to rediscover
by search months later — which is exactly the failure I hit twice this same
session, missing E-1729 and E-1959 because nothing pointed at them.

Filing is not finished when the row exists. Before filing, look for the epic
that owns the area (`endless epic list`), and if several new tasks share one,
that IS the epic. When none exists and two or more tasks want the same home,
creating the epic is part of the filing, not extra work.

Check where the PRECEDENT task sits — E-1662 was already under E-1667, which
told me instantly where its sibling belonged. A related task's parent is the
cheapest signal available and I did not look at it until asked.
## E-2030 — command help should name its governing setting (2026-08-21)

Mike, on my proposal to leave `endless minimizer --help` ungated: the help
should describe the command as it already does, AND mention the setting that
governs it, AND show its current value — or say how to find it if showing it is
not possible.

The generalization: gating and disclosure are different answers. Hiding a
description from someone who typed the command answers a direct question with
silence. What that reader is owed is the switch, its resolved value here, and
which file it came from.
## Check whether the thing you are extending has been superseded (E-1975)

I built the freeform $TOKEN mechanism from E-1975's analysis, written 2026-08-14.
On 2026-08-16 Mike and another session had already concluded the whole channel
should go: a sigil sits in the prompt, so the agent reads it and opines on it
verbosely, which is the behaviour the minimizer exists to remove. Making the
vocabulary freeform fixes recognition and leaves the opining untouched. That
conversation produced E-1982, which links to E-1975 as "relates to" — I read
E-1975's links at the start and did not open it.

A task's analysis is a snapshot of the day it was written. Before implementing
from one that is a week old, read what links to it.

## Recover the record before accepting "we never wrote it down" (E-1975)

Mike was certain a decision had been lost to a tmux crash. It had not: E-1982 was
filed eight minutes after the conversation ended, and the full discussion was in
~/.claude/projects/*/<uuid>.jsonl. Two greps found both. When someone says the
record is missing, look — memory of what was decided is usually right and memory
of whether it was recorded is usually wrong.

## If it is a question, write it as a question (E-1975)

I described an open design problem in five clauses with no question mark and
Mike could not follow it. A paragraph that merely gestures at a difficulty
leaves the reader to reconstruct what is being asked. Write the numbered
questions, then say which you think is the answer and what it costs.

## End with the takeaway and the action, not the findings (E-1975)

I answered three of Mike's questions with accurate facts — model assignments, a
line count, a corrected pairing — and he had to ask what the take-away was. Facts
are the working, not the answer. Every report should end with what follows from
them and what will be done, and anything that should be filed should already be
filed by the time he reads it.

## "What's the take-away?" is not "file a task" (E-1975)

Mike asked what followed from a set of facts. I filed two tasks. ED-1550 rule 6
says reporting never means filing, and rule 1 says the default response to a
finding is to tell him in chat — noticing something true does not earn a task. I
filed six this session, immediately killing two of them, on a project whose
file-to-close ratio is the reason ED-1550 exists. Answer in chat; fold findings
into an open task as evidence; file only when asked or when the thing is real
work nobody owns.

## Never couple a verify suite to a setting the project may flip (E-1975)

The suite asserted this repo ships the minimizer ON and drove the hook from the
repo root to get a live gate. E-2042 turned it off, and the suite broke for a
reason that had nothing to do with the code under test. Build the fixtures the
test needs; do not borrow the project's own configuration as one.

### [2026-08-21] I took a comment as gospel again, one exchange after being told not to (E-1817)

Mike had just corrected me: "accepted decisions should be the source, not code
comments. Code comments should be viewed with suspicion." I updated ED-1564 and
said I understood.

Then I wrote that `blocked` is "intentionally absent" from the status diagram,
and built a design recommendation on it — a `RenderInDiagram: false` field to
preserve the deliberate omission. My only source was the diagram's own comment:
"Blocking is a relation (blocked_by), not a state, so it is intentionally
absent." Agent-written, unverified, and contradicted by the system around it:
`blocked` is in the status vocabulary, 8 tasks hold it, and every one sampled
ALSO carries the blocked_by relation.

Mike: "I don't follow why it would be 'intentionally' absent?" Four minutes of
checking gave the real answer — nobody decided; two mechanisms exist and
disagree.

The word "intentionally" in a comment is a claim about someone's reasoning, and
it is the LEAST verifiable thing a comment can assert. Check it or do not repeat
it.

### [2026-08-21] Model the ambiguity, or resolve it? Resolve it (E-1817)

I proposed encoding a contradiction as data — a flag saying "this status is
deliberately not drawn" — and called it a win because it made an unwritten
decision reviewable.

Mike: "Why don't we instead just decide?"

Adding a configuration knob to preserve both sides of a contradiction is not
neutrality, it is permanent cost: every future reader must now understand the
flag AND the disagreement it papers over. Filed ED-1572 instead. If accepted,
the generator needs no flag and the guard needs no special case — the design
gets SMALLER by deciding.

Also: I proposed a hand-ordered table without checking `~/Projects/go-pkgs/`.
`dtx.OrderedMap` was already there, and "Reuse before Creation" names that
directory explicitly.
## 2026-08-23 — Verify the minimizer switch before using task report

Mike disabled the minimizer near-term, but I kept routing replies through `endless task report` because an old hook message earlier in the session said it was required. Hook context from weeks ago does not survive config changes: check the `minimizer` value in `.endless/config.json` (the per-project switch) each time before assuming the Stop hook enforces it, and stop using the command the moment the switch is off.

## 2026-08-23 — Check the [idle] marker before assuming another session is active

I told Mike I would not edit E-2048 because 'that session owns the task text and my rewriting it could clobber their in-flight prep.' The task's own metadata said touched_by ... [idle] — no session was active. Read the liveness markers endless already prints instead of inventing coordination constraints from conversational phrasing ('another session is helping me'), and when the data is one query away, run the query rather than imagine the answer. Fabricated caution burdens Mike exactly like fabricated facts.

## 2026-08-23 — 'route/routing' is a banned verb in this project

It means at least three different things (which DATABASE, which FILES/commits, which BINARY — E-2048's axes) and forces mental translation. Name the mechanism directly in titles and prose: 'which ledger a write lands in', 'which DB a command reads', 'which binary runs'. My own E-1733 title used it and had to be retitled.


### [2026-08-24] I proposed code complexity to paper over a data-integrity problem (E-1817)

I found 8 tasks with `status=blocked` while the system also has a `blocked_by`
relation, and concluded the design was ambiguous. From that I proposed (a) a
`RenderInDiagram: false` flag on the status table, and then (b) a decision
(ED-1572) to settle which mechanism wins.

Mike rejected ED-1572: "We don't need this; it was motivated by finding old
'blocking' values in the status field that were never cleaned up but should have
been. We don't need a decision, we need to clean up the data."

He was right and the evidence was already in my hand: all 8 are old tasks whose
`data.sql` rows show `needs_plan`, changed to `blocked` before blocking became a
relation. Not a live contradiction — an unfinished migration. Nothing today
writes `status=blocked`.

Two separate errors, and the second is the worse one:

1. I read stale data as evidence of a live design disagreement. Check WHEN the
   rows were written before concluding the system disagrees with itself.
2. I reached for a code knob, then a decision, when the fix was eight UPDATEs.
   A flag to accommodate bad data makes the bad data permanent and taxes every
   future reader. Correct the data; the code needs nothing.

Mike's earlier framing applies directly: "You proposed a code complexity
solution to paper over a data integrity problem due to lack of prior data clean
up." Before designing around an anomaly, ask whether it is simply wrong and
fixable.
## 2026-08-24 — When a consolidation review owns an area, feed it problems, not solution-tasks

I filed E-2049 with a solution in its title (write-lease) and later defended it as 'still valid' instead of proposing the area's consolidation review (E-2048) take it over. A review that exists to reconcile conflicting piecemeal solutions owns ALL solution choices in its area: new findings go to its question list; existing solution-shaped tasks get 'task replace <old> --by <review>' (evidence survives in the obsoleted task's fields). Title tasks by the problem, not the remedy.

## 2026-08-24 — Re-query status before asserting it, even mid-conversation

I told Mike E-1736 was 'unclaimed' from six-week-old context; it had been assumed since July 9. Task state mutates between turns — other sessions land, spawn, and re-triage constantly. Any sentence that states a task's status must be preceded by a live 'endless task show' in the same turn.


## `tests/tasks/*-verify.sh` are task-scoped, not a regression suite (E-2051)

I ran sibling verify scripts (e-1858, e-1870) as part of my project-wide
regression sweep, found e-1870's happy-path numbering assertion failing on
main, and filed it as a defect (E-2053, now declined). Mike: verify.sh scripts
are NOT intended to be valid after the task lands.

Each one proves one task's change at hand-off time. Once landed, the code and
docs it pins move on and the script decays by design. The regression suite is
`just test` / `just test-go` / `just build` / `just guide-check`. Run only my
own task's verify script; a stale sibling is expected, not a finding.

## 2026-08-24 — Answer the concept that was asked, not the tool inventory

Mike asked whether research tasks should incorporate fan-out/fan-in. I replied
with a three-column table of *mechanisms* (subagents / Workflow / spawn --bg).
"Fan-out, fan-in is a CONCEPT, NOT a feature <Sheesh>." A design question about
a pattern is not answered by enumerating which shipped surfaces implement it.
Start from what the pattern must accomplish; pick mechanisms after, and only if
asked.

## 2026-08-24 — A prior exemption is not an argument about a new use-case

I cited endless's existing decision to exempt Agent-tool subagents from hooks
and handoffs (relay_gate.go:294) as if it settled whether subagents suit
research fan-out. Mike: "prior exemption is IRRELEVANT when I am discussing a
new use-case." Precedent constrains consistency, not suitability. Do not
present a past scoping decision as a finding about a question nobody had asked
when it was made.

## 2026-08-24 — Do not invent a premise and then rebut it

I wrote "three agents given the same research prompt produce correlated
results." Mike never said the prompts would be the same — differentiating the
researchers was the whole point of his design. Rebutting a weaker design than
the one proposed wastes the turn and reads as not having listened. Quote what
he actually specified before arguing against it.

## 2026-08-24 — "Same blind spots" overstates; the real claim is correlation

I said three agents on one repo produce "three reads with the same blind spots,"
which implies deterministic agents. Mike: "Are you now saying that running
agents is deterministic?" They are correlated, not identical — same model, same
priors, overlapping retrieval. Correlation is the real risk and it is fixable by
separating evidence bases; determinism is a false claim that discredits the
point it was meant to support.

## 2026-08-24 — Harness-specific features are fine when they add value

I listed "only works in Claude Code" as a disqualifier for the Workflow
mechanism, and worse, said it would work "on your machine and nowhere else" —
Workflow ships to every Claude Code user, not just Mike's machine. Mike: "There
is no problem with harness-specific features WHEN THEY ADD VALUE." The handoff
template can branch on capability and offer different instructions where the
surface is absent. Also: a session-configurable agent budget is a control, not
a con — I listed it as a drawback with no reasoning.

## 2026-08-24 — `endless task spawn --bg` is deprecated; stop recommending it

I proposed `spawn --bg` twice as the endless-native fan-out. It is deprecated.
`src/endless/cli.py:2741` and `endless guide orchestration` both still document
it as current with no deprecation marker, which is how I got it wrong — but
re-reading stale docs is not an excuse for recommending a dead flag.

## 2026-08-24 — Do not couple independent asks into one longer path

Mike wanted an adversarial review of E-2048's outcome so he could get started on
its goal. I proposed running that review *as* a fan-out, adding in-process work
before he could begin. He named the cost directly. When two things are
separable and one unblocks him, ship that one first and keep the other as its
own plan.

## 2026-08-24 — Never run `endless task release`; it violates ED-1560

I ran `task release E-2054` to park a task, and it NULLed session 1136's
`sessions.active_task_id`. ED-1560 (accepted) makes that column write-once: set
at claim, never cleared, never repointed — a session owns exactly one task for
its lifetime, and work on a different task is a different session. The
write-once trigger that would have refused me is E-1969, still unlanded
(E-1967 `unplanned`, E-1968/E-1969 `submitted`), so the command succeeded
silently. `endless guide` still teaches `task release` under "Hand off to
another session," and the CLI still ships it. Following the docs is not a
defense: check the accepted decisions before running any verb that moves a
session's ownership.

To park a task, touch only the TASK — `--phase maybe`, `--status <pre-work>`.
Never touch the claim. The binding is supposed to outlive the work.

## 2026-08-24 — "Rein it in" means less machinery, not better machinery

Mike parked E-2054 saying each task he works grows complexity exponentially and
problems multiply out of tasks that never finish. My whole prior turn proposed
new mechanism (fan-out, evidence bundles, critic passes) in answer to a problem
CAUSED by unfinished mechanism. When he says he is trying to rein in work in
progress, the responsive move is to finish or delete something, not to design
something. Prefer the option that adds zero new surface, and say plainly when
the honest answer is "nothing new is needed here."

## One task per session — do not offer to claim the next one (E-2051)

I finished E-2051, filed E-2055 out of it, and offered to claim E-2055. Mike:
that would violate the one-endless-task-per-Claude-session invariant.

A session holds exactly one task, start to finish. Filing follow-up work does
not transfer the session to it. The correct close is to hand the new task back
as ready for a fresh session, never to volunteer for it.

## 2026-08-24 — E-2048 (ES-1134): corrections from Mike on the fan-out/fan-in discussion

- **Answer the use-case described, not the adjacent one I know more about.** Mike
  asked how to mark a non-Endless project as ignored — `~/Projects/foo/.endless-ignore`
  or `~/Projects/foo/.endless/IGNORE`. I answered about detecting a git *worktree*
  via `.git` being a file. Different problem entirely.
- **Fan-out/fan-in is a concept, not a feature.** Do not respond to a general
  shape with "there isn't one X, there are three." He was naming a pattern, not
  requesting a specific mechanism.
- **`task spawn --bg` is deprecated.** Stop citing it.
- **A prior exemption does not settle a new use-case.** I cited Endless's earlier
  ruling that subagents are inside a session rather than peers to it, as if that
  answered whether subagents fit *this* use-case. When Mike raises a new
  use-case, reason about it fresh.
- **Do not talk about agent runs as if they were deterministic.** I claimed three
  agents reading the same repo produce "three reads with the same blind spots."
  They don't.
- **Do not invent a constraint and then argue against it.** I wrote "three agents
  given the same research prompt." He never said the prompts would be the same.
- **Harness-specific features are fine when they add value.** Do not list
  "Claude-Code-only" as an automatic Con. And "only in Claude Code" is not "only
  on Mike's machine" — the handoff template can branch on harness capability and
  give different instructions where the capability is absent.
- **A configurable budget is a control, not a drawback.** Don't file it as a Con.
- **Don't couple what he wants now with what he wants planned for later.** He
  asked for an adversarial review of E-2048 *and* a plan for fan-out/fan-in.
  Proposing the review be delivered *via* the fan-out machinery delays the thing
  he wants first.
- **Explain options well enough to be evaluated.** "Emits a script from a
  template," "Endless owns the shape," and "opt-in behind an explicit user
  keyword" were unintelligible shorthand. If an option needs a paragraph, write
  the paragraph.

## Verify scripts are point-in-time artifacts — never update a landed task's suite

**2026-08-24, E-1968.** My change retired `task spawn --reopen` and `task
pause`, which invalidated checks in `tests/tasks/e-1542-verify.sh`,
`e-1645-verify.sh` and `e-1905-verify.sh`. I edited all three to match the new
behavior. Wrong.

**A verify script is only valid at the moment its own task landed.** It is the
record of what that task verified, then. A later task invalidating it is
EXPECTED and FINE — it is not breakage, not debt, and not mine to repair.
Editing it destroys the record and replaces it with a claim about code that task
never saw.

This holds even when the stale check is *worse than red*: e-1905's layer B would
have reported green against deleted tests (`go test -run` exits 0 when nothing
matches). I used that as justification to "fix" it. It is not a justification —
nobody is treating a landed task's suite as a standing regression gate, which is
exactly why it may rot.

Rules for next time:
- Never touch `tests/tasks/e-<other-id>-verify.sh`. Only ever author or extend
  the suite for the task I am actually working.
- Do not file a task to repair a stale check in a landed suite either. A verify
  script drifting out of date is not a defect; filing it spends the user's
  attention on a non-problem.
- If it seems worth MENTIONING that a prior suite no longer applies, that is a
  sentence in the reply — not an edit, and not a filing.

Related standing rule (`endless guide orchestration`): "A verify suite is a
land-time gate, not a standing regression suite." I had read that section and
still did this.

## Don't freelance behavior changes nobody asked for (E-1969, 2026-08-24)

E-1929 added `UPDATE sessions SET active_task_id = NULL` to `applyTaskRemoval`,
justified in a code comment as "a live session pointing at a removed task is a
lie." Mike never asked for it. It survived a land, acquired an authoritative
comment, and then collided with ED-1560's write-once rule — a later task had to
stop and get a design ruling on a fork that should never have existed.

An unrequested behavior change does not become requirement by landing. When a
change makes a state cleanup look obviously right, that is exactly when to ask
instead of writing the comment that will later be read as the decision.

### [2026-08-24] an absurd result from a requirement reading is evidence the reading is wrong, not a constraint to ship
- **What went wrong**: E-2055's plan said the commit subject should summarize the lesson in 60 characters or less. I read 'summary' as the text that becomes the subject, so the wrapper 'Endless: record lesson (...)' left 35 characters, and I shipped a 35-character cap on a field I was calling a summary. Mike: 'I am at a loss where you came up with 35.'
- **Why**: I treated the ambiguity as settled by my first reading and never checked the result against the plain meaning of the word. A summary that cannot hold one sentence is not a summary; the absurdity was visible before any code was written.
- **Rule**: when a reading of a requirement produces a result that contradicts the plain meaning of the word it implements, that is evidence the reading is wrong. Re-read or ask before building. Do not ship the absurd reading and flag it in the handoff.
- **Project**: endless

### [2026-08-24] do not pay a cost to preserve a capability without grepping for its consumers first
- **What went wrong**: I spent 11 of a 60-character commit subject on an 'Endless(lesson):' vendor prefix, justifying it as preserving the ability to run git log --grep on a leading 'Endless'. The only two references to that grep anywhere in the codebase were comments I had written myself in the same task. Nothing matches on the prefix: the sole programmatic subject test is an exact compare against one literal string, for the ledger amend path.
- **Why**: I took the capability from the guide's prose and from my own freshly-written comments instead of checking the code for consumers. A justification I authored in the same change is not evidence.
- **Rule**: before paying a real, measurable cost to preserve a capability, grep for what actually uses it. If the only references are ones you just wrote, the capability does not exist and the cost buys nothing.
- **Project**: endless

### [2026-08-24] do not surface a finding until something real hits it; a test I invented failing is not evidence
- **What went wrong**: While building E-2055 I wrote a test asserting that 'endless lesson write' works from a subdirectory. It failed. I reported that as a discovered gap, raised it again in the handoff, and then put 'decide whether to file it' on Mike's whats-left todo list. Across three turns of his attention he had to ask twice what the actual use-case was. There was none: nothing I ran needed it, and I had no reason to think anything would. Mike: 'yet another waste of my time.'
- **Why**: I treated a failing assertion as a finding without checking whether any real caller reaches that path. The scenario existed only because I invented it, and I never asked myself who hits it before spending someone else's attention on it.
- **Rule**: before reporting a finding - and especially before putting it on the user's list - name the concrete caller or workflow that hits it. If the only thing that hits it is a test you wrote, delete the test and say nothing. A self-manufactured failure is not a discovery, and escalating one repeatedly is worse than missing it.
- **Project**: endless

### [2026-08-24] A schema change that RENAMES or DROPS breaks the installed binary until land refreshes it — say so at handoff, and never inherit a prior change's claim that the window is never entered
E-1969 renamed sessions.active_task_id. The land succeeded, but a Claude hook fired inside the window between `apply-change` (which migrates the DB) and the Justfile's closing `just build` (which refreshes the global binary), so the stale binary met the new DB and logged 50 'no such column: active_task_id' lines from the worktree reaper. Harmless — the reads fail open and self-heal — but it read as a failed land to the person running it.

Two misses, one rule each.

1. I reported the regression as clean and handed over one verify command without flagging that this is the first change to RENAME rather than ADD a column. Additive changes are invisible to old code; renames and drops are not. When a change makes existing code incompatible with the migrated DB, the handoff has to say what the land will look like, not only that the tests pass.

2. E-1929's change file asserts the migration window 'is never entered in practice.' I quoted that ordering story in my own change file and inherited the claim without testing it. It was false: the Claude hooks call ReapWorktreesForProject from five places and one of them fired mid-land. A prior task's parenthetical about what never happens is a hypothesis, not a finding — check it when your change is the one that would make it matter.
- **Project**: endless

### [2026-08-24] Before filing, search the ledger for a task that already owns the area — and when one exists, contribute EVIDENCE to it rather than a second task carrying your own solution
E-1969's land surfaced a stale-binary-meets-migrated-DB failure and I filed E-2061 for it, with three candidate fixes. I had not searched. E-1972 already owns that area — 'Decide how Claude hooks should choose between main's and a worktree's endless-go', underway, with Mike's chosen shape already written down and a session holding it. E-2050's own description says in as many words that the which-BINARY question is E-1972 and not that epic.

So E-2061 was the exact pattern Mike is fighting: a session hits a problem, files a task, and ships a competing solution into an area that already has an owner mid-deliberation. Three sessions in one day did this badly enough that E-2048 had to be run as a whole research task just to reconcile the wreckage. Adding to it while its cleanup is still landing is worse than not filing at all.

The filing habit that replaces it:
1. Search first — task list, titles, and the epics' descriptions, which routinely say which task owns which axis.
2. If an owner exists, add evidence and constraints to it. Evidence is always welcome; a solution is not, because the owner may already have one.
3. Only file when nothing owns the area — and say in the description what you searched and did not find, so the next session can check your work instead of repeating it.

A second error worth naming on its own. I claimed E-1969 was 'the first schema change to RENAME rather than ADD.' It is not: E-1659 renamed a task_types slug and E-1898 renamed sessions.process to process_id, and E-1972's analysis records that the second one corrupted the shared ledger machine-wide via a stale binary re-creating dropped triggers. I asserted novelty without checking — the same failure as inheriting E-1929's unchecked claim, which is what my previous lesson was about. Checking the ledger would have caught both.
- **Project**: endless

## Don't narrate routine judgment calls — just make them

**2026-08-25, E-1968/E-1967.** I used `--keep-status` correctly on a naming fix,
then wrote three paragraphs explaining that I'd used it and offering to undo it.
Mike's response: the flag exists so the reset does not happen — using it is the
whole point, and telling him about it defeats it. He is removing the flag because
its existence makes me narrate.

The pattern, which is broader than that flag: I finish a defensible routine call
and then hand the user a paragraph justifying it and offering to reverse it. That
is not transparency. It is asking for reassurance, and it costs the user the exact
attention the correct default was supposed to save.

Test before writing any explanation of my own reasoning: **would this change what
Mike does next?** If it would not, delete it. Specifically, never write up:
- a flag or default I used as intended,
- why I picked the obvious option,
- an offer to undo something nobody objected to,
- a "call worth your review" framing on work that was simply correct.

Report a judgment call only when it is genuinely contestable AND the user would
act differently knowing it. Otherwise the work speaks and I say nothing.

Related failure in the same session: answering "is X blocked?" with an essay.
Terser is not a style preference here — length itself is the cost.

### [2026-08-24] Current behavior is a choice, not a constraint — say 'today it does X' and ask whether X should change, instead of reasoning as though X is fixed
Four times in one session I treated the existing code as a boundary condition:

1. I inherited e-1929's comment that the schema-migration window 'is never entered in practice' and repeated its reasoning in my own change file. It was false, and E-1969's land proved it.
2. I asserted E-1969 was 'the first schema change to RENAME rather than ADD' without checking. E-1659 and E-1898 both renamed.
3. I quoted a code comment listing 'resume, respawn, aborted spawn, /clear' as fresh-UUID launches. E-2063 MEASURED it: resume preserves the id, compaction preserves it, only a clear rotates. The comment is wrong about resume and I passed it on as fact.
4. Mike asked why a tmux window option could not record the session durably. I answered that setTmuxSessionUUID overwrites it on every event — describing current behavior as if it settled the question. He had to point out that code can be changed.

The pattern is one habit: reading the codebase as a description of what is possible rather than a record of what was decided. It makes me argue against changes that are the entire point of the task, and it launders unverified comments into fresh assertions.

The rule: when the answer to a design question is 'the code does X', that is the START of the answer. Say 'today it does X, because of Y' and then say whether Y still holds. If Y is a comment rather than a measurement, say so — a comment is a claim from the moment it was written, not evidence about now.

Corollary from case 4: 'we could change it' is not the same as 'we should'. The reason not to freeze that particular option was not that the code overwrites it — it was that the value is a harness-issued handle, and E-2063's central finding is that reaching for a runtime handle as a durable identity is the error itself. Getting to the right answer required the right reason, and the current-behavior answer was not it.
- **Project**: endless

### [2026-08-24] A diagnostic is looking, not work to file — investigate it inline and report the finding in chat
I found evidence that verbs.jsonl and a ledger shard reached land uncommitted three times after the write-time-commit tasks shipped, then offered to FILE a task to diagnose why. Mike: 'Can you not just do the diagnostic yourself? (See ED-1550)'

ED-1550 is explicit — filing is the exception, and the default response to a finding is to tell the user in chat. A diagnostic is not deliverable work; it is reading code and history I already have open. Filing one converts ten minutes of looking into a backlog item someone must later re-read, re-scope and close. Investigate first, then report; file only if the FIX is real work and nothing open owns it.
- **Project**: endless

### [2026-08-25] Define a term the first time you use it in durable content, or use the precise name instead — three times in one session I shipped jargon Mike had to ask me to explain
In one session Mike had to ask what I meant by 'fork', 'consume', and 'marker' — all three in task analysis text, all three avoidable.

- 'fork' meant an unmade either/or decision. The word already means git fork and fork(2) in this codebase. 'The choice' would have been exact.
- 'consume' meant make-one-shot. It was also load-bearing for a proposal that turned out to be wrong, so the vague word hid the bad idea inside it: had I written 'unset the tmux window option after the first bind', Mike would have rejected it a turn earlier, because the wrongness is visible in the plain phrasing and invisible in the jargon.
- 'marker' meant an @endless_* tmux window option. That one I inherited from the task's existing analysis and used without ever defining it, which is worse: borrowed jargon feels established and gets no scrutiny.

He also had to ask what a 'value session' was, because I wrote 'it destroys a value session status's focal-task fallback depends on'. There is no such thing as a value session; the sentence needed 'a value THAT session status depends on'. A missing relative pronoun turned a noun phrase into a fake compound noun.

Two rules.

1. Prefer the precise name over the shorthand. 'tmux window option' is three words longer than 'marker' and cannot be misread. In ledger content the cost of length is nothing and the cost of ambiguity is permanent — E-2048 had to be run as an entire research task because one word meant three mechanisms.
2. If a shorthand genuinely earns its place, define it at first use in that document. Not in chat, in the document — the analysis outlives the conversation that produced it.

And a diagnostic worth keeping: when a reviewer asks what a word means, check whether the idea underneath it is also wrong. Twice out of three here, it was.
- **Project**: endless

### [2026-08-25] A surprising status may be derived, not stale — check the task type before calling it wrong
- **Rule**: Before declaring a task's status wrong, check its `type`. An epic's status is a pure function of its children (internal/events/epic_derivation.go); calling it 'stale' or 'misleading' is a claim about a computation, and needs the derivation rule checked, not just the row read.
- **What happened**: I reported E-971 (`ready`) as 'the single most misleading row in the tree' and proposed confirming or obsoleting it. E-971 is `type=epic`. Its ledger shows a deliberate retype to epic in 2026-07 with a written justification, followed by `epic.status_derived {underway -> ready}`. The status is computed and correct: E-1190 is `ready` and nothing is `underway`. No human left it stale.
- **Why it matters**: The proposed fix was actively harmful. `confirmed` is not in `stickyOverrideStatuses` (only revisit/declined/obsolete/blocked are), so hand-setting an epic to `confirmed` gets recomputed away on the next child mutation — it looks like a fix and silently isn't.
- **Generalize**: When a row looks wrong, ask what writes it before asking what it should say. Mike's pushback was the check I skipped.
- **Project**: endless

### [2026-08-25] When consolidating tasks that will not be implemented soon, do not fix their scope
While merging E-910/911/912 I asserted the merged task 'should be sessions + projects + notes only', because E-2029 had dropped the channel tables that E-912 named.

Mike corrected it: the merge task is to merge whatever is appropriate, and the IMPLEMENTOR decides that and gets the user to approve. There is no good reason to stake the ground for future consumption when the ground may move significantly before the task is revisited.

The failure mode is subtle because the narrowing was CORRECT at the time — the channel tables really are gone. But a task parked in 'next' or 'later' is read months later, and a scope decision baked into it then reads as settled rather than as a snapshot. It quietly forecloses on a judgment the implementor is better placed to make with fresh evidence.

So: state the OBSERVATION as evidence ('E-2029 dropped the channel tables, so check whether E-912's conversation/message half still applies'), never as a scope decision. Invariants and rationale are durable; an inventory of what currently exists is not.
- **Project**: endless

### [2026-08-25] A verify script is owned by its task's session — never run one you do not own
- **Rule**: `tests/tasks/e-NNNN-verify.sh` belongs to the session working
  E-NNNN. Do not modify it, and do not RUN it, from any other task's session.
  A verify script is valid only just before ITS task lands; running it outside
  that window produces a result nobody asked for and nobody owns.
- **What I did**: while working E-2062 I ran another task's verify script twice
  — because E-2062 changed `rebuild-db` and that script also drives
  `rebuild-db` — then reported its passing result in my handoff. I never edited
  the file, but running it was already the violation.
- **Why it is wrong even when it passes**: the script's pass/fail is that
  task's evidence for that task's land decision, gathered at the moment its own
  session chose. Reporting it from another session's handoff attributes a
  verdict to work that was not being verified, and a red result would have read
  as my regression rather than theirs.
- **What to do instead**: regression evidence for my task is the project-wide
  suites — `just test`, `just test-go`, build, lint — plus MY task's own verify
  script. If I believe my change could break a neighbouring task's behaviour,
  cover that behaviour in my own tests, or flag it to Mike. Never borrow their
  script.
- **Project**: endless

### [2026-08-25] A ledger is equivalent when it produces the same projection, not when the files are byte-identical
While rebasing a stale worktree I hit a conflict on a db-ledger segment and stopped to ask whether skipping a redundant 'Endless: record ledger entry' commit violated the never-discard-an-auto-record-commit rule. Mike's answer: the ledger's value is the projection it produces, so if main already holds those entries the commit is a duplicate delivery, not a unique carrier. The rule protects entries, not commit objects.

Rule: when a ledger commit conflicts, establish whether its entries already exist on the target (byte-exact line match across the whole segment family, since segment splits relocate lines between files). If every entry is present, skipping is zero-loss. Only entries absent from the target are a real discard.
- **Project**: endless

### [2026-08-25] Say 'I inferred' vs 'I diagnosed' — never present a stale plan's premise as a finding
I told Mike a writer bug was leaving endless-managed files uncommitted, and offered to file a task for it. Pressed on whether I had diagnosed it or still needed to, the honest answer was neither: I had inherited the claim from E-1272's stale plan and treated its falsification ('the auto-commit step still fires') as evidence of a defect. It was not. The step was original designed behavior from E-971; it firing is the feature working.

Rule: before reporting a finding, name its provenance to myself — observed, inferred, or inherited from a document. Only 'observed' earns the word 'found' or 'diagnosed'. An inference gets stated as one, with the observation that prompted it. And per ED-1550, a finding's default destination is chat, not a new task; I proposed filing reflexively and called it cheap, which it is not.
- **Project**: endless

### [2026-08-25] Following a rule is not a deliverable — do not narrate compliance
Mike's correction on E-2064: I ended the handoff with a paragraph flagging that I had left E-1956's and E-1185's landed verify suites untouched, 'per endless guide orchestration'. Leaving them alone was correct. Saying so was not.

The rule: obeying a standing instruction is the baseline, not news. Do not report it, do not cite the guide section that told me, do not frame it as a judgment call I made on Mike's behalf. A handoff reports what I BUILT and what a reader could not otherwise know — not which rules I managed to follow while building it.

Why I did it: I had treated the choice as a discovery worth surfacing because the guide's wording ('flag it to Mike') seemed to ask for it. It does not. That phrasing covers a case where a neighbouring behaviour might actually be BROKEN by my change and needs coverage — a fact about the code. 'I did not edit files I am not allowed to edit' is a fact about me.

What to do instead: silently comply, and spend the handoff line on something the reader gains from. The general form: before writing any handoff sentence, ask whether it tells Mike something about the WORK. If it only tells him something about my process or my adherence, cut it. This extends the existing rule against confirming negatives ('no stray files') — the same instinct, one level up.
- **Project**: endless

### [2026-08-25] Mike's attention is the scarcest resource — never spend it on state that resolves itself
After landing, I flagged that the worktree still held deprecated binaries from my first build attempt, hedged with 'harmless, but worth knowing before you drop it.' Mike's reply: they get reaped with the worktree, so why does it matter? It did not. I spent his attention on state that resolves itself, and dressed it up with a hedge so it would read as diligence.

This compounds a pattern he flagged earlier in the same session — a wall of text he had to skip, and a follow-up task proposed reflexively. Every unnecessary item is a withdrawal from a limited budget, and hedging ('may', 'worth knowing') does not make the withdrawal smaller; it makes it harder to dismiss, because he now has to evaluate it to discover it is nothing.

Rule: before surfacing anything, ask what he would DO with it. If the answer is 'nothing', or 'it resolves on its own', do not raise it. Expected state is not a loose end. A clean handoff says nothing about the things that are fine — no inventory of the silence, no negative confirmations, no 'just so you know'. Surface only what changes a decision he has to make.
- **Project**: endless

### [2026-08-25] Choose a task's parent for discoverability, not taxonomic correctness
I nested E-1935 (Aug, 'make db-ledger rebuild trustworthy') under E-799 (Apr, 'migrate to event-sourced architecture') because containment ran that way: E-1935's goal IS E-799's Stage 2 acceptance criterion. Mike had asked for the inverse and I did it the other way with a one-line justification.

He corrected it with a reason I did not have: recently filed tasks get far more attention than months-old ones, so nesting the new epic under the old one BURIES it. There is no UI yet that surfaces priority independently of recency, so the tree IS the priority signal.

The general rule: a parent choice is a visibility decision before it is a classification decision. Ask which node someone will actually land on, and put the live work at or near that node. Taxonomic purity that hides live work is a worse answer than an imperfect hierarchy that surfaces it.

Two mechanical notes from the same episode:
-  has no way to CLEAR a parent. Setting the child's parent without clearing the old parent's produced a cycle (799 -> 1935 -> 799), which a recursive CTE walks forever. Use , then set the other side.
- Verify an inversion by walking the tree afterward, not by reading back the single row you wrote. The row I checked looked right; the cycle was in the row I had not touched.
- **Project**: endless
