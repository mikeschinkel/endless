E-1252's status rename exposed the cost: renaming a stored value forced rewriting the committed ledger, because rebuild-db replays raw event payloads.
