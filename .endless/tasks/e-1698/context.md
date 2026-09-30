Root cause: ResolveSessionStatusFocal's step-3 machine-wide last resort (ED-1523) returns an unrelated task; the status line has no such fallback (GetPaneStatus returns a placeholder/hint).

Seen in window verify_toml[E-1596] showing E-1461's list.
