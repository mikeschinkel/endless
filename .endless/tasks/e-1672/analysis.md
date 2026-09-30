Forward-only declarative ops (rename/add/remove/hoist/plunge/wrap/head/in/map/convert) + host-registered custom escape hatch (slice return for N to M).

Named+content-hash migration identity as manifest-of-references; events carry the applied-migration set; linear per-kind chain walk; integrity gates (hash mismatch / unregistered custom / missing referenced entry = hard error).

Ships with the golden-fixture CI harness for custom transforms (ADR-001).

No SQLite/CLI/ledger-IO.
