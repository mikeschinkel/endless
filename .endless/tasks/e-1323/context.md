Current dedup is strict byte-equal of all content columns against the latest row for the session.

Concrete incident: session 389 rows id=7 and id=8 inserted 2 min apart, same intent, byte-different due to retry-rewording (id=8 deleted as cleanup).
