Give agents a pre-flight that predicts whether `worktree land` will succeed, so a land blocker is found and fixed before the human runs the land.

Most of the machinery exists. `worktree land --dry-run` already runs the migration-overlap check and the project's pre-land hook (`_refuse_if_land_gated`), then rehearses the rebase on a throwaway branch in a throwaway checkout (`_rehearse_land_rebase`), classifying any conflict and exiting non-zero.

Decided (Q1 B1, Q2 A+B+C, Q3 A):

1. Add `endless worktree land <id> --check`. It shares the dry-run code path and differs only in output:
   - clean: exactly one line, `Landable: E-NNNN onto <base> @<short main sha>`, exit 0. The sha names the base the verdict holds for, since main moves;
   - not landable: the refusal in the existing one-line-summary format (the same renderer E-2184's gate and the conflict classification use), exit non-zero;
   - no "Would land" header. `--dry-run`'s output stays exactly as it is.

2. Checks `--check` (and therefore `--dry-run`) adds, so the pre-flight matches every refusal a real land makes before main moves:
   - A. Uncommitted user work: land's step 2 partition (auto-commit vs user work); refuse when user work is non-empty;
   - B. Commits that touch the ledger: the same `_ledger_touching_commits` refusal land makes;
   - C. Compile the rebased branch in the rehearsal checkout (land's step 4.2 equivalent): self-dev only, in the throwaway checkout, never in the live worktree. Reuse the build that step 4.2 runs.
   Order: A and B first (cheap), then the migration/pre-land gate, then the rebase rehearsal, then the compile. Stop at the first failure.

3. Handoff template: `internal/templatecmd/templates/handoff/_close.tmpl` tells the agent to run `endless worktree land --check`, fix anything it reports, and only then mark the task `unverified`. The `Landable:` line goes into the final message beside the `endless worktree check` result. The status transition itself does not run the check: the real land re-runs every check, so a skipped pre-flight costs a failed land, never a broken one.

Tests:
- `--check` on a clean branch prints exactly the `Landable:` line naming base and sha, and exits 0.
- Each of A, B, the migration overlap, a rebase conflict and a post-rebase compile failure: non-zero exit with that refusal, nothing changed in the worktree, main, or the database.
- `--dry-run` output is unchanged apart from the added refusals.
- The rendered handoff template contains the `--check` instruction.
