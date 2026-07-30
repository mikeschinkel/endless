package spawnlaunchcmd

import (
	"encoding/json"
	"fmt"
	"os"
)

// LaunchSpec is the JSON contract the outer `spawn-window` writes and the inner
// `spawn-launch` reads. A struct (not a bare map) keeps the JSON field order
// deterministic and self-documenting. Every value the launch needs travels here
// so the tmux command line carries only the binary, the verb, and one
// shell-safe spec path — no handoff text, task title, or model string is ever
// shell-quoted onto a command line or leaked into the session environment.
type LaunchSpec struct {
	// ClaudeBin is the resolved real claude binary (no shell-function wrapper).
	ClaudeBin string `json:"claude_bin"`
	// HandoffFile holds the rendered handoff; its text becomes claude's
	// positional prompt. Deleted by spawn-launch after it is read.
	HandoffFile string `json:"handoff_file"`
	// PermissionMode is claude's --permission-mode (default "auto").
	PermissionMode string `json:"permission_mode"`
	// Model is claude's --model (optional; omitted from argv when empty).
	Model string `json:"model,omitempty"`
	// Name is claude's --name (optional; omitted from argv when empty).
	Name string `json:"name,omitempty"`
	// TaskID / ProjectID / SpawnedBy become the @endless_* window options
	// SessionStart reads to bind the session to its task.
	TaskID    string `json:"task_id"`
	ProjectID string `json:"project_id"`
	SpawnedBy string `json:"spawned_by"`
	// WindowName / Cwd describe the tmux window to create.
	WindowName string `json:"window_name"`
	Cwd        string `json:"cwd"`
}

// writeSpecFile marshals spec to a fresh temp file and returns its path. The
// caller (spawn-window) passes the path as the sole data on the new-window
// command line; spawn-launch deletes the file after reading it.
func writeSpecFile(spec LaunchSpec) (string, error) {
	f, err := os.CreateTemp("", "endless-launchspec-*.json")
	if err != nil {
		return "", fmt.Errorf("create launch-spec file: %w", err)
	}
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("marshal launch spec: %w", err)
	}
	if _, err = f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("write launch-spec file: %w", err)
	}
	if err = f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("close launch-spec file: %w", err)
	}
	return f.Name(), nil
}

// readSpecFile loads a launch spec written by writeSpecFile.
func readSpecFile(path string) (LaunchSpec, error) {
	var spec LaunchSpec
	data, err := os.ReadFile(path)
	if err != nil {
		return spec, fmt.Errorf("read launch-spec file %q: %w", path, err)
	}
	if err = json.Unmarshal(data, &spec); err != nil {
		return spec, fmt.Errorf("parse launch-spec file %q: %w", path, err)
	}
	return spec, nil
}
