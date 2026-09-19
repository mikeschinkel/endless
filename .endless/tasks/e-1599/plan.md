# Render analysis in `endless task show` + `--all-fields` flag

## Problem
`tasks.analysis` is settable via `endless task add/update --analysis` (cli.py:1290) but
`endless task show` (`detail_item`, task_cmd.py:3445-3459) never renders it — analysis content
is write-only from the CLI (visible only via the web UI / raw SQL). E-999 (in_progress) already
names this gap; this task delivers just the read surface. The text→plan rename (E-1000) is out
of scope.

## Implementation
1. `task show` CLI (cli.py:1028-1050): add a `--analysis`/`show_analysis` flag (parallel to
   `--text`/`--outcome`), passed into `detail_item`. Add `--all-fields` that turns on
   description + analysis + text + outcome + children in one switch, so handoffs can request
   complete task content without enumerating each flag.
2. `detail_item` (task_cmd.py ~3445): render a cyan `— Analysis —` section. Order:
   Description → Analysis → Text → Outcome (analysis is pre-plan design content, so it precedes
   the plan). Include analysis in the `--llm` and `--json` output paths too.
3. Fix the validator help strings (task_cmd.py:296, 375) that wrongly steer long-form analysis
   to `--text`; they predate/contradict E-999's analysis-vs-plan separation.

## Tests
- New `tests/test_analysis_show.py` (or extend an existing show test): set `--analysis` on a
  task; assert `task show --analysis` emits `— Analysis —` with the content; assert
  `--all-fields` emits every content section; assert default `task show` still omits analysis.

## Verify
- `endless task update <t> --analysis "X"; endless task show <t> --analysis` shows `— Analysis —`
  with `X`; `endless task show <t> --all-fields` shows every content section; `just test` green.

## Out of scope
- text→plan rename (E-1000); analysis content-rule enforcement (E-1073 / E-1075).
