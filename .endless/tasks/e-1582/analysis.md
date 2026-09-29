Mike's evolution toward structural alternatives (epics, type-aware spawn, coordinator pattern) handles topic drift better.

Layer 1 (active-task context-refresh injection) stays.

Tests for pivot-gate behavior get deleted, not adapted. Schema migration must cleanly DROP session_gates;
