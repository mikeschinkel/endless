## The spark

Reading Endless task text in the terminal is painful. `glow` (via `endless task show <id> --text | glow -p`) wraps lines where it shouldn't — hard line-breaks land mid-sentence / mid-word, so documents are hard to read. Piping to `less` loses colorization of titles, headers, inline code, etc.

I want Endless to render markdown in the terminal *much* better than glow does — correct, human-expected wrapping 99%+ of the time, plus colorized titles/headers/code.

## What's nagging me

- Why doesn't glow do a better job? Is "wrap where a human expects" genuinely hard, or is glow just under-invested? Need to understand the root cause before committing to an approach.
- Correct reflow may be non-trivial: distinguishing hard line breaks in the source from soft wraps the terminal should reflow; respecting code fences, tables, lists, blockquotes; honoring terminal width.

## Open approaches (to explore, not decided)

- **Fix glow**: clone it, stand up a new Endless project for it, work the fix there.
- **Build into Endless**: a `view`/`render` command that renders markdown ourselves (from scratch or on a library).
- Some hybrid.

## The automation angle

This feels like the first clear-cut fit for Andrew Karpathy's AutoResearch-style loop
(https://github.com/karpathy/autoresearch): an automated make-change → render → inspect
output → correct → repeat loop that iterates until wrapping is correct 99%+ of the time.
I'm open to whatever harness makes that work.

## Desired outcome of this brainstorm

Land on: root-cause understanding of the wrapping problem, a chosen approach (fix-glow
vs build-in-Endless vs hybrid), and whether/how to run an automated iterate-until-correct
loop. Spawn the follow-up decision + do-tasks from here.
