Delete the existing bespoke os.ReadFile + json.Unmarshal implementation. Callsite is cmd/endless-hook/claude.go.

Behavior must be identical for any existing config file.
