Fix: in claim_item, if target task status is 'verify' (or any of {confirmed, declined, obsolete}), refuse with a message like 'E-NNN is in verify; pass --force to re-claim and demote to in_progress.'

Or for verify specifically, print a notice and proceed (lower friction).

Mike's call which behavior.
