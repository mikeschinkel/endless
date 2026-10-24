package verify_test

import (
	"errors"
	"testing"

	"github.com/mikeschinkel/endless/internal/verify"
)

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

// The discovery/merge path validates the effective manifest; ResolvedFormat for
// a full parsed manifest should match a hand-built check.
func TestCheck_ResolvedThroughParse(t *testing.T) {
	m, err := verify.ParseManifest([]byte(validManifest))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	wantFormats := []verify.Format{verify.FormatGotestJSON, verify.FormatPytestJSON, verify.FormatTAP}
	for i, c := range m.Checks {
		if c.ResolvedFormat() != wantFormats[i] {
			t.Errorf("Checks[%d].ResolvedFormat() = %q, want %q", i, c.ResolvedFormat(), wantFormats[i])
		}
	}
}

// A check names whose environment it runs in (E-2278). The two keys parse, and
// an `as` that names nobody is refused rather than quietly read as a person.
func TestCheck_AsAndTmuxParse(t *testing.T) {
	const head = "schema = 1\ntask = \"E-1\"\n"
	m, err := verify.ParseManifest([]byte(head + `
[[check]]
runner = "bats"
command = "true"
as = "agent"
tmux = true

[[check]]
runner = "bats"
command = "true"
`))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if m.Checks[0].As != verify.AsAgent || !m.Checks[0].Tmux {
		t.Errorf("first check = %+v, want as=agent tmux=true", m.Checks[0])
	}
	if m.Checks[1].As != "" || m.Checks[1].Tmux {
		t.Errorf("second check = %+v, want the person default outside tmux", m.Checks[1])
	}

	_, err = verify.ParseManifest([]byte(head + "[[check]]\nrunner = \"bats\"\ncommand = \"true\"\nas = \"robot\"\n"))
	if !errors.Is(err, verify.ErrUnknownCheckAs) {
		t.Errorf("as = \"robot\": err = %v, want ErrUnknownCheckAs", err)
	}
}
