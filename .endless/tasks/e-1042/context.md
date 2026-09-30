E-787 added ENDLESS_AUTO_MIGRATE env var gate in src/endless/db.py.

Default is '1' (auto-migrate, preserves prior behavior).

Setting to '0' in shell defers migrations — useful for editable-install dev workflows where you don't want source edits to silently migrate the prod DB the moment you run an endless command.

Tests force '1' via conftest.

Surfaced during E-787 Phase 5.
