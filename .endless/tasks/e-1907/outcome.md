Obsoleted into E-1671 (2026-08-06).

The upcasting-pipeline epic already owns this mechanism. Workstream 4's
coverage linter and per-prompt tripwire (applied-set is a subset of the
registry) IS the loud-unknown-kind check, and workstream 7 already folds the
dropped-`prompt` skip — the same class of silently-skipped retired thing — into
the pipeline as a seed migration. A separate RetiredKinds registry would be a
second mechanism that workstream 7 would then have to consolidate.

The enumeration is entangled too: 23 of the 43 kinds in ValidKinds hit
replayEvent's silent default branch, and they are dominated by session,
conversation, message and session_status kinds — exactly what workstream 2
reclassifies as machine-user scope and routes to a separate ledger. Classifying
them independently would pre-empt that split.

The full finding — the ValidKinds-is-write-side-only analysis, the split
between "absent from ValidKinds" (decidable alone, workstream 4) and "in
ValidKinds without a replay arm" (the 23, workstream 2), the enumeration
itself, and the E-1906 provenance — is folded into E-1671's text under "Known
defect this epic must close".
