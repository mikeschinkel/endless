Raised while handing off E-2376 in go-logutil, where the user had to edit the
commands the session handed them, and said they have asked for this before
without it sticking.

## The two problems

1. **Commands wrap and break on paste.** A long single-line command in a
   fenced block wraps in the tmux pane where the Claude session runs. Copying
   the wrapped text picks up newlines and spaces at the wrap points, so the
   pasted command fails. The session cannot see the pane width, so it has no
   signal that a line will wrap.

2. **Paths are longer than they need to be.** Sessions write the home
   directory out in full instead of `~`, and hand over scratchpad or tmp
   filenames so long they wrap twice. Beyond the paste problem, a long path
   makes the command hard to scan for what it does.

## Why instructions so far have not stuck

- Nothing the session reads says it. The guide's one-command handoff
  contract, the handoff template and the project CLAUDE.md files say WHICH
  command to hand over, not its shape.
- The Claude Code harness tells a session to write scratchpad paths
  "complete and absolute, in one piece", a nudge against half of the rule.
- Lessons do not reach a session. The go-pkgs CLAUDE.md forbids reading
  LESSONS.md, so a lesson written there (go-logutil has one on this, written
  2026-10-31) cannot change the next session's behavior unless Endless folds
  it into something the session does read.
- It is a formatting habit; compliance decays over a long session even with
  the rule in context.

## Related finding from the same conversation

The session was asked whether a hook could reliably extract the commands a
session hands over from its prose. Its answer: not reliably. Fences are
untagged or inconsistently tagged; fences also hold error text, file content
and TOML; inline code spans sometimes carry commands, sometimes with
placeholders; and which fence is "the" verify command lives only in the
prose. A structured channel (a command or tool call with typed fields) is
the only parse-free route; a tagging convention enforced by a Stop hook is
the next best.

## Candidate resolutions to weigh

- A rule in the user's global CLAUDE.md (covers every project, every
  session) naming a column budget, `~` for home, a `cd` line before long
  relative paths, and no scratchpad paths.
- A sentence in the guide's one-command handoff contract and in the
  handoff templates, so it lands at the point of use.
- A Stop hook that rejects a reply whose fenced block has a line over the
  budget or contains the absolute home path.
- A structured channel for handed-over commands (`endless task report` or
  similar), which also answers the extraction question.
- Feeding relevant lessons back into the handoff a spawned session reads.
- Having Endless tell the session the pane width (tmux knows it), so the
  budget is a fact, not a guess.
