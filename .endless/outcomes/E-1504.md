Commit 96d479469 on task/1504. The flag surface is one thing now: `--agent`,
`--json`, and `--format <fmt>` as the long form of both, on all 30 commands that
render a result, derived by `output_options(*, agent=...)` in cli.py from the
single fact of whether a command HAS an agent rendering.

WHAT DIVERGED FROM THE TASK ROW. The description's counts were off, in ways that
changed the work rather than just the arithmetic. `--llm` sat on 15 commands, not
12 — `task landed` and `task unlanded` arrived later via E-2095 and take their
flags from a shared factory, so a source scan for the option declaration misses
them. And of the "16 commands with --json and no agent view", two carry a
`--json` that names the command's INPUT rather than a rendering: `session order`
parses its SPEC argument as JSON, `task import` names a file. Neither is a
renderer, so neither took `--format`. That leaves 14, and 30 commands carrying
`--format` overall — the same 30 E-2141 counted as exposing an output flag.

THREE DESIGN CHOICES THE DESCRIPTION LEFT OPEN, decided by Mike mid-session and
recorded in the plan field. (1) `--format` reaches every command with an output
flag, not only the agent-facing ones. (2) A command with no agent rendering does
not ADVERTISE `agent` but still RECOGNISES it — `[text|json]` in `--help`, and a
written refusal leading with `--format json` when someone passes it anyway. That
needed a custom `ParamType`: `click.Choice` cannot separate what it advertises
from what it accepts, and its generic "is not one of" would tell a reader the
spelling was wrong when the spelling was right and the RENDERING was missing.
(3) The description governs over E-1504's own analysis on `session status` — the
flag, not the rendering. Nothing had to move to preserve that requirement:
E-2141 already states it in full under "The requirement that must not be lost",
naming the same `classify()` ordering and the same ES-1196 incident.

ONE DELIBERATE BEHAVIOUR CHANGE beyond the rename. `--llm --json` used to yield
JSON silently, because every renderer tested `as_json` before `llm` — a
precedence that was an accident of write order, stated in no help text. Naming
two different renderings is now refused. Picking a winner for a contradiction is
exactly the silent incorrectness E-2143 exists to end.

`--llm` is retired rather than deleted: still recognised, never working,
refusing with a pointer at `--agent`. That is the E-1000 pattern, and it is a
removal rather than the deprecated alias the description rules out — it never
renders anything. Agent muscle memory is the whole reason it earns the pointer.

FOLDED IN rather than filed, each cheaper to fix than to describe:
`docs/guide/reference.md` recommended `task list --json ... --llm`, which this
change makes an error; the guide's code-block comment columns needed re-aligning
for the longer flag; and test fixture and function names carrying `llm` were
renamed with it. Nothing was split out and nothing new was filed.

VERIFICATION. `.endless/tasks/e-1504/verify.sh` — 50 assertions, green, with
`tests/test_output_format.py` folded in as a fail-fast first check. The durable
half is that file's 18 tests, including a drift guard that walks the whole
command tree: `--json` without `--format` fails, `--format` without `--json`
fails, `--format` advertises `agent` iff the command carries `--agent`, and
every `--agent` command still carries its hidden `--llm` pointer. The two
input-`--json` commands are a named exception in it, so adding a third is a
deliberate act with a test to change. That guard is the thing this surface never
had, and the reason it drifted to 15-of-30 in the first place.

Project-wide regression green: 3385 pytest, `just test-go`, `go vet`,
`just build`, `just guide-check`, `just lifecycle-check`.
