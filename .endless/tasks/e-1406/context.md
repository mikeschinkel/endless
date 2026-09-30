E-1401's gate fires on cli/hook events whose session_id can't be resolved.

_perform_claim_work's status_changed event still has a target_session=None branch (spawn pre-claim) where the event emits unattributed — the gate will fire on spawn from a plain shell.
