package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProvisionRootBlocksEscape exercises the real Provision code path and
// asserts that os.Root prevents file operations from escaping the sandbox dir.
func TestProvisionRootBlocksEscape(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	t.Setenv("HOME", tmp)

	sb, err := Provision("test-escape", modeEphemeral)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Cleanup(func() { _ = sb.Destroy() })

	root := sb.Root()
	cases := []struct {
		name string
		path string
	}{
		{"parent_relative", "../escape.txt"},
		{"absolute", "/etc/passwd-fake"},
		{"deep_relative", "../../escape.txt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := root.Create(tc.path)
			if err == nil {
				_ = f.Close()
				t.Fatalf("expected Create(%q) via Root to fail, got nil", tc.path)
			}
		})
	}

	// Sanity: legitimate writes succeed.
	f, err := root.Create("legit.txt")
	if err != nil {
		t.Fatalf("legit Create failed: %v", err)
	}
	_ = f.Close()
	if _, err := os.Stat(filepath.Join(sb.Dir, "legit.txt")); err != nil {
		t.Fatalf("legit file not visible: %v", err)
	}

	// Meta file written via Root should exist.
	if _, err := os.Stat(filepath.Join(sb.Dir, metaFilename)); err != nil {
		t.Fatalf("meta file missing: %v", err)
	}
}

// TestProvisionCollision verifies named-sandbox collisions error cleanly.
func TestProvisionCollision(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	t.Setenv("HOME", tmp)

	sb1, err := Provision("dup", modeKeep)
	if err != nil {
		t.Fatalf("first Provision: %v", err)
	}
	t.Cleanup(func() { _ = sb1.Destroy() })

	if _, err := Provision("dup", modeKeep); err == nil {
		t.Fatal("expected collision error, got nil")
	}
}

// TestClassify verifies the live/in-use/orphaned label semantics.
func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		meta SandboxMeta
		want sandboxState
	}{
		{
			name: "keep_alive",
			meta: SandboxMeta{Mode: modeKeep, CreatorPID: os.Getpid()},
			want: stateInUse,
		},
		{
			name: "keep_dead_pid",
			meta: SandboxMeta{Mode: modeKeep, CreatorPID: 1},
			want: stateInUse,
		},
		{
			name: "ephemeral_alive",
			meta: SandboxMeta{Mode: modeEphemeral, CreatorPID: os.Getpid()},
			want: stateLive,
		},
		{
			name: "ephemeral_dead",
			// PID 0 is invalid; isAlive returns false for it.
			meta: SandboxMeta{Mode: modeEphemeral, CreatorPID: 0},
			want: stateOrphaned,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classify(tc.meta); got != tc.want {
				t.Fatalf("classify: got %s, want %s", got, tc.want)
			}
		})
	}
}
