Delete the existing bespoke reader (entire file becomes unnecessary; the default helper moves to internal/config/normalize.go per the parent plan). Callsite is cmd/endless-hook/claude.go.

With this in place, setting checks.drift_detection in either ~/.config/endless/config.json (global) OR <proj>/.endless/config.json (per-project) must work, with project key overriding global key (per-key merge confirmed by Mike).
