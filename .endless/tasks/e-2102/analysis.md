Not a one-liner: internal/sandboxcmd/reapguard.go reads window names to decide which DB sandboxes to SPARE, matching the bracketed `[E-NNNN]` form specifically — its comment calls that the user's own record that the task is still in play.

Renaming without moving that regex silently drops the protection and lets a sandbox be reaped while its window is open.

The regex must match the bare form and keep matching the bracketed one, since windows named before this lands stay open.
