package verify_test

import (
	"reflect"
	"testing"

	"github.com/mikeschinkel/endless/internal/verify"
)

// taskManifest builds a per-task Manifest for merge tests.
func taskManifest() *verify.Manifest {
	return &verify.Manifest{
		Schema: 1,
		Task:   "E-1234",
		Runner: "go test ./.endless/tasks/E-1234/...",
		Format: verify.FormatGotestJSON,
	}
}

func TestMerge_NilProjectIsIdentity(t *testing.T) {
	task := taskManifest()
	task.Setup = []string{"task-step"}
	eff := verify.Merge(nil, task)
	if !reflect.DeepEqual(eff.Setup, []string{"task-step"}) {
		t.Errorf("Setup = %v, want [task-step]", eff.Setup)
	}
	if eff.Runner != task.Runner || eff.Task != task.Task || eff.Format != task.Format {
		t.Errorf("identity merge changed a per-task field: %+v", eff)
	}
}

func TestMerge_SetupProjectFirstThenTask(t *testing.T) {
	pc := &verify.ProjectConfig{Schema: 1, Setup: []string{"just build", "schema-init"}}
	task := taskManifest()
	task.Setup = []string{"task-setup"}

	eff := verify.Merge(pc, task)
	want := []string{"just build", "schema-init", "task-setup"}
	if !reflect.DeepEqual(eff.Setup, want) {
		t.Errorf("Setup = %v, want %v", eff.Setup, want)
	}
}

func TestMerge_SeedProjectFirstThenTask(t *testing.T) {
	pc := &verify.ProjectConfig{Schema: 1, Seed: []string{"fixtures/shared.json"}}
	task := taskManifest()
	task.Seed = []string{"fixtures/task.json"}

	eff := verify.Merge(pc, task)
	want := []string{"fixtures/shared.json", "fixtures/task.json"}
	if !reflect.DeepEqual(eff.Seed, want) {
		t.Errorf("Seed = %v, want %v", eff.Seed, want)
	}
}

func TestMerge_FormatTaskOverridesProjectDefault(t *testing.T) {
	pc := &verify.ProjectConfig{Schema: 1, Format: verify.FormatTAP}

	// Task sets its own format -> task wins.
	task := taskManifest() // Format = gotest-json
	if eff := verify.Merge(pc, task); eff.Format != verify.FormatGotestJSON {
		t.Errorf("Format = %q, want task value %q", eff.Format, verify.FormatGotestJSON)
	}

	// Task omits format -> inherit project default.
	bare := taskManifest()
	bare.Format = ""
	if eff := verify.Merge(pc, bare); eff.Format != verify.FormatTAP {
		t.Errorf("Format = %q, want project default %q", eff.Format, verify.FormatTAP)
	}
}

func TestMerge_NeedsDefaultVsExplicitEmptyOverride(t *testing.T) {
	pc := &verify.ProjectConfig{Schema: 1, Needs: []string{"docker"}}

	// Task omits needs (nil) -> inherit project default.
	bare := taskManifest()
	bare.Needs = nil
	if eff := verify.Merge(pc, bare); !reflect.DeepEqual(eff.Needs, []string{"docker"}) {
		t.Errorf("Needs = %v, want inherited [docker]", eff.Needs)
	}

	// Task sets needs = [] (explicit empty) -> override default down to none.
	override := taskManifest()
	override.Needs = []string{}
	eff := verify.Merge(pc, override)
	if eff.Needs == nil || len(eff.Needs) != 0 {
		t.Errorf("Needs = %v, want explicit empty (override), not inherited default", eff.Needs)
	}

	// Task sets its own needs -> task wins.
	own := taskManifest()
	own.Needs = []string{"postgres"}
	if eff := verify.Merge(pc, own); !reflect.DeepEqual(eff.Needs, []string{"postgres"}) {
		t.Errorf("Needs = %v, want task value [postgres]", eff.Needs)
	}
}

// Merge must not mutate either input slice.
func TestMerge_DoesNotMutateInputs(t *testing.T) {
	pc := &verify.ProjectConfig{Schema: 1, Setup: []string{"a"}, Seed: []string{"s"}}
	task := taskManifest()
	task.Setup = []string{"b"}
	task.Seed = []string{"t"}

	_ = verify.Merge(pc, task)

	if !reflect.DeepEqual(pc.Setup, []string{"a"}) || !reflect.DeepEqual(pc.Seed, []string{"s"}) {
		t.Errorf("project config mutated: setup=%v seed=%v", pc.Setup, pc.Seed)
	}
	if !reflect.DeepEqual(task.Setup, []string{"b"}) || !reflect.DeepEqual(task.Seed, []string{"t"}) {
		t.Errorf("task manifest mutated: setup=%v seed=%v", task.Setup, task.Seed)
	}
}
