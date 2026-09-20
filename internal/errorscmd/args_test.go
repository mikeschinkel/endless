package errorscmd

import (
	"reflect"
	"testing"
)

// `endless errors show <id> --detail` — the spelling the listing's footer tells
// a user to type, and the one the Python CLI emits — failed every time it was
// run (E-2148). Go's flag package stops parsing at the first non-flag argument,
// so an id in front left --detail unparsed and sitting in Args() as a second
// positional, which runShow rejected as "one id at a time".
//
// jobs_cmd.errors_clear has carried a comment about this hazard since E-1960
// and orders its own argv around it. The lesson did not travel to errors_show,
// so the split is enforced here rather than left as a rule each caller has to
// remember — and these are what keep it enforced.
func TestSplitArgs(t *testing.T) {
	cases := map[string]struct {
		in          []string
		flags       []string
		positionals []string
	}{
		"the spelling the footer teaches": {
			in:          []string{"1534", "--detail"},
			flags:       []string{"--detail"},
			positionals: []string{"1534"},
		},
		"flags first still works": {
			in:          []string{"--detail", "1534"},
			flags:       []string{"--detail"},
			positionals: []string{"1534"},
		},
		"a flag on each side of the id": {
			in:          []string{"--detail", "1534", "--all"},
			flags:       []string{"--detail", "--all"},
			positionals: []string{"1534"},
		},
		"--id takes its value with it": {
			in:          []string{"--id", "1534", "--detail"},
			flags:       []string{"--id", "1534", "--detail"},
			positionals: nil,
		},
		"--id=N carries its own value": {
			in:          []string{"--id=1534", "--detail"},
			flags:       []string{"--id=1534", "--detail"},
			positionals: nil,
		},
		"single-dash spelling too": {
			in:          []string{"-id", "1534"},
			flags:       []string{"-id", "1534"},
			positionals: nil,
		},
		"-- ends flag parsing": {
			in:          []string{"--", "-7"},
			flags:       nil,
			positionals: []string{"-7"},
		},
		"two ids are still two positionals": {
			in:          []string{"1534", "1535", "--detail"},
			flags:       []string{"--detail"},
			positionals: []string{"1534", "1535"},
		},
		"nothing at all": {
			in:          nil,
			flags:       nil,
			positionals: nil,
		},
	}

	for name, c := range cases {
		flags, positionals := splitArgs(c.in)
		if !reflect.DeepEqual(flags, c.flags) {
			t.Errorf("%s: flags = %#v, want %#v", name, flags, c.flags)
		}
		if !reflect.DeepEqual(positionals, c.positionals) {
			t.Errorf("%s: positionals = %#v, want %#v", name, positionals, c.positionals)
		}
	}
}
