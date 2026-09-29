Force-push and other history rewrites (non-interactive rebase --onto, filter-branch) do not expose their discarded commit set in the command the way reset --hard / branch -D do — the drop set depends on remote-tracking state (possibly stale without a fetch) or a computed rebase plan.

E-1734 ships SURGICAL db-ledger-discard detection for reset --hard + branch -D plus a BEST-EFFORT remote-tracking check for force-push.

Emerged from E-1734's scoping.
