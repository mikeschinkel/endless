Catches the case where a delegator session bypassed the file-time check (e.g. with --draft) and then tries to spawn — the spawned session would have no clean contract to inherit.
