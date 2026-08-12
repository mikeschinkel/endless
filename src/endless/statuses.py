"""Task status vocabulary — the one list every surface reads.

Deliberately dependency-free (no click, no db, no task_cmd) so the CLI layer can
import it at module scope without dragging in the 200ms task_cmd import that its
lazy per-command imports exist to avoid.

That import cost is why this module exists at all. The vocabulary previously
lived as four hand-maintained copies — `cli.TASK_STATUSES`, two hand-typed
`--status` help strings, `update_plan`'s validator tuple, and
`session_status_cmd._VALID_STATUSES` — and they drifted: E-1956 found `task
update --help` advertising 11 of the 13 statuses while `update_plan` rejected a
12th (`submitted`) that every other surface accepted. One list, four readers.
"""

# Lifecycle order, matching docs/status-lifecycle.mmd: the pre-work statuses,
# then the in-flight ones, then the terminals. `blocked` is the odd one out — it
# is a legacy status, not a state the diagram draws (blocking is a relation) —
# and sits with the management statuses at the end.
TASK_STATUSES = (
    "untriaged", "unplanned", "submitted", "ready", "underway",
    "unverified", "confirmed", "assumed", "completed",
    "blocked", "revisit", "declined", "obsolete",
)

# The shared `--status` help string. Derived, never typed: a status added above
# shows up in every `--help` for free, which is the failure E-1956 fixed.
TASK_STATUS_HELP = "Status: " + ", ".join(TASK_STATUSES)
