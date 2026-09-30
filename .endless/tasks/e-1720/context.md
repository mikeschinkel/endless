The ledger is the DB's append-only WAL, so neutralizing bad or superseded events must not rewrite committed history.

Surfaced by the E-1710 audit.
