package verify_test

import (
	"reflect"
	"testing"

	"github.com/mikeschinkel/endless/internal/verify"
)

func TestCheck_IsFirstClass(t *testing.T) {
	cases := map[string]bool{
		"gotest": true,
		"pytest": true,
		"bats":   false,
		"":       false,
	}
	for runner, want := range cases {
		got := verify.Check{Runner: runner}.IsFirstClass()
		if got != want {
			t.Errorf("Check{Runner:%q}.IsFirstClass() = %v, want %v", runner, got, want)
		}
	}
}

func TestCheck_ResolvedFormat(t *testing.T) {
	cases := []struct {
		name  string
		check verify.Check
		want  verify.Format
	}{
		{"gotest infers gotest-json", verify.Check{Runner: "gotest", Tests: []string{"TestX"}}, verify.FormatGotestJSON},
		{"pytest infers pytest-json", verify.Check{Runner: "pytest", Tests: []string{"a::b"}}, verify.FormatPytestJSON},
		{"raw with declared format", verify.Check{Runner: "bats", Command: "bats x", Format: verify.FormatTAP}, verify.FormatTAP},
		{"raw without format defaults tap", verify.Check{Runner: "bats", Command: "bats x"}, verify.FormatTAP},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.check.ResolvedFormat(); got != tc.want {
				t.Errorf("ResolvedFormat() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheck_ResolvedCommand(t *testing.T) {
	cases := []struct {
		name  string
		check verify.Check
		want  string
	}{
		{
			name:  "gotest anchors test names and defaults to ./...",
			check: verify.Check{Runner: "gotest", Tests: []string{"TestFoo", "TestBar"}},
			want:  "go test -run '^(TestFoo|TestBar)$' ./...",
		},
		{
			name:  "gotest scopes packages via paths",
			check: verify.Check{Runner: "gotest", Tests: []string{"TestFoo"}, Paths: []string{"./internal/verify/...", "./internal/foo"}},
			want:  "go test -run '^(TestFoo)$' ./internal/verify/... ./internal/foo",
		},
		{
			name:  "gotest paths-only runs all tests in scope",
			check: verify.Check{Runner: "gotest", Paths: []string{"./internal/verify/..."}},
			want:  "go test ./internal/verify/...",
		},
		{
			name:  "gotest command-mode is literal",
			check: verify.Check{Runner: "gotest", Command: "go test -run TestX ./internal/foo/..."},
			want:  "go test -run TestX ./internal/foo/...",
		},
		{
			name:  "pytest joins paths then nodeids",
			check: verify.Check{Runner: "pytest", Tests: []string{"tests/test_x.py::test_a"}, Paths: []string{"tests/"}},
			want:  "pytest tests/ tests/test_x.py::test_a",
		},
		{
			name:  "pytest nodeids only",
			check: verify.Check{Runner: "pytest", Tests: []string{"tests/test_x.py::test_a", "tests/test_y.py::test_b"}},
			want:  "pytest tests/test_x.py::test_a tests/test_y.py::test_b",
		},
		{
			name:  "raw command is literal",
			check: verify.Check{Runner: "bats", Command: "bats ./.endless/tasks/E-1/cli.bats"},
			want:  "bats ./.endless/tasks/E-1/cli.bats",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.check.ResolvedCommand(); got != tc.want {
				t.Errorf("ResolvedCommand() = %q, want %q", got, tc.want)
			}
		})
	}
}

// The discovery/merge path validates the effective manifest; ResolvedCommand and
// ResolvedFormat for a full parsed manifest should match a hand-built check.
func TestCheck_ResolvedThroughParse(t *testing.T) {
	m, err := verify.ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	wantCmds := []string{
		"go test -run '^(TestFoo|TestBar)$' ./internal/verify/...",
		"pytest tests/test_x.py::test_a",
		"bats ./.endless/tasks/E-1234/cli.bats",
	}
	var gotCmds []string
	for _, c := range m.Checks {
		gotCmds = append(gotCmds, c.ResolvedCommand())
	}
	if !reflect.DeepEqual(gotCmds, wantCmds) {
		t.Errorf("resolved commands = %v, want %v", gotCmds, wantCmds)
	}
	wantFormats := []verify.Format{verify.FormatGotestJSON, verify.FormatPytestJSON, verify.FormatTAP}
	for i, c := range m.Checks {
		if c.ResolvedFormat() != wantFormats[i] {
			t.Errorf("Checks[%d].ResolvedFormat() = %q, want %q", i, c.ResolvedFormat(), wantFormats[i])
		}
	}
}
