Fix DIRECTLY on the live DB (a .sql change-file UPDATE via apply-change) + rewrite the ledger payload.status values for consistency; do NOT use rebuild-db (its replay is lossy, drops ~233/947 tasks).

Also add the missing 'unapproved' status to the valid set.
