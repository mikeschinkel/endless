Settled design, from working through it with Mike. Part 2 of the original
description is already done: ED-1073 was widened the same morning E-1934 was
filed and now reads "no time-frozen specifics in any durable field", enumerating
description, analysis, text/plan, outcome and decision bodies. What remains is
the gate (part 1) and the guide wording (part 3).

## Rule 3 — line citations

`_guard_inline_content` has Rule 1 (whole value is a path token) and Rule 2 (an
absolute path token anywhere). Rule 3 refuses a line citation.

- Matches `<name>.<ext>:<digits>` where `<ext>` is in the citation-extension
  set. Also `<name>.<ext>:<digits>-<digits>` (range) and
  `<name>.<ext>:<digits>:<digits>` (line:col).
- Does NOT match a bare leading-colon number. Nobody writes a bare line number
  that way, and the false positives are everywhere: clock times, host:port,
  ratios.
- Skips any token preceded by a scheme separator or an at-sign, or beginning
  with a www prefix. This is what keeps host:port out of the gate, and it is
  load-bearing rather than belt-and-braces: many source extensions ARE ccTLDs
  (Serbia, Paraguay, Poland, Saint Helena, Mali, Cocos, Moldova), so the
  extension set alone does not separate a file citation from a domain.
- No escape hatch. Rule 2 has one; Rule 3 does not.
- Applies wherever Rule 2 applies: description, text/plan, analysis, outcome and
  decision bodies. E-1992 collapses these into task_content, at which point the
  gate stops enumerating flags and guards content.

## Configuration

Three keys under a content block in the project config file, which already
carries list-valued keys, so no new shape is needed.

- A built-in default extension set of roughly eighty entries covering systems,
  JVM and .NET, scripting, JS/TS and web, functional, data and config, docs,
  and build and schema files.
- Two thin override lists layered over that default rather than replacing it,
  so a project adding or removing one extension does not restate eighty. Named
  `content.extensions.block` and `content.extensions.unblock`, nested. The verb
  attaches to the behaviour rather than to set membership, which is what makes
  it readable a year later: block names one more extension whose citations are
  refused, unblock stops refusing one the default covers. Earlier candidates
  spelled around an abstract "set the gate matches" and every one of them was
  ambiguous in one direction or the other.
- An extension listed in BOTH lists is a config error and must be refused at
  load, not silently resolved. That follows the house pattern already set by
  --clear against a field flag, and by --status against --keep-status: when two
  settings spell contradictory intent, refuse and say so.
- A switch that disables the gate outright, for a project that finds it more
  trouble than it is worth, nested alongside as `content.extensions` sits under
  `content`. Worth giving Rule 2 its own switch at the same time, by the same
  reasoning.

## Widening the built-in path exemption

`_builtin_allowed_dirs` exempts endless's own config and cache directories, and
its docstring already anticipates being extended at that composition point.
Widen it to any dot-directory directly under home. Rule 2's stated rationale is
that absolute paths are non-portable and that a temp path dies with the
worktree; neither is true of a tool's conventional dot-directory under home,
which names the same location for every user on every machine. Home-relative
paths that are NOT dot-directories stay refused — a personal projects directory
really is machine-specific. Encountered live: writing a lesson about the Claude
transcript directory was refused, and the text had to be degraded to prose that
named no location.

## Part 3 — guide wording

The tasks guide still describes text as a full implementation plan "including
approach, file paths, verification steps", which invites the thing ED-1073
forbids, and says nothing about line numbers anywhere. Fix the wording to say
file and function names.
