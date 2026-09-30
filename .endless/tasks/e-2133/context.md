Endless read commands resolve their scope -- which database, which project -- from ambient context and then omit it from the answer, so a caller quotes a truthful answer about something they did not name.

session list already prints 'Sessions - project: probe' and that header is the precedent; session-query list-live's JSON carries project_id but never names the database.
