`endless sql` and the listing verbs return every matching row with no cap, so a consumer that cannot predict the result size bounds it downstream with `head` — and `head` is silent, which makes a truncated result indistinguishable from a complete one.

That silence turned two searches in one session into false "no existing task" conclusions: E-1935 fell off a `head -30` and E-1535 off a `head -24`, both ordered by id ASC so the newest and most relevant rows were exactly the ones dropped.

Endless already has the right idiom — the session-status view never hides silently, printing "… N hidden (--show-hidden)".
