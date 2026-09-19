# E-1735 — plan (seed)

Spec is in the description. Build after the full Go conversion.

## Verification
Deliver `tests/tasks/e-1735-verify.sh`, committed (shape/output per the
`tests/tasks/e-1577-verify.sh` prototype: `ALL PASSED` or itemized failures). Over a
fixture doc set, assert the linter FLAGS an internal-ID citation ("per E-1216") and does
NOT flag a legitimate placeholder example (`--parent E-799` in a code snippet), honoring
the configured doc-path list. Verify handoff to Mike is exactly:

    esu && ./tests/tasks/e-1735-verify.sh

Add manual steps only if something truly cannot be scripted. Convention per E-1596.
