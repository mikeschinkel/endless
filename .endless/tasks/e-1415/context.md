after worktree-sandbox land the companion can hold a stale id (e.g. 1) that resolves to the wrong real-DB row, silently misattributing every event/bind/update for the affected session.
