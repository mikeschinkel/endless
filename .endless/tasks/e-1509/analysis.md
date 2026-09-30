Fix: point both functions at shutil.which('endless-go'), build argv as ['endless-go', 'event', 'apply-change'|'backup', ...]; trim error messages to identify the missing binary without 'Build it' instructions (Justfile concern, not user-facing).

Scope is event_bridge.py only; the Go-side eventcmd.apply-change verb already works correctly.
