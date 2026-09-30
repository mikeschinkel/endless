Python has 7+ readers in src/endless/config.py, event_bridge.py, register.py, reconcile.py plus tests, all using direct json.load.

They currently work for the unchanged-config case but will diverge from Go's merge rules for any layered field (today: checks and tracking).
