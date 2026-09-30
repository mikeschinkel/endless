E-1322's auto-capture handler is gated on Actor.Kind == ActorSession, but emit_event always sets Kind to one of cli/hook/web/system (1518 cli events vs zero session in the ledger).

Session attribution lives in the orthogonal Actor.SessionID field.

Root cause: E-1322 spec (in the spawn prompt) incorrectly required ActorSession kind; the implementer followed the spec exactly.
