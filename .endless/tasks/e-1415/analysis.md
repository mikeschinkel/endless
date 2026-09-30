Add a DB existence check at the resolver level so all consumers (E-1401 gate, session show, task bind, the new session id verb) fail loudly instead of trusting the bad id.
