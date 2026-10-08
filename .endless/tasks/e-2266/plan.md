# Find why verify passes for agents but fails for the user

Open questions for Mike to answer before this research starts. Scale: about
1,380 Claude Code transcripts for Endless (~2.6 GB), and the verify runner's
cache holds reports for 128 tasks.

## Q1. Which evidence does the research use?

| Option | Pros | Cons |
|---|---|---|
| A. Claude Code transcripts only | They hold both Mike's failure reports and the agents' explanations | 2.6 GB to sift; a case where Mike never pasted the output can't be found |
| B. Cached verify reports only | Exact, machine-readable failures with task and commit | Failed reports are deleted once a task passes, so most past cases are gone; no explanations |
| C. Transcripts plus a review of the code: every place Endless behaves differently for an agent (`agentenv`, `agent_env.py` and their callers) | Every cause can be traced to the code that produces it; the code review also finds causes that never showed up in a transcript | Two pieces of work instead of one |

Recommendation: C.

## Q2. How far back does it look?

| Option | Pros | Cons |
|---|---|---|
| A. All six months | Nothing is missed | Early cases come from code since rewritten, so their causes may no longer exist |
| B. Since the verify runner took its current form (refusals, isolated `HOME`) | Cases match today's runner | Misses older causes that may still be live |
| C. The last 8 weeks | Cheapest; most relevant | May find too few cases to see patterns |

Recommendation: B, plus any older case the code review (Q1 C) points to.

## Q3. How are cases found in the transcripts?

| Option | Pros | Cons |
|---|---|---|
| A. Search Mike's messages for pasted verify failure output (`FAILED:`, `✗`, a CTRF path) | Finds the failures themselves, however the agent reacted | Misses cases Mike described without pasting |
| B. Search agent replies for "worked for me", "passes here", "can't reproduce" | Finds the explanations directly | Phrasing varies; misses agents that silently fixed it |
| C. Both, then de-duplicate by session | Finds the most cases | none beyond A's and B's own |

Recommendation: C.

## Q4. How is each cause confirmed?

| Option | Pros | Cons |
|---|---|---|
| A. From the transcript's explanation alone | Cheap | Agents' explanations were guesses at the time, and some will be wrong |
| B. Reproduce each cause today, running a small test in both contexts with the tmux method from the E-2225 probe | Confirmed, not guessed | Old suites aren't meant to be re-run, so reproduce with a new minimal test, not the original suite |
| C. A for every case; B only for causes still possible in today's code | Confirms what still matters | Removed causes stay unconfirmed, which doesn't matter because they're gone |

Recommendation: C.

## Q5. What does the outcome contain, and what happens to bugs found?

| Option | Pros | Cons |
|---|---|---|
| A. A table (case, task, cause, intended or bug, still present?) with proposed bugfix tasks for Mike to approve | Mike decides what gets filed, as with any finding | One round with Mike before anything is filed |
| B. The table, plus bugfix tasks filed directly under E-2261 | Ready to work at once | Files without Mike's call, which he has said is his |
| C. The table only | Smallest | The fixes have no tasks yet |

Recommendation: A.

## Q6. How is the reading split up?

| Option | Pros | Cons |
|---|---|---|
| A. One session reads everything | One consistent judgment | Transcript dumps fill its context long before 2.6 GB is read |
| B. Subagents in parallel, each searching one stretch of time and returning only the cases it found; the main session classifies them | Fits the size; the main context only receives conclusions | Each subagent judges "is this a case?" alone, so borderline ones vary |
| C. A script searches first (Q3's patterns) and writes a short list of matching excerpts; one session reads the list | Cheap and repeatable | Only finds what the patterns match |

Recommendation: C, then B for the matches that need reading in context.
