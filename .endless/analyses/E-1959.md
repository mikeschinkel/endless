E-1953 persists the eval-corpus triple (prompting user message, raw draft,
minimized output) plus the `$CUT`/`$BLOAT`/`$WRONG`/`$GOOD` label on
`session_gates`, but nothing reads it back for a human. `task report --raw` is
agent-side and resolves the *calling* session, so from a shell it cannot show
another session's draft. Reviewing the corpus today means hand-written SQL and
a `length()` call to guess whether a cut was reasonable.

That blocks the loop the whole design rests on. The minimizer prompt is
explicitly expected to need substantial tuning, and promotion of an override
into the embedded default is supposed to be gated on beating the current
default OVER THE CORPUS. Neither judging a cut nor running that comparison is
possible without being able to read raw and minimized side by side.

Needed, roughly:

- List recent triples across sessions — when, which session, which task (may be
  null), prompt excerpt, raw/minimized sizes, label. Filterable by label, so
  "show me everything I marked $CUT" is one command.
- Show one triple in full, with raw and minimized presented for comparison
  rather than concatenated. A diff view is the obvious default given the
  minimizer only deletes; a side-by-side is the alternative.
- Read-only, and it must reach the real ledger from a plain shell (the reviewer
  is a human at a terminal, not a session).

Open questions for whoever takes it:

- Command shape. `endless report log` / `endless report show <id>` reads
  naturally but `report` is currently a `task` subcommand, so `endless task
  report --log` may fit the existing tree better at the cost of overloading a
  command that is otherwise agent-facing.
- Whether it also needs an export (JSONL) for running the promotion comparison
  offline, or whether that is a separate concern.

Note for anyone reading the table early: most existing rows predate E-1953 and
carry only `sanctioned_text` — they are E-1901-era relay checkpoints, not
corpus samples. Filtering to rows with a non-null `raw_draft` is what selects
real triples.



---

## Planning session 1 (partial) — decisions and open questions

Planning started in the session that filed this, and stopped early: context was
large, and the requester raised a prior question that should be settled before
any of the below is built. The task was retyped to `brainstorm` at that point,
because the need is no longer the premise — it is the thing to determine.

Everything under "Decided" below is therefore CONDITIONALLY decided: those were
real choices the requester made, but each assumes a viewer gets built at all.
If the prior question resolves the other way they lapse rather than carry
forward, and none of them should be treated as a constraint on the synthesis.

### The prior question, unresolved and load-bearing

**Does reading the corpus actually produce a benefit?** Raised by the requester,
not answered. Everything below assumes it does. Settle this first — if the
answer is no, most of this task evaporates and the remaining need is whatever
the promotion gate strictly requires, which may be a non-interactive export
rather than a viewer.

The case for: judging a cut needs raw and minimized together, and today that is
hand-written SQL. Without it the prompt gets tuned by recollection.

The case against: nobody may actually sit and review triples. The labels the
promotion gate wants arrive live via `$CUT`/`$BLOAT`/`$WRONG`/`$GOOD`, and if
those are the real signal, a viewer is a surface that gets built and not opened.
The honest test is whether the requester would have used it in the last week —
there were ~5 real triples in that span.

### Decided

1. **Command shape — restructure, do not add a sibling group.**
   `task report` becomes a command with subcommands:
   - `endless task report submit --draft-file <path>` — the agent path (what is
     today the bare `task report [<id>] --draft-file`).
   - `endless task report log` — the human review path.

   NOT a new top-level `endless report` group, and not `endless session
   reports`.

   **Consequence the planning session must scope:** this renames the
   agent-facing invocation, which is currently named in SEVEN places, all of
   which must move together or agents will be told to run a command that no
   longer exists:
   - `reportChannelRule` (SessionStart) — internal/hookcmd/claude.go
   - `reportRelayInstruction` (PostToolUse) — internal/hookcmd/claude.go
   - `reportMissingReason` + `relayBlockReason` (Stop bounces) —
     internal/hookcmd/relay_gate.go
   - the spawn handoff close — internal/templatecmd/templates/handoff/_close.tmpl
   - the respawn handoff — templates/handoff/respawn.md.tmpl
   - the wind-down nudge — src/endless/task_cmd.py
   - docs/guide/tasks.md, index.md, orchestration.md, help/_topics.md
   Plus `tests/tasks/e-1953-verify.sh` and `e-1973-verify.sh`, which assert the
   current spelling in several places.

   Whether `task report --draft-file` keeps working as a deprecated alias is
   NOT decided. Note that live sessions carry the old spelling in context, so a
   hard break strands every session running at land time.

2. **`show` renders side-by-side columns.** RAW left, MINIMIZED right, aligned.
   Rejected: inline strikethrough (reads as one document but assumes the
   minimizer only deletes), and unified diff (correct under rewording but reads
   as a patch).

   Known cost to solve, not to rediscover: side-by-side wraps badly on tables
   and fenced code blocks — precisely the content the minimizer's invariants
   protect, so it is precisely the content a reviewer is checking. `internal/mdterm`
   already has table rendering that may help.

3. **No retroactive labelling.** The surface stays read-only. Labels keep
   meaning "the user's in-the-moment reaction", which is the more honest signal;
   the cost accepted is that a turn not labelled live can never be labelled.

### Open — do not decide alone

4. **Scope.** Held by the requester for further discussion. The three shapes on
   the table were: read-only viewer; viewer + JSONL export; viewer + export +
   an eval runner that scores prompt A against prompt B over the corpus and so
   implements the promotion gate. This interacts with the prior question above —
   if the viewer is not worth building, the export may still be.

### Facts a planning session should not re-derive

- The triple lives on `session_gates` where `kind_id = 2`: `user_prompt`,
  `raw_draft`, `sanctioned_text` (the minimized output), plus `label` /
  `label_text` and a nullable `task_id`.
- **Most rows are not corpus samples.** As of filing, 60 of 65 rows were
  E-1901-era relay checkpoints carrying only `sanctioned_text`. Filter on
  `raw_draft IS NOT NULL`.
- Zero rows were labelled at that point, which is itself evidence about how much
  signal the live-label path is actually producing.
- Rows persist after `cleared_at` is set, so review reads closed rows; there is
  no retention or pruning policy yet, and the table grows per reported turn.
- `task report --raw` is not a substitute: it resolves the CALLING session, so
  from a human's shell it cannot reach another session's draft.
- This repo ships `"report_gate": false`, so triples accumulate here only from
  voluntary use.


