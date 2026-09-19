# Why replies must restate the task ID

Reported by Mike 2026-08-05.

## The workflow this breaks

Mike runs 50+ concurrent sessions in separate tmux windows. The pattern is:
submit a prompt, switch away to another window, come back later. On return, the
agent's reply is usually separated from his question by a run of tool calls —
so the question that prompted it has scrolled off.

A reply that says "Both are submitted now" or "Needs a plan" is then unreadable
without scrolling back to find which task was being discussed. At 50 windows
that cost is paid constantly.

## Observed failures

| Session | Written | Needed |
|---|---|---|
| E-1865 | "Both are submitted now, awaiting your approve to reach ready" | "Both **E-1876 and E-1879** are submitted now, awaiting your approve to reach ready" |
| E-1832 | "Needs a plan — not spawnable as-is" | "**E-1853** needs a plan — not spawnable as-is" |

Note the subject is often NOT the session's own task: in both cases the session
was working E-1865 / E-1832 while answering about *other* tasks. So "the reader
knows which session this is" does not supply the missing ID — the window title
is the wrong answer.

## The rule

Restate the explicit task ID(s) in the reply whenever the reply is about a task:

- answering a question about any task,
- reporting a status or transition,
- naming a task filed, blocked, spawned, or landed.

State it in the FIRST sentence that makes a claim about the task, not only in a
trailing summary — the first line is what is read on return.

Applies to the session's own task and, especially, to other tasks discussed.

## Where it could be delivered

1. **Handoff templates** — `internal/templatecmd/templates/handoff/*.tmpl`
   (todo, bugfix, research, epic, brainstorm, respawn, _close). The reporting
   instructions already live here, so the rule belongs beside them. Reaches
   every spawned session at spawn time.
2. **`endless guide`** — the orchestration / reporting sections, for sessions
   not started from a handoff.
3. **The E-1803 PostToolUse hook** — E-1803 already establishes a compose-time,
   harness-authoritative `additionalContext` nudge attached to `task report`.
   That is the same lever, and this rule could ride it. Honest limit, already
   documented on E-1803: it is a strong nudge, not a hard gate.

`endless task report <id>` ALREADY prints `Task: E-NNN`, so the report channel
is compliant today. The gap is freeform replies — exactly the coverage facet
E-1803 describes.

## Open question for the implementer

Whether this is one line added to each existing template plus a guide section,
or a shared fragment the templates include (they already share `_close.tmpl`).
The fragment approach avoids seven copies drifting.
