// Package matchers loads matcher patterns (verbs, pivots, action regexes)
// from project + machine config files. Mirrors the Python format in
// src/endless/matchers.py.
//
// Read-only: this package never writes config. Mutations go through the
// Python CLI ('endless phrase ...').
package matchers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// Matcher is one entry from the "matchers" array in config.json.
// Match holds either []string (for exact/substring) or string (for regex).
type Matcher struct {
	Type          string      `json:"type"`
	Scope         string      `json:"scope,omitempty"`
	Method        string      `json:"method"`
	Match         interface{} `json:"match"`
	CaseSensitive bool        `json:"case_sensitive,omitempty"`
	Enabled       *bool       `json:"enabled,omitempty"` // nil = default true
}

// IsEnabled returns true unless explicitly set to false.
func (m Matcher) IsEnabled() bool {
	return m.Enabled == nil || *m.Enabled
}

// MatchString returns the regex pattern as a string when method=regex,
// or "" otherwise.
func (m Matcher) MatchString() string {
	s, ok := m.Match.(string)
	if !ok {
		return ""
	}
	return s
}

// MatchList returns the match values as []string when method=exact/substring,
// or nil otherwise.
func (m Matcher) MatchList() []string {
	raw, ok := m.Match.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

type configFile struct {
	Matchers []Matcher `json:"matchers,omitempty"`
}

// Load returns project + machine matchers merged in that order. Project
// path is resolved via the project's registered path in the DB (so it
// works correctly from inside a worktree, per E-972).
func Load(projectID int64) ([]Matcher, error) {
	machine, err := loadFile(machineConfigPath())
	if err != nil {
		return nil, fmt.Errorf("machine matchers: %w", err)
	}

	project := []Matcher{}
	if projectID > 0 {
		root, err := monitor.ProjectPath(projectID)
		if err == nil && root != "" {
			project, err = loadFile(filepath.Join(root, ".endless", "config.json"))
			if err != nil {
				return nil, fmt.Errorf("project matchers: %w", err)
			}
		}
	}

	// Project entries appear first; merging into a single slice is fine
	// for the consumers here (ActionRegex finds first match by type+scope).
	out := make([]Matcher, 0, len(project)+len(machine))
	out = append(out, project...)
	out = append(out, machine...)
	return out, nil
}

func loadFile(path string) ([]Matcher, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var c configFile
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return c.Matchers, nil
}

func machineConfigPath() string {
	return filepath.Join(monitor.ConfigDir(), "config.json")
}

// GetPivotMatchers returns the subset of matchers with type="pivot"
// that are enabled. Used by the UserPromptSubmit hook (E-971 Layer E).
func GetPivotMatchers(all []Matcher) []Matcher {
	out := make([]Matcher, 0)
	for _, m := range all {
		if m.Type == "pivot" && m.IsEnabled() {
			out = append(out, m)
		}
	}
	return out
}

// FindPivotMatch returns the first phrase from any enabled pivot
// matcher that substring-matches text, or "" if no match. Honors
// case_sensitive per matcher (default: case-insensitive substring).
// Supports method="substring" and method="exact"; "regex" pivots are
// not supported in v1 since live config uses substrings only.
func FindPivotMatch(all []Matcher, text string) string {
	if text == "" {
		return ""
	}
	lower := strings.ToLower(text)
	for _, m := range GetPivotMatchers(all) {
		if m.Method != "substring" && m.Method != "exact" {
			continue
		}
		for _, phrase := range m.MatchList() {
			if phrase == "" {
				continue
			}
			if m.CaseSensitive {
				if strings.Contains(text, phrase) {
					return phrase
				}
			} else {
				if strings.Contains(lower, strings.ToLower(phrase)) {
					return phrase
				}
			}
		}
	}
	return ""
}

// ActionRegex finds the first enabled regex matcher with the given
// (type, scope) and returns its compiled pattern. Returns nil if no
// matcher exists or if compilation fails.
func ActionRegex(all []Matcher, action, scope string) *regexp.Regexp {
	for _, m := range all {
		if m.Type != action || m.Scope != scope {
			continue
		}
		if !m.IsEnabled() {
			continue
		}
		if m.Method != "regex" {
			continue
		}
		pattern := m.MatchString()
		if pattern == "" {
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		return re
	}
	return nil
}
