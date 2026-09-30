Brainstorm a safe-by-construction option: e.g. an emit --dry-run / a standalone 'validate this binary's enum against this DB' mode that opens the DB and runs VerifyIntegrity WITHOUT touching the ledger, so direct invocation physically cannot pollute real state during testing.

Explore what the right primitive is (dry-run flag vs separate verb vs read-only DB open) and how it composes with the existing --db main|sandbox routing.
