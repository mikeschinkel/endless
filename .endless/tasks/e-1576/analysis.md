Replace with: section label 'This task:', then indented lines '<Relation phrase>:  E-NNNN [status]' where the label provides the explicit subject and the relation phrase carries the full direction (Blocks / Blocked by / Cleans up / Cleaned up by / Relates to / Implements / etc.). Example output: 'This task:\n  Blocks:         E-1537 [in_progress]\n  Blocked by:     E-1557 [ready]\n  Relates to:     E-1136 [needs_plan]\n  Cleaned up by:  E-1564 [needs_plan]'.

Verify no scripts depend on the old format (grep for '(blocks)' / '(blocked by)' patterns in tests + tooling); migrate any that do.
