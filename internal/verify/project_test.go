package verify_test

import (
	"errors"
	"testing"

	"github.com/mikeschinkel/endless/internal/verify"
)

const validProjectConfig = `
schema   = 1
setup    = ["just build", ".endless/verify/setup.sh"]
teardown = ["docker compose down"]
seed     = ["fixtures/shared.json"]
needs    = []
`

func TestParseProjectConfig_ValidFull(t *testing.T) {
	pc, err := verify.ParseProjectConfig([]byte(validProjectConfig))
	if err != nil {
		t.Fatalf("ParseProjectConfig returned error: %v", err)
	}
	if pc.Schema != 1 {
		t.Errorf("Schema = %d, want 1", pc.Schema)
	}
	if len(pc.Setup) != 2 || pc.Setup[0] != "just build" || pc.Setup[1] != ".endless/verify/setup.sh" {
		t.Errorf("Setup = %v, want [just build .endless/verify/setup.sh]", pc.Setup)
	}
	if len(pc.Teardown) != 1 || pc.Teardown[0] != "docker compose down" {
		t.Errorf("Teardown = %v, want [docker compose down]", pc.Teardown)
	}
	if len(pc.Seed) != 1 || pc.Seed[0] != "fixtures/shared.json" {
		t.Errorf("Seed = %v, want [fixtures/shared.json]", pc.Seed)
	}
	if pc.Needs == nil {
		t.Errorf("Needs = nil, want non-nil empty slice (explicit needs = [])")
	}
	if len(pc.Needs) != 0 {
		t.Errorf("Needs = %v, want []", pc.Needs)
	}
}

func TestParseProjectConfig_MinimalSchemaOnly(t *testing.T) {
	pc, err := verify.ParseProjectConfig([]byte("schema = 1\n"))
	if err != nil {
		t.Fatalf("ParseProjectConfig returned error for schema-only config: %v", err)
	}
	if len(pc.Setup) != 0 || len(pc.Teardown) != 0 || len(pc.Seed) != 0 || len(pc.Needs) != 0 {
		t.Errorf("optional fields should be empty: %+v", pc)
	}
}

func TestParseProjectConfig_MissingSchema(t *testing.T) {
	_, err := verify.ParseProjectConfig([]byte(`setup = ["just build"]`))
	if err == nil {
		t.Fatal("ParseProjectConfig accepted config with no schema")
	}
	if !errors.Is(err, verify.ErrMissingField) {
		t.Errorf("error did not wrap ErrMissingField: %v", err)
	}
	if !errors.Is(err, verify.ErrInvalidProjectConfig) {
		t.Errorf("error did not wrap ErrInvalidProjectConfig: %v", err)
	}
}

func TestParseProjectConfig_UnknownSchema(t *testing.T) {
	_, err := verify.ParseProjectConfig([]byte("schema = 2\n"))
	if err == nil {
		t.Fatal("ParseProjectConfig accepted unknown schema version")
	}
	if !errors.Is(err, verify.ErrUnknownSchema) {
		t.Errorf("error did not wrap ErrUnknownSchema: %v", err)
	}
}

// The project config carries no per-task fields and no format (format is now a
// per-check concern). runner/task/tiers/checks/format must all be rejected as
// unknown keys so a misplaced field is caught loudly.
func TestParseProjectConfig_RejectsNonProjectKeys(t *testing.T) {
	cases := map[string]string{
		"runner": "schema = 1\nrunner = \"go test ./...\"\n",
		"task":   "schema = 1\ntask = \"E-1\"\n",
		"tiers":  "schema = 1\ntiers = [\"smoke\"]\n",
		"format": "schema = 1\nformat = \"tap\"\n",
	}
	for name, toml := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := verify.ParseProjectConfig([]byte(toml))
			if err == nil {
				t.Fatalf("ParseProjectConfig accepted non-project key %q", name)
			}
			if !errors.Is(err, verify.ErrUnknownManifestKeys) {
				t.Errorf("error did not wrap ErrUnknownManifestKeys: %v", err)
			}
		})
	}
}
