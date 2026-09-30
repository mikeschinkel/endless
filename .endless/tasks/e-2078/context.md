E-1898 replaced pane identity with (server_uuid, address) and removed TouchSession's collision invalidation on the reasoning that a reissued pane resolves to a DIFFERENT processes row, so "the case cannot arise" — and that a genuine same-server collision "leaves both rows alone and readers order by last_activity".

Reproduced: sessions 976 (process_id 91, last active 2026-08-06) and 1152 (process_id 185) both read as %422, which blocked esu in that tmux window.

Neither row is wrong — they are two different panes that share an address string.
