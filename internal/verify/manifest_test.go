package verify_test

import (
	"errors"
	"testing"

	"github.com/mikeschinkel/endless/internal/verify"
)

const validManifest = `
schema = 1
task   = "E-1234"
runner = "go test ./.endless/tasks/E-1234/..."
format = "gotest-json"
setup  = ["just build", ".endless/tasks/E-1234/setup.sh"]
tiers  = ["smoke", "full"]
seed   = ["fixtures/baseline.json"]
needs  = []
`

const minimalManifest = `
schema = 1
task   = "E-1234"
runner = "go test ./..."
format = "tap"
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
	if m.Runner != "go test ./.endless/tasks/E-1234/..." {
		t.Errorf("Runner = %q", m.Runner)
	}
	if m.Format != verify.FormatGotestJSON {
		t.Errorf("Format = %q, want %q", m.Format, verify.FormatGotestJSON)
	}
	if len(m.Tiers) != 2 || m.Tiers[0] != "smoke" || m.Tiers[1] != "full" {
		t.Errorf("Tiers = %v, want [smoke full]", m.Tiers)
	}
	if len(m.Seed) != 1 || m.Seed[0] != "fixtures/baseline.json" {
		t.Errorf("Seed = %v, want [fixtures/baseline.json]", m.Seed)
	}
	if len(m.Setup) != 2 || m.Setup[0] != "just build" || m.Setup[1] != ".endless/tasks/E-1234/setup.sh" {
		t.Errorf("Setup = %v, want [just build .endless/tasks/E-1234/setup.sh]", m.Setup)
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
	if m.Format != verify.FormatTAP {
		t.Errorf("Format = %q, want %q", m.Format, verify.FormatTAP)
	}
	if len(m.Setup) != 0 || len(m.Tiers) != 0 || len(m.Seed) != 0 || len(m.Needs) != 0 {
		t.Errorf("optional fields should be empty: setup=%v tiers=%v seed=%v needs=%v", m.Setup, m.Tiers, m.Seed, m.Needs)
	}
}

func TestParseManifest_MissingRequiredField(t *testing.T) {
	cases := map[string]string{
		"missing schema": `task = "E-1"
runner = "go test ./..."
format = "tap"`,
		"missing task": `schema = 1
runner = "go test ./..."
format = "tap"`,
		"missing runner": `schema = 1
task = "E-1"
format = "tap"`,
		"missing format": `schema = 1
task = "E-1"
runner = "go test ./..."`,
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

func TestParseManifest_UnknownFormat(t *testing.T) {
	const toml = `schema = 1
task = "E-1"
runner = "go test ./..."
format = "junit-xml"`
	_, err := verify.ParseManifest([]byte(toml))
	if err == nil {
		t.Fatal("ParseManifest accepted unknown format")
	}
	if !errors.Is(err, verify.ErrUnknownFormat) {
		t.Errorf("error did not wrap ErrUnknownFormat: %v", err)
	}
}

func TestParseManifest_UnknownSchemaVersion(t *testing.T) {
	const toml = `schema = 2
task = "E-1"
runner = "go test ./..."
format = "tap"`
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
runner = "go test ./..."
format = "tap"
runer = "typo"`
	_, err := verify.ParseManifest([]byte(toml))
	if err == nil {
		t.Fatal("ParseManifest accepted unknown key")
	}
	if !errors.Is(err, verify.ErrUnknownManifestKeys) {
		t.Errorf("error did not wrap ErrUnknownManifestKeys: %v", err)
	}
}

func TestParseManifest_MalformedTOML(t *testing.T) {
	const toml = `schema = 1
task = "E-1
format =`
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
