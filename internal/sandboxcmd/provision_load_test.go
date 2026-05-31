package sandboxcmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProvision_CreatesDirAndMeta pins the happy path: Provision with a
// fresh name lays down the sandbox dir under sandboxesDir() and writes a
// .sandbox-meta.json with the supplied mode + the calling process's pid.
// The seam is XDG_CACHE_HOME — sandboxesDir() composes monitor.CacheDir()
// from $XDG_CACHE_HOME so a per-test TempDir isolates this and other
// tests that touch sandboxes.
func TestProvision_CreatesDirAndMeta(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)

	sb, err := Provision("test-prov-happy", modeKeep)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Cleanup(func() { _ = sb.Destroy() })

	if sb.Name != "test-prov-happy" {
		t.Errorf("Name = %q, want test-prov-happy", sb.Name)
	}
	wantDir := filepath.Join(tmp, "endless", "sandboxes", "test-prov-happy")
	if sb.Dir != wantDir {
		t.Errorf("Dir = %q, want %q", sb.Dir, wantDir)
	}
	if _, err := os.Stat(filepath.Join(sb.Dir, metaFilename)); err != nil {
		t.Errorf("meta file missing: %v", err)
	}
	if sb.Meta.Mode != modeKeep {
		t.Errorf("Meta.Mode = %q, want %q", sb.Meta.Mode, modeKeep)
	}
	if sb.Meta.CreatorPID != os.Getpid() {
		t.Errorf("Meta.CreatorPID = %d, want test pid %d", sb.Meta.CreatorPID, os.Getpid())
	}
}

// TestProvision_RejectsDuplicateName pins the documented refusal: a
// second Provision call for a name that already has a sandbox returns
// an error rather than silently overwriting the existing meta. The
// error mentions the destroy command so a user has the next step.
func TestProvision_RejectsDuplicateName(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)

	first, err := Provision("test-dup", modeKeep)
	if err != nil {
		t.Fatalf("first Provision: %v", err)
	}
	t.Cleanup(func() { _ = first.Destroy() })

	_, err = Provision("test-dup", modeKeep)
	if err == nil {
		t.Fatal("second Provision returned nil, want duplicate error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error %q missing 'already exists'", err)
	}
	if !strings.Contains(err.Error(), "destroy") {
		t.Errorf("error %q missing 'destroy' hint", err)
	}
}

// TestProvision_RejectsInvalidName pins validateName's gate: names that
// would escape sandboxesDir() (path separators) or break filesystem
// semantics (".", "..") are refused before any directory is created.
func TestProvision_RejectsInvalidName(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)
	tests := []string{".", "..", "foo/bar", "foo\\bar"}
	for _, name := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Provision(name, modeKeep)
			if err == nil {
				t.Errorf("Provision(%q) returned nil, want error", name)
			}
		})
	}
}

// TestProvision_EmptyNameGeneratesRandom pins the auto-name branch:
// Provision with name="" generates a random hex name (the sandbox is
// usable; the name is non-empty and valid).
func TestProvision_EmptyNameGeneratesRandom(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)

	sb, err := Provision("", modeKeep)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Cleanup(func() { _ = sb.Destroy() })

	if sb.Name == "" {
		t.Error("auto-name returned empty Name")
	}
	if strings.ContainsAny(sb.Name, `/\`) {
		t.Errorf("auto-name %q contains path separator", sb.Name)
	}
}

// TestProvision_StaleDirReportsDistinctError pins the recovery-friendly
// distinction: a directory at the sandbox path WITHOUT a meta file (left
// behind by an interrupted enter) is reported with different wording
// than a fully-provisioned duplicate, so the user knows to destroy it
// rather than assume a healthy sandbox.
func TestProvision_StaleDirReportsDistinctError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)

	// Hand-create a stale sandbox dir with NO meta file.
	if err := os.MkdirAll(filepath.Join(tmp, "endless", "sandboxes", "test-stale"), 0o755); err != nil {
		t.Fatalf("mkdir stale: %v", err)
	}

	_, err := Provision("test-stale", modeKeep)
	if err == nil {
		t.Fatal("Provision on stale dir returned nil, want error")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Errorf("error %q missing 'stale' wording", err)
	}
}

// TestLoad_ReturnsProvisionedSandbox pins the round-trip: a sandbox
// provisioned by Provision is returned by Load with the same Name, Dir,
// and Meta. This is the read-side guarantee that callers like the
// destroy and bind paths rely on.
func TestLoad_ReturnsProvisionedSandbox(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)

	want, err := Provision("test-load-rt", modePersistent)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Cleanup(func() { _ = want.Destroy() })

	got, err := Load("test-load-rt")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Name != want.Name {
		t.Errorf("Name = %q, want %q", got.Name, want.Name)
	}
	if got.Dir != want.Dir {
		t.Errorf("Dir = %q, want %q", got.Dir, want.Dir)
	}
	if got.Meta.Mode != modePersistent {
		t.Errorf("Meta.Mode = %q, want %q", got.Meta.Mode, modePersistent)
	}
	if got.Meta.CreatorPID != want.Meta.CreatorPID {
		t.Errorf("Meta.CreatorPID = %d, want %d", got.Meta.CreatorPID, want.Meta.CreatorPID)
	}
}

// TestLoad_MissingSandboxReturnsError pins the documented refusal for
// unknown names: callers must distinguish "absent" from real I/O errors,
// and the message names the sandbox so users have actionable diagnostics.
func TestLoad_MissingSandboxReturnsError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)

	_, err := Load("does-not-exist")
	if err == nil {
		t.Fatal("Load on missing sandbox returned nil, want error")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error %q missing 'does not exist'", err)
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error %q missing the sandbox name", err)
	}
}

// TestLoad_CorruptMetaReturnsError pins the parse-failure branch: a
// sandbox directory whose meta file contains malformed JSON surfaces an
// error rather than silently producing a zero-Meta Sandbox.
func TestLoad_CorruptMetaReturnsError(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)

	dir := filepath.Join(tmp, "endless", "sandboxes", "test-corrupt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, metaFilename), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write bad meta: %v", err)
	}

	_, err := Load("test-corrupt")
	if err == nil {
		t.Fatal("Load on corrupt meta returned nil, want parse error")
	}
}

// TestLoad_MetaCarriesProvisionedFields pins the byte-exact round-trip
// of the JSON meta: every Meta field Provision sets survives a Load via
// the on-disk JSON. Guards against silent field-name drift between the
// struct tags and the persisted shape.
func TestLoad_MetaCarriesProvisionedFields(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmp)

	sb, err := Provision("test-meta-fields", modeEphemeral)
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	t.Cleanup(func() { _ = sb.Destroy() })

	// Verify the on-disk JSON has every field we expect, with the
	// snake_case tags declared on SandboxMeta.
	data, err := os.ReadFile(filepath.Join(sb.Dir, metaFilename))
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal meta: %v", err)
	}
	for _, field := range []string{"created_at", "mode", "creator_pid", "name"} {
		if _, ok := raw[field]; !ok {
			t.Errorf("meta JSON missing field %q: %s", field, data)
		}
	}
	if raw["mode"] != string(modeEphemeral) {
		t.Errorf("on-disk mode = %v, want %q", raw["mode"], modeEphemeral)
	}
	if raw["name"] != "test-meta-fields" {
		t.Errorf("on-disk name = %v, want test-meta-fields", raw["name"])
	}
}
