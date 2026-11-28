package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoModHasNoReplace fails when Endless's go.mod holds a replace directive
// (E-2523). Go ignores a dependency's replace lines, and a relative replace
// resolves only on a machine with the target checkout beside this one, so a
// replace makes the build depend on one developer's filesystem. Develop
// against a sibling checkout through go.work instead, which go.mod never sees.
func TestGoModHasNoReplace(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}
	modFile, err := filepath.Abs(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("resolving go.mod: %v", err)
	}
	// `go mod edit -json` parses go.mod with the toolchain's own parser, so a
	// replace in any form (single line, block, versioned) is caught.
	out, err := exec.Command(goBin, "mod", "edit", "-json", modFile).Output()
	if err != nil {
		t.Fatalf("go mod edit -json %s: %v", modFile, err)
	}
	var mod struct {
		Replace []struct {
			Old struct{ Path, Version string }
			New struct{ Path, Version string }
		}
	}
	err = json.Unmarshal(out, &mod)
	if err != nil {
		t.Fatalf("parsing go mod edit -json output: %v", err)
	}
	if len(mod.Replace) == 0 {
		return
	}
	lines := make([]string, 0, len(mod.Replace))
	for _, r := range mod.Replace {
		lines = append(lines, "  "+r.Old.Path+" => "+r.New.Path)
	}
	t.Errorf("%s holds %d replace directive(s):\n%s\n\n"+
		"Require the module's published tag instead. To develop against a local\n"+
		"checkout, put the replace in go.work, which go.mod never sees.",
		modFile, len(mod.Replace), strings.Join(lines, "\n"))
}
