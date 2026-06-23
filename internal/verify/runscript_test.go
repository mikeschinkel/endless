package verify_test

import (
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/verify"
)

func TestRenderRunScript_FullManifest(t *testing.T) {
	m, err := verify.ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	script := verify.RenderRunScript(m)

	mustContain := []string{
		"#!/usr/bin/env sh",
		"set -eu",
		"trap teardown EXIT",
		"docker compose down",
		"just build",
		".endless/tasks/E-1234/setup.sh",
		"go test -run '^(TestFoo|TestBar)$' ./internal/verify/...",
		"pytest tests/test_x.py::test_a",
		"bats ./.endless/tasks/E-1234/cli.bats",
	}
	for _, want := range mustContain {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q\n---\n%s", want, script)
		}
	}

	// Seed and needs are Endless-only and must NOT appear in the bare-clone script.
	if strings.Contains(script, "fixtures/baseline.json") {
		t.Errorf("script leaked a seed entry:\n%s", script)
	}

	// Ordering: setup before checks; teardown defined before setup (trap-based).
	setupIdx := strings.Index(script, "just build")
	checkIdx := strings.Index(script, "go test -run")
	teardownIdx := strings.Index(script, "trap teardown")
	if !(teardownIdx < setupIdx && setupIdx < checkIdx) {
		t.Errorf("expected teardown(trap) < setup < checks, got teardown=%d setup=%d check=%d\n%s",
			teardownIdx, setupIdx, checkIdx, script)
	}
}

// With no setup or teardown, the script still renders the checks and omits the
// trap machinery.
func TestRenderRunScript_Minimal(t *testing.T) {
	m, err := verify.ParseManifest([]byte(minimalManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	script := verify.RenderRunScript(m)

	if strings.Contains(script, "trap teardown") {
		t.Errorf("minimal manifest should emit no teardown trap:\n%s", script)
	}
	if !strings.Contains(script, "go test -run '^(TestFoo)$' ./...") {
		t.Errorf("minimal script missing the resolved gotest command:\n%s", script)
	}
}
