Today the main DB is canonical and rebuild-db is not trusted: a rebuild is neither robust nor reliable, so it must not be exercised against real data, and verify scripts have to route around it.

That is a load-bearing gap — the ledger is supposed to be the source of truth the DB projects from, and E-1829's two-developer milestone depends on each developer rebuilding the projection from the shared ledger. Until rebuild is trustworthy, every executor handler has a projector twin that nothing exercises end to end, so divergence between them is invisible until someone rebuilds and loses data.
