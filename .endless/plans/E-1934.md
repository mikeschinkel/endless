# Plan — E-1934

Design rationale is in this task's analysis. Part 2 (widen ED-1073) was already
done before this task was picked up and is not in scope.

## The gate has two kinds of rule, and that distinction drives everything

The existing gate (E-1744) was built for ONE purpose: catching a file path
mis-passed to an inline flag. Typing `--text ./plan.md` when you meant
`--text-file ./plan.md` stored the path and discarded the content — the
corruption that lost E-1626 and E-1564. Three checks now live in it, and they
are not the same kind of thing:

- FLAG-USAGE check: the whole value is a single path token. This is the
  mis-pass. It is meaningless on the file branch, because passing a path to
  `--text-file` IS the correct usage. Correctly inline-only.
- CONTENT checks: an absolute path anywhere in the value; and, new here, a line
  citation anywhere in the value. These are rules about what durable content may
  say. Content does not become acceptable because it arrived in a file.

The absolute-path check sits on the inline branch today only because it was
added inside a gate built for the mis-pass case. That is an artifact, not a
reason. Both content checks move to resolved content.

## 1. The line-citation predicate

New helper beside `_is_absolute_path`, `_is_path_shaped` and
`_absolute_path_tokens`. Returns offending tokens, matching:

- `<name>.<ext>:<digits>` where `<ext>` is in the effective extension set
- the same with a range suffix
- the same with a column suffix

Skips any token preceded by a scheme separator or an at-sign, or beginning with
a www prefix, so a host and port is never read as a citation. Load-bearing, not
decoration: many source extensions are also ccTLDs.

Does NOT match a bare leading-colon number.

## 2. Where the checks run

In `_resolve_content_flag`, on the RESOLVED content, for both branches:

- line citations — new
- absolute paths — MOVED here from the inline-only branch

The mis-pass check stays where it is, inline-only.

E-2089 measured why this matters: 129 of 412 stored plans carry a file-and-line
reference, and plans are long-form, so they arrive by `--text-file`. A gate that
skipped the file branch would miss the majority of the thing it exists to stop.

Because every long-form field on task add/update, decision add/update, epic and
the status-transition verbs funnels through this one resolver, description,
text, analysis and outcome are covered by construction. No per-flag wiring.

## 3. Configuration

Read through the project config module, following the nested-block-with-defaults
shape `project_minimizer_config` already uses.

- built-in default extension set, full breadth (see analysis)
- `content.extensions.block` — adds
- `content.extensions.unblock` — removes
- an extension in both is refused at config load, not silently resolved
- a switch disabling the line-citation check, and a separate switch for the
  absolute-path check. Project config only, no per-invocation form. The
  mis-pass check is covered by neither: it catches an error, not a style.

## 4. Guide wording

In the tasks guide source, the `text` row still reads "Full implementation plan,
including approach, file paths, verification steps", and that guide never
mentions line numbers. Rewrite to say file and function names, and state the
rule. Re-run the guide index generation afterwards.

## 5. Tests

Suite at the task's verify path, first statement sourcing the shared harness;
read the tasks CLAUDE file before writing it. Cases: the three matched shapes;
bare-colon non-match; clock time, host-and-port and a ratio passing; a URL
carrying a source extension passing; block and unblock each taking effect;
both-lists refusal; each disable switch; absolute paths now refused on the file
branch; and line citations refused on the file branch, the regression that
proves the placement decision.

## Phase 2 — the retroactive sweep, in this task

Stays in E-1934 rather than becoming a new task. ED-1550 forbids reopening
SHIPPED work to extend it, and exempts work still live in the session that
landed it — which this is. Filing a separate sweep task would be the
file-more-than-you-close behaviour that decision exists to stop.

Ordering, not scope, is what separates the two phases: the gate lands first, or
the sweep has nothing holding the line behind it.

Measured with the shipped predicate, against the main checkout:

- plans: 126 of 418 files carry a citation, 505 in total
- analyses: 23 of 131 files, 51 in total

Rewriting is per-file judgement, not a regex: each citation has to become the
function, command or symbol at that location, or the search that finds it. The
worst files carry twenty-odd each.
