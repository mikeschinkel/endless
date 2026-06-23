package verify_test

import (
	"errors"
	"testing"

	"github.com/mikeschinkel/endless/internal/verify"
)

// A full manifest exercising both check forms plus every top-level field. Note
// the TOML ordering: all top-level keys precede the first [[check]] block.
const validManifest = `
schema   = 1
task     = "E-1234"
setup    = ["just build", ".endless/tasks/E-1234/setup.sh"]
teardown = ["docker compose down"]
tiers    = ["smoke", "full"]
seed     = ["fixtures/baseline.json"]
needs    = []

[[check]]
runner = "gotest"
tests  = ["TestFoo", "TestBar"]
paths  = ["./internal/verify/..."]

[[check]]
runner = "pytest"
tests  = ["tests/test_x.py::test_a"]

[[check]]
runner  = "bats"
command = "bats ./.endless/tasks/E-1234/cli.bats"
format  = "tap"
`

const minimalManifest = `
schema = 1
task   = "E-1234"

[[check]]
runner = "gotest"
tests  = ["TestFoo"]
`

func TestParseManifest_ValidFull(t *testing.T) {
	m, err := verify.ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("ParseManifest returned error: %v", err)
	}
	if m.Schema != 1 {
		t.Errorf("Schema = %d, want 1", m.Schema)
	}
	if m.Task != "E-1234" {
		t.Errorf("Task = %q, want %q", m.Task, "E-1234")
	}
	if len(m.Checks) != 3 {
		t.Fatalf("len(Checks) = %d, want 3", len(m.Checks))
	}
	if m.Checks[0].Runner != "gotest" || len(m.Checks[0].Tests) != 2 {
		t.Errorf("Checks[0] = %+v, want gotest with 2 tests", m.Checks[0])
	}
	if len(m.Checks[0].Paths) != 1 || m.Checks[0].Paths[0] != "./internal/verify/..." {
		t.Errorf("Checks[0].Paths = %v, want [./internal/verify/...]", m.Checks[0].Paths)
	}
	if m.Checks[2].Runner != "bats" || m.Checks[2].Command == "" || m.Checks[2].Format != verify.FormatTAP {
		t.Errorf("Checks[2] = %+v, want bats raw command + tap", m.Checks[2])
	}
	if len(m.Tiers) != 2 || m.Tiers[0] != "smoke" || m.Tiers[1] != "full" {
		t.Errorf("Tiers = %v, want [smoke full]", m.Tiers)
	}
	if len(m.Seed) != 1 || m.Seed[0] != "fixtures/baseline.json" {
		t.Errorf("Seed = %v, want [fixtures/baseline.json]", m.Seed)
	}
	if len(m.Setup) != 2 || m.Setup[0] != "just build" || m.Setup[1] != ".endless/tasks/E-1234/setup.sh" {
		t.Errorf("Setup = %v", m.Setup)
	}
	if len(m.Teardown) != 1 || m.Teardown[0] != "docker compose down" {
		t.Errorf("Teardown = %v, want [docker compose down]", m.Teardown)
	}
	if len(m.Needs) != 0 {
		t.Errorf("Needs = %v, want []", m.Needs)
	}
}

func TestParseManifest_ValidMinimal(t *testing.T) {
	m, err := verify.ParseManifest([]byte(minimalManifest))
	if err != nil {
		t.Fatalf("ParseManifest returned error: %v", err)
	}
	if len(m.Checks) != 1 || m.Checks[0].Runner != "gotest" {
		t.Errorf("Checks = %+v, want one gotest check", m.Checks)
	}
	if len(m.Setup) != 0 || len(m.Teardown) != 0 || len(m.Tiers) != 0 || len(m.Seed) != 0 || len(m.Needs) != 0 {
		t.Errorf("optional fields should be empty: %+v", m)
	}
}

func TestParseManifest_MissingRequiredField(t *testing.T) {
	cases := map[string]string{
		"missing schema": `task = "E-1"
[[check]]
runner = "gotest"
tests = ["TestX"]`,
		"missing task": `schema = 1
[[check]]
runner = "gotest"
tests = ["TestX"]`,
	}
	for name, toml := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := verify.ParseManifest([]byte(toml))
			if err == nil {
				t.Fatalf("ParseManifest accepted manifest with %s", name)
			}
			if !errors.Is(err, verify.ErrMissingField) {
				t.Errorf("error did not wrap ErrMissingField: %v", err)
			}
			if !errors.Is(err, verify.ErrInvalidManifest) {
				t.Errorf("error did not wrap ErrInvalidManifest: %v", err)
			}
		})
	}
}

func TestParseManifest_NoChecks(t *testing.T) {
	const toml = `schema = 1
task = "E-1"`
	_, err := verify.ParseManifest([]byte(toml))
	if err == nil {
		t.Fatal("ParseManifest accepted manifest with no checks")
	}
	if !errors.Is(err, verify.ErrNoChecks) {
		t.Errorf("error did not wrap ErrNoChecks: %v", err)
	}
}

// Per-check validation matrix: the two-form rules.
func TestParseManifest_CheckValidation(t *testing.T) {
	cases := []struct {
		name    string
		toml    string
		wantErr error
	}{
		{
			name: "tests on a raw runner is rejected",
			toml: `schema = 1
task = "E-1"
[[check]]
runner = "bats"
tests = ["whatever"]`,
			wantErr: verify.ErrTestsRequireFirstClass,
		},
		{
			name: "paths on a raw runner is rejected",
			toml: `schema = 1
task = "E-1"
[[check]]
runner = "bats"
paths = ["./x"]`,
			wantErr: verify.ErrPathsRequireFirstClass,
		},
		{
			name: "raw runner without command is rejected",
			toml: `schema = 1
task = "E-1"
[[check]]
runner = "bats"`,
			wantErr: verify.ErrRawCheckNeedsCommand,
		},
		{
			name: "raw runner with unknown format is rejected",
			toml: `schema = 1
task = "E-1"
[[check]]
runner = "bats"
command = "bats x"
format = "junit-xml"`,
			wantErr: verify.ErrUnknownFormat,
		},
		{
			name: "first-class with both tests and command is rejected",
			toml: `schema = 1
task = "E-1"
[[check]]
runner = "gotest"
tests = ["TestX"]
command = "go test ./..."`,
			wantErr: verify.ErrFirstClassCommandConflict,
		},
		{
			name: "first-class with neither selection nor command is rejected",
			toml: `schema = 1
task = "E-1"
[[check]]
runner = "gotest"`,
			wantErr: verify.ErrFirstClassNeedsSelection,
		},
		{
			name: "first-class with a mismatched explicit format is rejected",
			toml: `schema = 1
task = "E-1"
[[check]]
runner = "gotest"
tests = ["TestX"]
format = "tap"`,
			wantErr: verify.ErrFormatMismatch,
		},
		{
			name: "check without a runner is rejected",
			toml: `schema = 1
task = "E-1"
[[check]]
tests = ["TestX"]`,
			wantErr: verify.ErrCheckMissingRunner,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := verify.ParseManifest([]byte(tc.toml))
			if err == nil {
				t.Fatalf("ParseManifest accepted invalid check")
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("error did not wrap %v: %v", tc.wantErr, err)
			}
			if !errors.Is(err, verify.ErrInvalidManifest) {
				t.Errorf("error did not wrap ErrInvalidManifest: %v", err)
			}
		})
	}
}

// A first-class check may carry an explicit format if it equals the inferred
// value; that is accepted, and a command-mode first-class check is accepted too.
func TestParseManifest_FirstClassConsistentVariants(t *testing.T) {
	cases := map[string]string{
		"explicit matching format": `schema = 1
task = "E-1"
[[check]]
runner = "gotest"
tests = ["TestX"]
format = "gotest-json"`,
		"command-mode escape hatch": `schema = 1
task = "E-1"
[[check]]
runner = "gotest"
command = "go test -run TestX ./internal/foo/..."`,
		"paths-only selection": `schema = 1
task = "E-1"
[[check]]
runner = "gotest"
paths = ["./internal/verify/..."]`,
	}
	for name, toml := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := verify.ParseManifest([]byte(toml)); err != nil {
				t.Errorf("ParseManifest rejected a consistent first-class check: %v", err)
			}
		})
	}
}

func TestParseManifest_UnknownSchemaVersion(t *testing.T) {
	const toml = `schema = 2
task = "E-1"
[[check]]
runner = "gotest"
tests = ["TestX"]`
	_, err := verify.ParseManifest([]byte(toml))
	if err == nil {
		t.Fatal("ParseManifest accepted unknown schema version")
	}
	if !errors.Is(err, verify.ErrUnknownSchema) {
		t.Errorf("error did not wrap ErrUnknownSchema: %v", err)
	}
}

func TestParseManifest_UnknownKeysRejected(t *testing.T) {
	const toml = `schema = 1
task = "E-1"
runer = "typo"
[[check]]
runner = "gotest"
tests = ["TestX"]`
	_, err := verify.ParseManifest([]byte(toml))
	if err == nil {
		t.Fatal("ParseManifest accepted unknown key")
	}
	if !errors.Is(err, verify.ErrUnknownManifestKeys) {
		t.Errorf("error did not wrap ErrUnknownManifestKeys: %v", err)
	}
}

// A top-level key placed AFTER a [[check]] binds to that check table in TOML,
// where it is an unknown check key and must be rejected loudly. This guards the
// documented ordering constraint.
func TestParseManifest_TopLevelKeyAfterCheckRejected(t *testing.T) {
	const toml = `schema = 1
task = "E-1"
[[check]]
runner = "gotest"
tests = ["TestX"]
setup = ["just build"]`
	_, err := verify.ParseManifest([]byte(toml))
	if err == nil {
		t.Fatal("ParseManifest accepted a top-level key misplaced under [[check]]")
	}
	if !errors.Is(err, verify.ErrUnknownManifestKeys) {
		t.Errorf("error did not wrap ErrUnknownManifestKeys: %v", err)
	}
}

func TestParseManifest_MalformedTOML(t *testing.T) {
	const toml = `schema = 1
task = "E-1
[[check`
	_, err := verify.ParseManifest([]byte(toml))
	if err == nil {
		t.Fatal("ParseManifest accepted malformed TOML")
	}
	if !errors.Is(err, verify.ErrDecodingManifest) {
		t.Errorf("error did not wrap ErrDecodingManifest: %v", err)
	}
}

func TestFormat_Valid(t *testing.T) {
	valid := []verify.Format{verify.FormatGotestJSON, verify.FormatPytestJSON, verify.FormatTAP}
	for _, f := range valid {
		if !f.Valid() {
			t.Errorf("Format(%q).Valid() = false, want true", f)
		}
	}
	for _, f := range []verify.Format{"", "junit-xml", "GoTest-JSON"} {
		if f.Valid() {
			t.Errorf("Format(%q).Valid() = true, want false", f)
		}
	}
}
