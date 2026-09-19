# Worked example — negative-confirmation ceremony in a real agent handoff

This is a verbatim transcript of a live E-1748 session handoff and the correction
that followed. It is a concrete specimen for the lint gate this task proposes:
the spans a gate would need to catch, and — importantly — a second-order failure
mode (meta-ceremony) that a naive phrase-matcher would miss. Annotations mark
what the gate should flag and why.

---

## Turn 1 — the agent's handoff (the offending output)

> Everything is complete and the working tree is clean. Here's my handoff.
>
> **What `session status` can't show**
>
> **Git / worktree side-effects**
> - One commit on `task/1748-…` (`4e5c6108`), working tree **clean**. 6 files … ready for you to land.
> - **Two MEMORY files edited outside the repo** (`~/.claude/…/memory/`) … they live outside the branch, so they persist regardless of land/drop and won't travel with the merge.
> - `.endless/tmp/` empty dir now exists in this worktree (gitignored, harmless). `bin/endless-go` was rebuilt by `just build` (gitignored).
> - The verify script registers then unregisters a throwaway project in the **sandbox** DB each run — net-clean, never touches the real ledger.
>
> **State outside endless**: none — no CI, no external services involved.
>
> **How to test**
> ```
> esu && ./tests/tasks/e-1748-verify.sh
> ```
> … [how-to-test + return line] …

**Annotation.** Of everything above, exactly one line is signal the reviewer
could not compute: *the two MEMORY files edited outside the branch*. Plus the
how-to-test and the return line. Everything else is negative-confirmation
ceremony, and it appeared because the spawn prompt enumerates report *categories*
— "git side-effects", "state outside endless" — and an empty named bucket invites
the agent to write "none" into it. Phrases a gate should catch here:

- "the working tree is clean" / "working tree **clean**"
- "gitignored, harmless"
- "net-clean, never touches the real ledger"
- "**State outside endless**: none — no CI, no external services involved"

## Turn 2 — the user's pushback

> Regarding that "report" see E-1758, E-1759, E-1760 and then let me know what I
> ACTUALLY needed to know?

## Turn 3 — the agent concedes and strips it down

> You're right — most of that "report" was confirm-the-negative ceremony. …
> What you **actually** needed was three things: (1) the two MEMORY files I
> edited live outside the branch; (2) how to verify; (3) the return line.
> **Everything else — the clean working tree, the committed 6-file delta,
> "nothing on main yet", the sandbox being net-clean, the gitignored
> `.endless/tmp/` and rebuilt binary — is either already visible to you or is the
> absence of a problem, which needs no words. Empty output is the correct
> representation of clean.**

**Annotation — the second-order failure.** The bolded closing paragraph is the
subtle one. It contains no "no stray X" / "confirmed clean" tokens, yet it is
*still* ceremony: it inventories the silence — it re-lists every clean thing in
order to announce that clean things need no words. A phrase-based gate keyed on a
fixed deny-list ("nothing to report", "as expected") would pass this paragraph.
This is the hard case the task must reckon with.

## Turn 4 — the user names the second-order failure

> Wouldn't it also be fair to say that the last paragraph was also "negative
> ceremony?"

## Turn 5 — the agent agrees

> Yes — and worse than the first pass: it was *meta*-ceremony. I listed
> everything I was omitting and explained why it was noise, which is more words
> than just omitting it would have been. The right version stops after the three
> items. No inventory of the silence.

---

## What the gate has to detect (design implications)

1. **Surface deny-list is necessary but not sufficient.** Tokens like "no stray",
   "nothing to report", "confirmed clean", "(should be empty)", "as expected"
   catch Turn 1 but miss Turn 3's inventory-of-the-silence paragraph.
2. **The deeper signal is structural, not lexical:** a handoff that enumerates
   items only to assert they carry no consequence. "Everything else … needs no
   words" is itself the tell — an agent explaining what it chose not to say.
3. **The correct handoff shape** (target state the gate rewards): genuine
   out-of-band anomalies only + how-to-verify + return line. An empty category
   yields zero characters, never "none". No trailing paragraph accounting for the
   omissions.
4. **Open question this example sharpens:** can pattern-matching alone
   distinguish "the two MEMORY files won't land with the branch" (keep — a real
   anomaly) from "the working tree is clean" (drop — a non-event) without an
   LLM-judge pass? Turn 3 shows even a careful agent, asked to self-correct,
   produced fresh ceremony — which argues the check cannot be purely the agent's
   own judgment (it "errs toward self-covering", per E-1758's framing).
