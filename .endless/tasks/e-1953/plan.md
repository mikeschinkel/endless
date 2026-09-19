# Verification

Two increments, verified separately.

## Increment 1 — disable

```sh
endless task report E-1952            # command refuses / reports disabled
endless task report --help            # PostToolUse hook does NOT fire
```

Pass: neither invocation injects the "append the block, unchanged" instruction,
and no session is asked to emit a block.

## Increment 2 — minimizer

Fixture: a draft containing (a) a markdown table, (b) a fenced code block,
(c) a one-line verify command, (d) three paragraphs narrating the analysis,
(e) a direct answer to a direct question.

```sh
endless task report E-NNN --draft-file tests/fixtures/report-draft.md
```

Pass criteria, asserted by a golden test over the fixture:

1. The table survives byte-for-byte.
2. The fenced code block survives byte-for-byte.
3. The verify command appears in the output.
4. The direct answer appears in the output.
5. The three narrating paragraphs are gone.
6. Output is shorter than input.

Then:

```sh
endless task report E-NNN --raw        # prints the persisted draft in full
```

Pass: the raw draft round-trips unchanged, so nothing the minimizer cut is lost.

## Increment 2 — enforcement

A Stop-hook test, not a CLI test:

- Reply verbatim with the minimized output → allowed.
- Reply with the minimized output plus one appended sentence → rejected.
- Reply without having called `task report` at all → rejected.

The third case is the one that matters; the first two are the easy half.

## Increment 2 — signals

```sh
# simulate a user prompt beginning "$CUT you dropped the verify command"
```

Pass: the UserPromptSubmit hook records the label against the preceding turn's
triple. `$CUT` / `$BLOAT` / `$WRONG` with no following text are rejected;
`$GOOD` alone is accepted. `CUT the scope` and `WRONG: ...` do not fire.

## Not verifiable by test

The minimizer's judgment quality. That is what the eval corpus and the promotion
gate exist for — an override is promoted into the `//go:embed`ed default only
when it beats the current default over the corpus. No golden test substitutes
for that.

---

# Answers to build-session questions (from E-1952 requester)

**1. Sessions with no claimed task — (b), id-less allowed.**
Exempting unclaimed sessions exempts most of the verbosity the gate exists to
stop; unclaimed quick questions are exactly where sprawl happens. Don't force a
claim either — that taxes a one-line question. Make the id optional: the corpus
row keys on the session, which always exists; the task id is attribution only.
Scope the gate to registered projects. Command stays `task report` for now.

**2. "Every turn" — fire only when assistant text exists.**
A clarifying-question turn has text and goes through. A tool-only turn has
nothing to minimize. Keep empty `last_assistant_message` as fail-open: "said
nothing" versus "field unpopulated" is not a distinction worth guessing on.

**3. Agent-tool subagents — still exempt.**
A subagent's final message is a return value, not a handoff. Nothing may sit
between an agent and its subagent. The parent's own reply — the thing the user
reads — is already gated.

**4. FULL STATUS — replaced by `$FULL`, and it is a full one-turn exemption
from the gate.**
FULL STATUS was built against the old placeholder scheme; revise it to the
sigil vocabulary. `$FULL` is a *directive*, not a label, so unlike
`$CUT`/`$BLOAT`/`$WRONG` it may stand alone or carry the question with it —
`$FULL why did the rebase conflict?`. Same sigil, different class; the hook
routes on the word. The licensed reply does not go through the minimizer —
routing it through would contradict the license.

**5. Loop guard — session-scoped counter mirroring `RelayBounceLimit`.**
Add one thing: once the budget is spent, fail open **and record it**. A silent
livelock and a silent surrender are indistinguishable from outside, and the
second is how a gate rots unnoticed.

**6. Ships enabled — make the switch configuration, not a code constant, and
default it on.**
This overturns the analysis's "the switch belongs in code." The switch belongs
in `.endless/config.json`, endless's canonical per-project config, and **ships
enabled for product users** — the pain of not having it is what drives
adoption, so there is no case for shipping it inert. Endless's own
`.endless/config.json` can set it off while the prompt is being tuned, which is
the one place that needs to opt out.

`.endless/config.json` is also the right home because a gate an agent can
switch off is not a gate — this must not live anywhere an agent edits in the
course of normal work, `.claude/settings.json` included.

**Also:** `relay_gate.go` belongs to E-1901, which is `assumed` and landed at
`aa83a0c`; its `relayGateEnabled` constant is currently `false`. Link
`E-1953 replaces E-1901` so the supersession is visible.
