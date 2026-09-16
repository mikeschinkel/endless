package sandboxcmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
)

// Reading and writing a Claude settings file as JSON.
//
// These lived in bind.go until E-1964 deleted `sandbox bind` along with the
// XDG_CONFIG_HOME injection it wrote. They outlived that command because
// `sandbox claude-settings-repair` (E-1347) reads and rewrites the same files —
// and repairing what bind left behind is now the only reason this package
// touches them at all.

func readSettings(path string) (map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	out := map[string]any{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return out, nil
}

func writeSettings(path string, settings map[string]any) error {
	// Deterministic key order; settings.json is small enough that custom
	// marshaling isn't worth it for everything, but env-block keys benefit
	// from sorting so reruns produce byte-identical output.
	if env, ok := settings["env"].(map[string]any); ok {
		settings["env"] = sortedMap(env)
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling settings: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("renaming %s -> %s: %w", tmp, path, err)
	}
	return nil
}

// sortedMap returns a map with keys in deterministic order via a JSON-marshal
// wrapper. json.Marshal sorts map[string]any keys alphabetically by default
// in Go's encoding/json, so we just need a typed alias to clarify intent.
func sortedMap(m map[string]any) map[string]any {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]any, len(m))
	for _, k := range keys {
		out[k] = m[k]
	}
	return out
}
