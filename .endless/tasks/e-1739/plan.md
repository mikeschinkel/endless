# E-1739 — plan (seed)

Spec is in the description. Blocked by E-1738 (sqlite-vec `endless search`).

## Verification
Deliver `tests/tasks/e-1739-verify.sh`, committed (shape/output per
`tests/tasks/e-1577-verify.sh`: `ALL PASSED` or itemized failures). In an ISOLATED
sandbox DB, assert: adding a task whose title/description is a semantic near-dup of an
existing UNIMPLEMENTED task is DENIED with the matches listed; `--ignore-dups=<wrong>`
still denies; `--ignore-dups=<correct-count>` proceeds; a settled
(assumed/confirmed/...) match does NOT trigger denial. Verify handoff to Mike is exactly:

    esu && ./tests/tasks/e-1739-verify.sh

Add manual steps only if something truly cannot be scripted. Convention per E-1596.
