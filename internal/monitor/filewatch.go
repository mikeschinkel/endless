package monitor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// SkipDirs are directories to ignore when scanning for changes.
var SkipDirs = map[string]bool{
	".git":         true,
	".endless":     true,
	"vendor":       true,
	"node_modules": true,
	"__pycache__":  true,
	".venv":        true,
	".claude":      true,
	".pytest_cache": true,
}

// FileChange represents a detected file change.
type FileChange struct {
	RelativePath string
	ChangeType   string // "new", "modified", "deleted"
}

// WatchConfig controls which files to track.
type WatchConfig struct {
	Include []string // glob patterns to include (empty = all)
	Exclude []string // glob patterns to exclude
}

// DefaultWatchConfig returns the default config (track all files).
func DefaultWatchConfig() WatchConfig {
	return WatchConfig{}
}

// LoadWatchConfig reads watch patterns from .endless/config.json.
func LoadWatchConfig(projectPath string) WatchConfig {
	cfg := DefaultWatchConfig()

	configPath := filepath.Join(projectPath, ".endless", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return cfg
	}

	var parsed struct {
		Watch struct {
			Include []string `json:"include"`
			Exclude []string `json:"exclude"`
		} `json:"watch"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return cfg
	}

	if len(parsed.Watch.Include) > 0 {
		cfg.Include = parsed.Watch.Include
	}
	if len(parsed.Watch.Exclude) > 0 {
		cfg.Exclude = parsed.Watch.Exclude
	}

	return cfg
}

// shouldTrack checks if a file matches the watch config.
func (wc WatchConfig) shouldTrack(relPath string) bool {
	base := filepath.Base(relPath)

	// Check excludes first
	for _, pattern := range wc.Exclude {
		if matched, _ := filepath.Match(pattern, base); matched {
			return false
		}
		if matched, _ := filepath.Match(pattern, relPath); matched {
			return false
		}
	}

	// If includes are specified, file must match one
	if len(wc.Include) > 0 {
		for _, pattern := range wc.Include {
			if matched, _ := filepath.Match(pattern, base); matched {
				return true
			}
			if matched, _ := filepath.Match(pattern, relPath); matched {
				return true
			}
		}
		return false
	}

	// No includes specified → track everything
	return true
}

// DetectFileChanges compares current file mtimes against the last
// recorded state in file_changes. Returns new changes found.
func DetectFileChanges(projectID int64, projectPath string) ([]FileChange, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}

	watchCfg := LoadWatchConfig(projectPath)

	// Get the latest snapshot: most recent file_change per path
	rows, err := db.Query(
		`SELECT relative_path, change_type, detected_at
		 FROM file_changes
		 WHERE project_id = ?
		   AND id IN (
		     SELECT MAX(id) FROM file_changes
		     WHERE project_id = ?
		     GROUP BY relative_path
		   )`,
		projectID, projectID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	// known tracks the last recorded state per file
	type fileState struct {
		changeType string
		detectedAt string
	}
	known := make(map[string]fileState)
	for rows.Next() {
		var relPath, changeType, detectedAt string
		if err := rows.Scan(&relPath, &changeType, &detectedAt); err != nil {
			continue
		}
		known[relPath] = fileState{changeType, detectedAt}
	}

	var changes []FileChange
	seen := make(map[string]bool)

	err = filepath.Walk(projectPath, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if info.IsDir() {
			if SkipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		rel, err := filepath.Rel(projectPath, path)
		if err != nil {
			return nil
		}

		if !watchCfg.shouldTrack(rel) {
			return nil
		}

		seen[rel] = true

		modTime := info.ModTime().UTC().Format("2006-01-02T15:04:05")
		state, exists := known[rel]

		if !exists {
			// Never seen before
			changes = append(changes, FileChange{
				RelativePath: rel,
				ChangeType:   "new",
			})
		} else if state.changeType == "deleted" {
			// Was deleted, now back
			changes = append(changes, FileChange{
				RelativePath: rel,
				ChangeType:   "new",
			})
		} else if modTime > state.detectedAt {
			// Modified since last detection
			changes = append(changes, FileChange{
				RelativePath: rel,
				ChangeType:   "modified",
			})
		}

		return nil
	})
	if err != nil {
		return changes, err
	}

	// Check for deleted files (were tracked, now gone)
	for relPath, state := range known {
		if state.changeType == "deleted" {
			continue // already recorded as deleted
		}
		if !seen[relPath] {
			changes = append(changes, FileChange{
				RelativePath: relPath,
				ChangeType:   "deleted",
			})
		}
	}

	return changes, nil
}

// RecordFileChanges writes detected changes to the file_changes table.
func RecordFileChanges(projectID int64, changes []FileChange, source string) error {
	if len(changes) == 0 {
		return nil
	}

	db, err := DB()
	if err != nil {
		return err
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	for _, c := range changes {
		_, err := db.Exec(
			"INSERT INTO file_changes "+
				"(project_id, relative_path, change_type, detected_at, source) "+
				"VALUES (?, ?, ?, ?, ?)",
			projectID, c.RelativePath, c.ChangeType, now, source,
		)
		if err != nil {
			return err
		}
	}
	return nil
}
