Replace `task report`'s payload-and-render model with a minimizer over the
agent's entire draft reply. Design synthesized in E-1952's outcome — read that
first; this task is the build.

Why the current command fails: the agent writes the verify command in prose and
then tells `task report` there is nothing to report. Two output channels exist
and content lands in the cheap one. Any design where the agent volunteers a
structured payload fails identically, which is why the four defects named in
E-1952's seed are subsumed rather than fixed individually.

Step one, immediately: disable the command and its PostToolUse hook. It emits
"Nothing to report." most of the time and unrequested noise the rest, so the hook
currently demands agents append a block carrying no signal. Keep the command
reachable enough to rebuild in place.

Shape:

- `task report --draft-file <path>` takes the agent's ENTIRE freeform draft
  reply as plain markdown. No JSON/XML, no field tagging — any format requiring
  the agent to pre-structure the draft reintroduces the restatement tax that
  caused the routing failure.
- An adversarial agent minimizes it. Objective: "delete what the user didn't
  ask for," NOT "make it short" — a pure minimizer guts a requested discussion.
  It occupies the slot the current ceremony check occupies.
- The output is the only thing the agent may say. A Stop hook enforces verbatim
  and also catches "never called it," which is otherwise the trivial bypass.
- Every turn, not just handoffs.
- Persist the raw draft; expose it on demand. This is both the safety net and
  the training corpus, and it is what lets the minimizer be aggressive at zero
  risk.
- Bounded appeal: one, and the appeal text is itself minimized.
- Fix the PostToolUse hook to key on a successful render rather than the
  command name (it currently fires on `--help`).

Prompt as shipped asset:

- Invariants (tables verbatim, code blocks intact, verify command always
  survives, direct questions get direct answers) plus anti-puffery/anti-jargon
  guidance go in the `//go:embed`ed default. The anti-puffery half is a stated
  product benefit, not personal config; the user template holds additions.
- Guidelines to the adversarial agent, not regex gates.
- A generative rule ("delete any sentence that characterizes the reasoning or
  narrates the analysis rather than delivering information the user needs")
  with a phrase denylist as anchors beneath it — the list alone never
  converges.

Feedback loop:

- Persisted triples (prompting user message, raw draft, minimized output) form
  the eval corpus. Prefer this over mining transcripts with a periodic job
  (cf. E-698): the triple is exact, transcript-mining is inferred.
- `$CUT` / `$BLOAT` / `$WRONG` / `$GOOD` supply the labels — first token of a
  line. Text is REQUIRED after the first three (a bare complaint gives the
  corpus nothing to learn from); `$GOOD` may stand alone. Detected by a
  UserPromptSubmit hook, never by the agent noticing. `$GOOD` is required, not
  politeness: a corpus of only complaints trains toward verbosity.
- Template edits need no task and no land. Ceremony is reserved for promoting a
  proven override into the embedded default, gated on beating it over the
  corpus.

Open question carried from E-1952: the exact wording of the minimizer's
objective and the invariant list.
