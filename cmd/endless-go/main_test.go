package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/endless/internal/settings"
)

// TestInitialize runs the startup step over exactly what this binary
// registers. Its failure is the development-time form of "every endless-go
// command refuses": a registration missing its rationale, a duplicate ID or a
// duplicate job name goes red here rather than on a user's machine (E-2283).
func TestInitialize(t *testing.T) {
	if err := initialize(); err != nil {
		t.Fatalf("initialize() = %v", err)
	}
}

// unregisteredKeys are config keys not yet in the settings registry (E-2284
// registered the opt-ins first; E-2303 does the rest). This list may only
// shrink: a new config key is registered, not added here.
var unregisteredKeys = []string{
	"dependencies",
	"description",
	"documents.rules",
	"ignore",
	"label",
	"language",
	"migrations.dirs",
	"name",
	"node_id",
	"ownership",
	"project_status.colors.epics.bg",
	"project_status.colors.epics.fg",
	"project_status.colors.other.bg",
	"project_status.colors.other.fg",
	"project_status.colors.urgent.bg",
	"project_status.colors.urgent.fg",
	"roots",
	"scan_interval",
	"status",
	"tracking",
}

// maxUnregistered pins the allowlist's size, so growing it shows up in a diff
// of this line rather than slipping in with an appended key. Lower it when
// you register a key; never raise it.
const maxUnregistered = 20

// TestEveryConfigKeyIsRegistered walks the config file schema: each leaf key
// is registered, or allowlisted above. A map leaf (`checks`) counts as
// registered when any of its keys is.
func TestEveryConfigKeyIsRegistered(t *testing.T) {
	if err := initialize(); err != nil {
		t.Fatal(err)
	}
	all, err := settings.All()
	if err != nil {
		t.Fatal(err)
	}
	registered := func(key string) bool {
		for _, s := range all {
			id := string(s.ID)
			if id == key || strings.HasPrefix(id, key+".") {
				return true
			}
		}
		return false
	}

	if len(unregisteredKeys) > maxUnregistered {
		t.Errorf("unregisteredKeys has %d entries, more than the pinned %d: register the new key instead",
			len(unregisteredKeys), maxUnregistered)
	}
	leaves := config.LeafKeys()
	for _, key := range leaves {
		listed := slices.Contains(unregisteredKeys, key)
		switch {
		case registered(key) && listed:
			t.Errorf("%s is registered and still allowlisted: remove it from unregisteredKeys and lower maxUnregistered", key)
		case !registered(key) && !listed:
			t.Errorf("config key %s is not registered: declare it in its feature's Settings()", key)
		}
	}
	for _, key := range unregisteredKeys {
		if !slices.Contains(leaves, key) {
			t.Errorf("unregisteredKeys lists %s, which the config schema no longer has", key)
		}
	}

	// And the other direction: nothing registers a key the schema lacks.
	for _, s := range all {
		id := string(s.ID)
		parent, _, _ := strings.Cut(id, ".")
		if !slices.Contains(leaves, id) && !slices.Contains(leaves, parent) {
			t.Errorf("setting %s names no key in the config schema", id)
		}
	}
}
