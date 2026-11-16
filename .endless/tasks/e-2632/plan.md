# Open questions: how `endless worktree land` reports open post-land items

## What prompted this
Landing E-2529 (go-gitutil) printed:

    • E-2529 is now landed: post-land items still open —
        P16 After the land, main contains GitHub's LICENSE-only commit ...
        P20 origin/main equals local main after the push, and the repo has no tags
        P21 The first Test workflow run on the pushed main passes ...
      Do them on the landed, installed result and record them with
      `endless task reconcile E-2529`; the last one settles it as assumed
      (`endless guide tasks/post-land`).

The user, who ran the land, could not tell from it what they had to do. In that case the answer was "nothing": the session that claimed E-2529 does the items, and it did. The message is printed by `settle()` in `src/endless/unlanded.py`.

Terms used below:
- **Post-land item**: a plan item marked `post_land`, done after the land (`endless guide tasks/post-land`).
- **Claiming session**: the session listed as `Claimed:` in `endless task show`, the one that did the task's work.
- **Item statement**: the plan item's one-line statement of what will be true. **Item verify text**: the item's optional `verify` field.

## Decided without asking
- The message says first that the land succeeded and main advanced, and only then lists the remaining items. Today the yellow bullet and "still open" lead, which reads as a failure.

## Q1. Who does the message tell to do the items?
| Option | Pros | Cons |
|---|---|---|
| A. Keep today's "Do them …" with no subject | none | The person running the land, usually the user, cannot tell whether the items are theirs (what happened on E-2529) |
| B. Name the claiming session as the one that does them, with its ID and the `endless session goto E-<id>` command that reaches it | The user learns the items are not theirs and where to go | Wrong when the claiming session has ended or its pane is gone; the message must then fall back to something else |
| C. Name the claiming session and have the land also send it a message in its tmux pane telling it to do the items | The user has nothing to do or relay; the session starts at once | The land writes into another session's live input, which can land mid-turn or into a prompt the user is typing in that pane; fails silently when the pane is gone |
| D. Address the user: "these are yours to do" | Matches who reads the message | Wrong whenever the plan gives the items to the session, as on E-2529; reconcile needs a verdict file that is an agent's tool, not a person's |

Recommendation: B, falling back to D's wording when no claiming session is live.

## Q2. What does the message show for each item?
| Option | Pros | Cons |
|---|---|---|
| A. The item statement only (today) | Short | A statement says what will be true, not what to run; P16's statement names no command |
| B. The statement plus the item verify text when it has one | Uses data already on the item; tells the reader how the item will be checked | Verify text is optional and was on only one of E-2529's three items; it says how to check, not what to do |
| C. A new required `how` field on post-land items holding the exact commands, shown under each | The reader gets runnable commands | Every post-land item must carry a command written before the land, when the exact command (a commit SHA, a run ID) is often not known yet |
| D. The statement only, plus a pointer to `endless task show E-<id>` for detail | Short; detail is one command away | One more command before the reader learns anything new |

Recommendation: B.

## Q3. What does "installed" say?
"Do them on the landed, installed result" fits projects with an install step after a land (Endless itself). For a Go library such as go-gitutil nothing is installed.
| Option | Pros | Cons |
|---|---|---|
| A. Drop "installed" everywhere | Never wrong for a project with no install step | For Endless's own tasks, loses the reminder that the items need the installed build |
| B. Say "installed" only for a project that declares an install step | Correct in both kinds of project | Endless needs a project setting that says whether there is an install step; a project that omits it loses the reminder |
| C. Keep it everywhere (today) | none | Confuses readers in projects that install nothing, as on go-gitutil |

Recommendation: B.

## Q4. Does the message tell the reader how to record verdicts?
| Option | Pros | Cons |
|---|---|---|
| A. Name `endless task reconcile E-<id>` only (today) | Short | Only the agent can act on it: reconcile prints a file to fill in, which a person does not know to do |
| B. Name the reconcile command and say the claiming session runs it | Tells the user it is not theirs to run | none beyond Q1's |
| C. Omit the recording step from the land message, and leave it to the claiming session's own guidance | Shortest message for the user | The claiming session learns about it only if something else tells it; if the user does the items themselves, nothing tells them to record |

Recommendation: B, which follows from Q1's B.
