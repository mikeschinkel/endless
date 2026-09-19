Shipped `endless session activity` CLI verb and "Session Activity" tmux menu item.

CLI: `endless session activity [<session-ref>] [--kinds <list>] [--json]`. Default session = current (3-layer resolver from E-1294). Accepts ES-NNN / E-NNN / plain integer. Reads `.endless/db-ledger/*.jsonl`, filters by `actor.session_id`, groups by event kind into the archetype-2 shape (Filed, Decisions, Claimed, Shipped → verify, Confirmed, Assumed, Declined, Obsoleted, Released, Dependencies, Field updates).

Tmux: prefix+e and right-click menus get "Session Activity" (hotkey `a`) opening the report in an 80%/80% display-popup paged by less.

Smoke test on dev DB (session 356) returned the expected 16 events grouped correctly:
  Filed (5): E-1287, E-1290, E-1292, E-1294, E-1299
  Claimed (3): E-1287, E-1294, E-1285
  Confirmed (1): E-1294
  Field updates: 4

327 tests pass. Verification deferred to land+install — exercise: `endless session activity` (from any pane resolvable to the current Claude session) should print the same shape; prefix+e → `a` opens it in a popup.