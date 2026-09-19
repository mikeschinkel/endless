# Synthesis — lint gate that bounces negative-confirmation ceremony

## The reframe that resolved it

The task was seeded as a *lint gate* that detects ceremony in freeform prose.
The brainstorm rejected that framing. Phrase/deny-list matching was already
tried in practice — too many false positives and false negatives — and the
worked example proves the hard case (Turn 3's "inventory of the silence") has
none of the tell-tale tokens. Detection in prose cannot be made deterministic.

The resolution inverts the problem: **make ceremony unrepresentable instead of
detectable.** The agent's end-of-session report stops being freeform prose it
composes and becomes the verbatim output of a command it feeds a structured,
validated payload: `endless task report <id> --xml=<…>`. There is no "State
outside endless: none" line because there is no field to write it into.
Determinism comes from removing the freeform surface, not from matching prose
better.

This is the enforcement layer that sits after the two prevention pieces already
landed: E-1758 (handoff-status computes git anomalies; empty = clean) and
E-1759 (spawn prompt states a delta only if anomalous). The worked example is
from before those landed, but the requester reports residual ceremony is still
frequent — "one of my biggest problems with Claude+Endless" — which justifies a
durable structural fix over more guidance.

## The mechanism

`endless task report <id> --xml=<…>` — verb `report` (chosen over `handoff`
because it is the vehicle for *all* end-of-session reporting, not just the final
handoff), one positional id, payload via `--xml`.

1. **Validated schema, not freeform.** Each field carries a value from a
   constrained set; `none` is an allowed value rendered as *silence*. Out-of-set
   values are rejected the way the Haiku verb-check rejects a non-verb (the
   requester noted, and this session then demonstrated: the verb-check bounced
   task D's own title). The agent selects; it does not editorialize.
2. **The command computes what it can.** Git/worktree/external facts come from
   handoff-status (E-1758) internally. The agent never types them, so it can
   never dress "working tree clean" as content — it was never asked.
3. **Self-complete relay output.** The command returns the entire final message
   (anomalies-or-nothing + how-to-verify + return line). The agent prints it and
   adds nothing before or after, which closes the envelope leak ("Here's my
   handoff! 🎉 …").
4. **Scoped, conditional check.** The one free-text escape hatch — genuinely
   non-computable anomaly entries — gets a per-entry Haiku classifier ("a real
   thing the reviewer could not compute, or the absence of a problem?"). It
   bounces the entry, not the message, and fires *only when free text is
   present*. The common path (`none` / validated values) makes zero Haiku calls,
   so structure-and-check together cost nothing on the normal path.
5. **Tunable wording (user lever).** The check prompt lives in a user-editable
   config surface (verbs.jsonl-style precedent), not hardcoded in the product
   template — so when the agent freelances, the requester tells it to improve
   the wording and it edits the *config*, giving the requester a lever instead
   of forcing template perfection upstream. Handoff-message templates may become
   tunable the same way later; start with the check wording.

## Interception & triggering (decided)

- **A CLI command, not a hook.** Hooks are for what a command cannot do; this
  can be done with a command, so the command *is* the mechanism. (The one place
  the final message is already parsed — the Stop hook — runs async and cannot
  block, so it could only warn, not bounce. Not used.)
- **Reminder on terminal status-set.** Setting a wind-down status returns a
  stdout reminder to route this handoff and all further reporting for the rest
  of the session through `task report`. Nudge in the reminder, gate in the
  command. Fires on: `underway → unverified`, `→ assumed`, `→ completed
  --outcome`. Does NOT fire on `submitted`, `ready`, `confirmed`, claim/underway,
  or revisit/declined/obsolete.
- **Mid-session gap + bypass.** Before any terminal transition, the spawn prompt
  carries the default (E-1759 territory). A user keyword — working name
  `FULL STATUS` — licenses an *unconstrained* response for that one turn
  (per-response, not sticky), the explicit "elaborate when I ASK" lever. Keyword
  lives in the tunable surface so it can be renamed.

## Open at implementation time (not blocking)

- Exact XML schema field set and which fields are enumerable vs. free text.
- Where the tunable config surface lives (verbs.jsonl-style project file?).
- Whether handoff-message *templates* also become tunable (deferred; do the
  check wording first).
- Final keyword spelling for the bypass.

## Follow-ups spawned

- **ED-1531** — architecture decision: end-of-session reports are produced by a
  structured command, not freeform prose (decides E-1771/1772/1773).
- **E-1771** — implement `endless task report <id> --xml` (the command, schema,
  validated rejection, handoff-status integration, self-complete relay output,
  scoped anomaly check, tunable check wording). cleans_up E-1760; relates_to
  E-1758/1759.
- **E-1772** — emit the reminder on terminal status-set. cleans_up E-1760;
  blocked_by E-1771.
- **E-1773** — add the `FULL STATUS` bypass keyword + mid-session reporting
  guidance. cleans_up E-1760; relates_to E-1759/1771.
