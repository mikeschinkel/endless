While building E-989 we articulated when non-fatal-on-error is justified:

1. Opportunistic / best-effort by design (diagnostics, telemetry).
2. Self-healing on a later event (e.g. BackfillProcess retries on UserPromptSubmit).
3. High expected error rate under normal operation — would be noisy without informing the user.

snapshotPlanFile in cmd/endless-hook/claude.go currently swallows errors with 'log.Printf' and returns nil to the caller. Audit whether that fits criterion (1) — losing one plan snapshot is genuinely tolerable — or whether it actually matters because downstream features (plan import, plan attach, drift detection) silently break when snapshots are missing.

If (1) holds: leave it, document the rationale in a comment so the next reader doesn't second-guess.
If not: convert to fatal, matching InitSession / SetProcess / WriteCompanion (E-989).

Out of scope: a sweep of every other 'log.Printf("...: %v", err)' site in the hook — file follow-ups if the audit finds more.