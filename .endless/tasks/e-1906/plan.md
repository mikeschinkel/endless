`FlagNeedsRecap` fires on both `Stop` and `SessionEnd` in
`internal/hookcmd/claude.go`, flagging a session as owing a recap. It dates from
the era when a Claude session was open-ended and covered many tasks, so a recap
was the only way to reconstruct what a session had done.

That premise no longer holds: one session now corresponds to roughly one task,
and `endless task report` covers the handoff for that task directly. The recap
flag, its consumers (`internal/hookcmd/recap.go`), and any surfacing of
"needs_recap" state should be audited and removed if nothing else depends on
them.

Scope: find every reader of the flag before deleting the writer — session
monitor / status rendering may still display it, and the sessions column (if any)
needs a schema change to drop.

Surfaced by Mike during E-1901, which added the Stop-hook relay gate alongside
the existing Stop-branch `FlagNeedsRecap` call.
