Add one sentence to the shared final-message rules in `internal/templatecmd/templates/handoff/_close.tmpl`, outside the `report_gate` conditional so it survives both the minimizer-on and minimizer-off branches:

> Do NOT check, mention, or report any other task's verify suite. A landed task's suite is invalid by design.

Put it in `handoff_close` so every task kind (todo, bugfix, research, brainstorm, epic, claim) gets it, not only the kinds that render `handoff_verify`. Mirror the existing comment style in `_close.tmpl` with a short comment naming why the sentence is there.

Pin it in `internal/templatecmd/template_test.go` next to the existing "do NOT confirm the negative" assertions: the sentence appears in the rendered handoff for every template, with the report gate on and off.

Run `just test-go` and `just guide-check`. If `docs/guide/orchestration.md` quotes the final-message rules, keep its wording in step.
