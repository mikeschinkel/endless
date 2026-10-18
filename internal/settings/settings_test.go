package settings

import (
	"errors"
	"strings"
	"testing"
)

func optIn(id ID) Setting {
	return Setting{ID: id, Feature: "f", Files: ProjectFile, Kind: OptInKind,
		Suggestable: true, Rationale: "because", Default: OffText}
}

func TestAccessorsRefuseBeforeInitialize(t *testing.T) {
	t.Cleanup(resetForTest())
	Register(optIn("f.enabled"))

	if _, err := All(); !errors.Is(err, ErrNotInitialized) {
		t.Errorf("All() before Initialize = %v, want ErrNotInitialized", err)
	}
	if _, _, err := Lookup("f.enabled"); !errors.Is(err, ErrNotInitialized) {
		t.Errorf("Lookup() before Initialize = %v, want ErrNotInitialized", err)
	}
	if _, err := Resolve("f.enabled", Unset); !errors.Is(err, ErrNotInitialized) {
		t.Errorf("Resolve() before Initialize = %v, want ErrNotInitialized", err)
	}
}

func TestInitialize_ValidRegistryIsOrderedAndFrozen(t *testing.T) {
	t.Cleanup(resetForTest())
	Register(optIn("z.enabled"), optIn("a.enabled"),
		Setting{ID: "a.interval", Feature: "a", Files: MachineFile, Kind: DurationKind, Default: "5m"},
		Setting{ID: "a.cap", Feature: "a", Files: ProjectFile, Kind: IntKind, Default: "3"},
		Setting{ID: "a.target", Feature: "a", Files: MachineFile, Kind: ChoiceKind,
			Choices: []string{"active", "monitor"}, Default: "active"},
		Setting{ID: "a.dir", Feature: "a", Files: MachineFile, Kind: PathKind, Default: "/tmp/a"},
		Setting{ID: "a.name", Feature: "a", Files: MachineFile, Kind: NameKind, Default: "a-b_c"},
		Setting{ID: "a.term", Feature: "a", Files: MachineFile, Kind: CommandKind, Default: ""},
	)
	if err := Initialize(); err != nil {
		t.Fatalf("Initialize() = %v", err)
	}
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	var got []ID
	for _, s := range all {
		got = append(got, s.ID)
	}
	want := []ID{"a.cap", "a.dir", "a.enabled", "a.interval", "a.name", "a.target", "a.term", "z.enabled"}
	if len(got) != len(want) {
		t.Fatalf("All() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("All() = %v, want %v", got, want)
		}
	}
	if _, ok, _ := Lookup("a.interval"); !ok {
		t.Error("Lookup(a.interval) not found")
	}
	if _, ok, _ := Lookup("nope"); ok {
		t.Error("Lookup(nope) found something")
	}
}

// TestInitialize_ReportsEveryFailure: one bad registry run names every
// problem, not just the first, and leaves the registry refusing.
func TestInitialize_ReportsEveryFailure(t *testing.T) {
	t.Cleanup(resetForTest())
	noRationale := optIn("b.enabled")
	noRationale.Rationale = ""
	Register(
		Setting{ID: "", Feature: "f", Files: ProjectFile, Kind: OptInKind, Default: OffText},
		Setting{ID: "Bad-ID", Feature: "f", Files: ProjectFile, Kind: OptInKind, Default: OffText},
		Setting{ID: "c.enabled", Feature: "f", Files: 0, Kind: OptInKind, Default: OffText},
		noRationale,
		optIn("d.enabled"), optIn("d.enabled"),
		Setting{ID: "e.x", Files: ProjectFile, Kind: OptInKind, Default: OffText},
		Setting{ID: "g.x", Feature: "g", Files: ProjectFile, Kind: 99, Default: "?"},
		Setting{ID: "h.enabled", Feature: "h", Files: ProjectFile, Kind: OptInKind, Default: UnsetText},
		Setting{ID: "i.interval", Feature: "i", Files: MachineFile, Kind: DurationKind, Default: "soon"},
		Setting{ID: "j.cap", Feature: "j", Files: MachineFile, Kind: IntKind, Default: "three"},
		Setting{ID: "k.target", Feature: "k", Files: MachineFile, Kind: ChoiceKind},
		Setting{ID: "l.target", Feature: "l", Files: MachineFile, Kind: ChoiceKind,
			Choices: []string{"a"}, Default: "b"},
		Setting{ID: "m.cap", Feature: "m", Files: MachineFile, Kind: IntKind, Default: "1",
			Suggestable: true, Rationale: "r"},
	)
	err := Initialize()
	for _, want := range []error{
		ErrEmptyID, ErrMalformedID, ErrUnknownFiles, ErrMissingRationale,
		ErrDuplicateID, ErrEmptyFeature, ErrUnknownKind, ErrInvalidDefault,
		ErrNoChoices, ErrSuggestNonOptIn,
	} {
		if !errors.Is(err, want) {
			t.Errorf("Initialize() error does not include %v", want)
		}
	}
	if _, err := All(); !errors.Is(err, ErrNotInitialized) {
		t.Errorf("All() after a failed Initialize = %v, want ErrNotInitialized", err)
	}
}

// TestInitialize_RefusesAMalformedShapedDefault: a path, name or command
// default that fails its kind's shape check fails Initialize, naming the key.
func TestInitialize_RefusesAMalformedShapedDefault(t *testing.T) {
	for _, s := range []Setting{
		{ID: "p.dir", Feature: "p", Files: MachineFile, Kind: PathKind, Default: "relative/dir"},
		{ID: "p.dir", Feature: "p", Files: MachineFile, Kind: PathKind, Default: ""},
		{ID: "n.name", Feature: "n", Files: MachineFile, Kind: NameKind, Default: "a.b"},
		{ID: "n.name", Feature: "n", Files: MachineFile, Kind: NameKind, Default: ""},
		{ID: "c.term", Feature: "c", Files: MachineFile, Kind: CommandKind, Default: "ghostty -e"},
	} {
		t.Run(s.Kind.String()+"="+s.Default, func(t *testing.T) {
			t.Cleanup(resetForTest())
			Register(s)
			err := Initialize()
			if !errors.Is(err, ErrInvalidDefault) {
				t.Fatalf("Initialize() = %v, want ErrInvalidDefault", err)
			}
			if !strings.Contains(err.Error(), string(s.ID)) {
				t.Errorf("Initialize() = %v, does not name %s", err, s.ID)
			}
		})
	}
}

func TestShapeChecks(t *testing.T) {
	for _, c := range []struct {
		check func(string) error
		in    string
		want  error // nil: accepted
	}{
		{CheckPath, "/tmp/endless-501", nil},
		{CheckPath, "~", nil},
		{CheckPath, "~/sock", nil},
		{CheckPath, "sock", ErrNotAbsolutePath},
		{CheckPath, "./sock", ErrNotAbsolutePath},
		{CheckPath, "~other/sock", ErrNotAbsolutePath},
		{CheckPath, "", ErrNotAbsolutePath},
		{CheckName, "endless-tasks", nil},
		{CheckName, "work_2", nil},
		{CheckName, "", ErrMalformedName},
		{CheckName, "a/b", ErrMalformedName},
		{CheckName, "a b", ErrMalformedName},
		{CheckName, "a\tb", ErrMalformedName},
		{CheckName, "a:b", ErrMalformedName},
		{CheckName, "a.b", ErrMalformedName},
		{CheckCommand, "", nil},
		{CheckCommand, "ghostty -e {cmd}", nil},
		{CheckCommand, "ghostty -e", ErrNoCommandPlaceholder},
		{CheckCommand, "{CMD}", ErrNoCommandPlaceholder},
	} {
		err := c.check(c.in)
		switch {
		case c.want == nil && err != nil:
			t.Errorf("check(%q) = %v, want accepted", c.in, err)
		case c.want != nil && !errors.Is(err, c.want):
			t.Errorf("check(%q) = %v, want %v", c.in, err, c.want)
		case c.want != nil && !errors.Is(err, ErrInvalidValue):
			t.Errorf("check(%q) = %v, not an ErrInvalidValue", c.in, err)
		}
	}
}

func TestKindExportNames(t *testing.T) {
	for k, want := range map[Kind]string{PathKind: "path", NameKind: "name", CommandKind: "command"} {
		if got := k.String(); got != want {
			t.Errorf("%d.String() = %q, want %q", k, got, want)
		}
	}
}

func TestResolve(t *testing.T) {
	t.Cleanup(resetForTest())
	on := optIn("on.enabled")
	on.Default = OnText
	Register(optIn("off.enabled"), on)
	if err := Initialize(); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		id   ID
		v    OptIn
		want OptIn
	}{
		{"off.enabled", Unset, Off},
		{"off.enabled", On, On},
		{"on.enabled", Unset, On},
		{"on.enabled", Off, Off},
		{"unknown.key", Unset, On},
		{"unknown.key", Off, Off},
	}
	for _, c := range cases {
		got, err := Resolve(c.id, c.v)
		if err != nil || got != c.want {
			t.Errorf("Resolve(%s, %s) = %s, %v; want %s", c.id, c.v, got, err, c.want)
		}
	}
}

func TestParseOptIn(t *testing.T) {
	for _, c := range []struct {
		in   string
		want OptIn
	}{
		{"", Unset}, {"unset", Unset}, {"on", On}, {"off", Off},
	} {
		got, err := ParseOptIn(c.in)
		if err != nil || got != c.want {
			t.Errorf("ParseOptIn(%q) = %s, %v; want %s", c.in, got, err, c.want)
		}
	}
	for _, c := range []struct {
		in   string
		want error
	}{
		{"true", ErrLegacyBool}, {"false", ErrLegacyBool},
		{"yes", ErrUnknownOptIn}, {"ON", ErrUnknownOptIn}, {"1", ErrUnknownOptIn},
	} {
		if _, err := ParseOptIn(c.in); !errors.Is(err, c.want) {
			t.Errorf("ParseOptIn(%q) = %v, want %v", c.in, err, c.want)
		}
	}
}

func TestLegacyReplacement(t *testing.T) {
	if got := legacyReplacement("true"); got != OnText {
		t.Errorf("legacyReplacement(true) = %q", got)
	}
	if got := legacyReplacement("false"); got != OffText {
		t.Errorf("legacyReplacement(false) = %q", got)
	}
}
