package settings

import (
	"errors"
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
	want := []ID{"a.cap", "a.enabled", "a.interval", "a.target", "z.enabled"}
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
