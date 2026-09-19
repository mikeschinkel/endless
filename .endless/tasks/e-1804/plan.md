# Combine and front-load task-title validation

## Problem

Filing one task cost four `endless task add` invocations and three rejections,
because the title contract is enforced *late, sequentially, and in internal
jargon*:

1. **Sequential gates.** Length (≤100) is checked first; the verb rule is
   checked only after length passes. Two gates → ≥2 round-trips even when
   everything else is right. Each round-trip is a full CLI call + error parse +
   retry — real tokens and latency on one of the system's most frequent ops,
   across every project and agent.
2. **Invisible contract.** `task add --help` lists flags but states none of the
   title rules (≤100 chars, must start with a verb, name WHAT not HOW). The
   rules live only inside the validator, surfaced only on violation. Reading
   `--help` first does not help.
3. **Jargon-leaking verb error.** On a non-verb lead the error talks about a
   "registered verb", offers `endless verb add '<word>' --definition ...`, and
   warns that registering a non-verb "defeats the check for everyone". This is
   internal mechanism: "registration" is just a cache of AI-validated verbs, not
   an authorization scheme. To a context-free agent it reads as a permissions
   puzzle, and it dangles a bypass that invites gaming. The rule is simply:
   start with a real verb so the task's intent is legible.

## Deliverable

Make a well-formed title land on the first attempt.

1. **Validate all title constraints at once.** Run length, verb, and WHAT-not-
   HOW checks together and report every violation in a single error, so one
   rewrite fixes everything. No fail-fast on the first gate.
2. **Front-load the contract.** State the title rules in `task add --help`, and
   inline the two mechanical ones ("≤100 chars; start with a verb") in the
   AGENT banner so they are seen without a guide round-trip. Expand in
   `endless guide tasks`.
3. **Rewrite the verb-reject error, WHY-first and jargon-free.** The reject
   path (Haiku judges the first word not a verb) currently reads
   "'<word>' is not registered" and offers `endless verb add '<word>'
   --definition`. Drop "registered" / "is not registered" — "register" implies
   verbs are special or authorized, when it is merely a cache of validated
   verbs. Rename the manual command `verb add` → `verb confirm` (you are
   *confirming* a word is a verb; the `--definition` is the sanity check), and
   frame it as the escape hatch for a Haiku false-negative — a real verb it
   failed to recognize — not a way to force a non-verb through. Target wording:

       Titles must start with a verb, so the task's intent is clear.
       '<word>' isn't a verb. Lead with what the task does — Add…, Fix…, Show….
       (If it truly is a verb, confirm it: endless verb confirm '<word>' --definition "…")

## Decided

Keep E-1264's auto-register: when Haiku confirms an unrecognized first word is a
verb, it is cached and the title passes automatically (observed live for
'combine' and 'steer' while filing tasks this session). This is the intended
low-friction path and it closes the rewrite-bypass — it stays. Its
`Auto-registered verb '<word>': <def>` success line is intentional and
test-asserted (test_verb_gate.py), so it is left as-is. Only the *reject* path
(a genuine non-verb) gets the de-jargon rewrite and `verb confirm` rename in
deliverable 3.

## Non-goals

Do not weaken the gates — the ≤100 / verb-led / WHAT-not-HOW policy encodes the
clean-ledger intent (titles self-explanatory) and stays. This task lowers the
cost of *satisfying* the policy on the first try, not the policy itself.
