# Evidence: the shipped label channel dropped 90% of what the user sent

Measured 2026-08-16 over `session_messages` since E-1953 landed, counting
line-leading `$TOKEN` uses in user messages:

| sigil          |  n | status                        |
|----------------|----|-------------------------------|
| `$PUFFERY`     |  8 | not in vocabulary — ignored   |
| `$DUPLICATION` |  6 | ignored                       |
| `$JARGON`      |  3 | ignored                       |
| `$UNCLEAR`     |  3 | ignored                       |
| `$UNNECESSARY` |  3 | ignored                       |
| `$FORMATTING`  |  2 | ignored                       |
| `$NOTE`        |  1 | ignored                       |
| `$DEFY`        |  1 | ignored                       |
| `$MISSING`     |  1 | ignored                       |
| `$CUT`         |  1 | recognised                    |
| `$GOOD`        |  1 | recognised                    |
| `$WRONG`       |  1 | recognised                    |

**31 uses, 3 recognised.** `session_gates` holds exactly one label row, attached
to a pre-E-1953 relay checkpoint with no `raw_draft` — i.e. not even a corpus
sample. The mechanism was never disabled and never failed to fire; it fired
every time and rejected silently, because `scanSigils` matches only
`CUT|BLOAT|WRONG|GOOD|FULL` and an unrecognised token produces no match, no
record, and no error. The one "not recorded" notice that does exist covers only
a BARE known label, so it never fired either.

Two things follow, and they are different tasks.

## 1. The vocabulary was the wrong ontology (owned by ED-1555 / E-1975)

E-1953 shipped labels about the MINIMIZER'S PERFORMANCE — did it cut too much
(`$CUT`) or too little (`$BLOAT`). The user types labels about the REPLY'S
DEFECTS — puffery, duplication, jargon, formatting, unclear. Only `$WRONG`
overlaps. The user was never grading the minimizer; they were critiquing the
writing.

This is empirical support for ED-1555 (free-form, discovered from use), which is
still `proposed`. Recorded here rather than edited into that decision because
E-1975 is underway in another session.

Note the immediate consequence for E-1975 — and note that it does NOT require
the user to type a token. A classifier reading "this is puffery" or "seam is
jargon" recognises the shape as well as `$PUFFERY` would. What matters is that
critiques of this shape ARE denylist entries. The minimize prompt already carries an anti-puffery section with phrase
anchors, so a critique of that shape can append straight to
`report-prompts.jsonl` — config, no task, no land, no batch job, no judge. That
is a real consumer of user feedback available today, and it is upstream of the
whole optimizer loop.

## 2. The CHANNEL is in-band, and that is this task

ED-1555 fixes the mechanism as `$TOKEN "quoted span"` typed into a prompt.
Everything typed into a prompt is visible to the agent by construction, so the
agent responds to the critique. The user's stated complaint is that this
commentary is itself the thing being criticised, and that it persists after
being told to stop — which is the same failure that motivated the minimizer.

Channels ranked by whether silence is structural:

- **Sibling terminal pane** — `endless feedback ...` run in another pane.
  Claude never sees it. The ONLY structurally silent option.
- **`!cmd` in the prompt** — NOT silent. Tested 2026-08-16: `! echo ''`
  produced "No output, as expected. Ready when you are."
- **Slash-command skill** — not silent. "Run this and say nothing" is a
  guideline; a skill body cannot bind the model's final message.
- **In-band `$TOKEN`** — the current design; least silent of all.

## Build shape

- `endless feedback` CLI, **prose critique only — no vocabulary at all**, not
  even an optional one. The requester's position (2026-08-16) is explicit: they
  do not want to reach for `$PUFFERY` when they can write "this is puffery", or
  `$JARGON` when they can write "seam is jargon, explain what you mean, don't
  use seam". A token the user has to recall is the same tax whether it is
  required or optional. The ONLY structured element in the whole surface is the
  optional span reference.
- Optional span reference. The user's preferred notation is `P<n>` for a
  paragraph of the last reply and `T<n>:P<n>` to reach back a turn, chosen over
  `$<n>` specifically to avoid shell escaping. The user is content to count
  paragraphs by eye; a `--show` that prints the numbered reply from
  `session_messages` is a convenience, not a requirement.
- Repeatable, so several critiques land in one invocation:
  `endless feedback P1 '...' T2:P3 '...'`
- **Silent on success. Loud on every rejection.** This is the rule the evidence
  above bought: an unparseable reference, an unknown flag, a missing session —
  each must fail visibly. 28 dropped signals is what silent rejection costs.
- A `/e-feedback` skill as the front door for users who will not open a
  terminal, accepting that it comments. Adoption argument, not a functional one:
  skills are the culturally legible surface, and a better CLI with no on-ramp
  loses to a worse skill with one. Skill body stays a thin template over the
  CLI so the binary owns parsing and validation (see
  docs/private/research-2026-08-15-skills-arguments.md, option 3).

## Open

- Whether the agent may ever call `endless feedback` itself. If it may, the
  CLI can print "Reply ONLY with 'Ok'" — still a guideline, but positioned in a
  tool result immediately before composition, and the worst case is one word of
  noise rather than a whole reply. Low stakes make a guideline acceptable here
  in a way it was not for the minimizer.
- Whether out-of-band invocation can resolve WHICH session the feedback is
  about. A sibling pane has no session id of its own; it needs the tmux window's
  session, which `endless` already resolves elsewhere (E-1585).



---

## Update, 2026-08-22 (ES-1104, while landing E-1975)

**ED-1555 was rejected.** The in-band `$TOKEN "quoted span"` mechanism this task
replaces no longer has a decision behind it.

**E-1975 shipped that mechanism anyway, and widened it.**
`internal/hookcmd/sigils.go` now accepts any uppercase token, not only
`CUT|BLOAT|WRONG|GOOD|FULL`, and records one row per quoted span. That fixed the
defect measured on 2026-08-16 — 31 uses, 3 recognised, `$PUFFERY` (8),
`$DUPLICATION` (6), `$JARGON` (3) and the rest silently ignored — but it did not
retire the channel. It made the rejected channel work better.

**So retiring it now belongs here**, or in a task filed against this one:
`scanSigils`, its `applySigils` / `stageReportTurn` hook wiring, the
`report_labels` table's write path, and the `$A`/`$B`/`$MORE`/`$LESS` directives
that ride the same parse. Otherwise a rejected mechanism ships indefinitely
alongside the replacement this task builds.

Note the directives are NOT all feedback: `$A`/`$B` pick between two offered
minimizations and `$FULL` licenses one un-minimized reply. Those are commands to
Endless, not critique, so decide per sigil whether `endless feedback` absorbs it
or it needs its own home before deleting the parse.



## The in-band half, corrected (2026-08-22)

The update above framed this task as "add the out-of-band channel, and separately
retire the sigils". That is half the design. Mike's account of how it got here:

  specific $sigils -> freeform $sigils -> NO $sigils

and the reason for the last step is not ergonomics. A sigil sits in the prompt,
so the agent reads it and opines on it, verbosely — which is the behaviour the
minimizer exists to remove. Making the vocabulary freeform (what E-1975 shipped)
does not touch that: it fixes recognition and leaves the opining intact.

**Nothing replaces the sigils in band.** A model can read sentiment from the
user's ordinary words, so a special syntax buys nothing and costs a vocabulary
nobody can recall mid-complaint. Recorded as ED-1575, which reverses ED-1555.

So this task has two halves, and the second is not merely deletion:

  1. `endless feedback`, out of band, as described above.
  2. Derive the in-band signal by reading ordinary turns for sentiment, replacing
     what `report_labels` is populated from today.

Half 2 has a consequence for E-1975's loop that has to be decided before it is
built. Stated as the questions it actually is:

Today the loop checks itself like this. Before you see a reply, a model guesses
whether you will complain about it. Later we look at what you did. If the
guesses match what you did, often enough, the loop treats that model as a decent
stand-in for you and lets it score prompts you never looked at.

"What you did" is currently unambiguous: you typed a sigil, or you did not.
Under half 2 it stops being unambiguous — a second model reads your ordinary
words and decides whether you were complaining.

  1. If one model guesses your reaction and another model decides what your
     reaction was, is anything about YOU being measured? Or only whether two
     models agree with each other?

  2. Both are the same model family, so they share the same blind spots. Will
     they agree with each other more often than either agrees with you, and make
     the loop look better calibrated than it is?

  3. Should the check instead use only things you did unambiguously — running
     `endless feedback`, and picking A or B when offered two replies — and let
     sentiment reading inform the corpus without ever scoring the judge?

Question 3 is probably the answer, but it costs something: those two signals are
much rarer than "did the user sound annoyed", so calibration would move slowly.
That trade is the decision.



### Mike's answers, 2026-08-22

**Q1 — yes, something about him is measured.** He writes in-band annoyance about
Claude's output constantly; `endless feedback` adds an out-of-band channel; and
`.endless/LESSONS.md` is a third source nobody counted — 3265 lines, 129
recorded lessons, each one a correction he gave. Signal is abundant, not scarce.

That removes the premise Q3 rested on. The argument for restricting the check to
unambiguous acts was that they are rare, so sentiment reading was needed for
volume. If the in-band signal is constant, restricting throws away the bulk of it
rather than trading speed for rigour.

LESSONS.md deserves its own look and has never been discussed. It is the highest
quality record of his preferences in the system — written deliberately, at the
moment of correction, in his framing — and nothing reads it.

**Q2 — the model mapping in the question was wrong.** Roles today:

  agent          Opus 5           writes the draft
  minimizer      sonnet/medium    cuts it            report_cmd
  judge          sonnet/medium    scores the cut     minimizer_judge
  generator      sonnet/medium    writes challengers minimizer_optimizer

Mike's opus-vs-sonnet split is the agent/minimizer pair and it is real. The
correlation risk is a different pair: the judge and the proposed sentiment
reader, which would both be sonnet at medium — the same model, not merely the
same family, so the worry is stronger than stated.

He plans to allow a different vendor (ChatGPT) as the adversary. That breaks the
agent/minimizer correlation. It fixes judge-vs-sentiment-reader only if those two
are what differ, which is the thing to specify.

**Q3 — "without ever scoring the judge" needed defining.** The judge guesses,
before the user sees a reply, whether they will complain. Later the loop checks
that guess and counts how often it was right. That count is the agreement number,
and the loop acts on it: below 60% it stops trusting the judge and raises how
often it shows A/B pairs. Scoring the judge means producing that number. The
proposal was: let sentiment reading label corpus rows, so the optimizer has data,
but never let it be the answer key the agreement number is computed from.

Given Q1, that restriction now looks wrong. Reconsider before building.
