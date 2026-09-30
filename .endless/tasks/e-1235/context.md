Today, claim_item emits task.status_changed unconditionally when the current session is not the owner.

Surfaced when I (the implementing session) ran 'endless task start 1203' during E-1229 testing and silently un-verified E-1203.
