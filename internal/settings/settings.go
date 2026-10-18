// Package settings is the registry of Endless's config keys (E-2284).
//
// Each key is declared once, as a Setting, by the package that owns the
// feature it configures. The declaration carries what `endless doctor`
// (E-2285/E-2287) needs to suggest it — whether it may be suggested, why
// someone would turn it on, which file it lives in — so a setting cannot exist
// without its rationale.
//
// # Collect, then validate (E-2283)
//
// Register is called from init() and only appends. It returns nothing and
// checks nothing, so an init() can never fail and no validation can come to
// depend on the order init()s run in. Initialize, called once from endless-go's
// startup, validates every entry, reports every failure together, builds the
// lookup index and freezes it. Before Initialize, All and Lookup return
// ErrNotInitialized.
//
// No mutex: init() runs on one goroutine, and after Initialize the index is
// only read.
package settings

import (
	"errors"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/mikeschinkel/go-doterr"
)

// ID is a setting's JSON path in the config file, e.g. "main_sync.enabled".
type ID string

// Files says which config file(s) a setting may live in.
type Files uint8

const (
	// MachineFile is ~/.config/endless/config.json: one value for every
	// project on the machine.
	MachineFile Files = 1 << iota

	// ProjectFile is <project>/.endless/config.json, committed with the
	// project.
	ProjectFile

	// BothFiles may live in either; the project file wins. A decline is
	// recorded in the project file by default (E-2285 lets the user choose).
	BothFiles = MachineFile | ProjectFile
)

// String names the file(s) as the JSON export spells them.
func (f Files) String() string {
	switch f {
	case MachineFile:
		return "machine"
	case ProjectFile:
		return "project"
	case BothFiles:
		return "both"
	}
	return "unknown"
}

// Has reports whether f includes file.
func (f Files) Has(file Files) bool {
	return f&file != 0
}

// Kind is the shape of a setting's value, which says what values are valid.
type Kind uint8

const (
	// OptInKind is a three-way "on" / "off" / "unset" switch.
	OptInKind Kind = iota + 1

	// DurationKind is a Go duration string ("5m").
	DurationKind

	// IntKind is a JSON integer.
	IntKind

	// ChoiceKind is one of the strings in Setting.Choices.
	ChoiceKind

	// PathKind is an absolute path, or one under ~ (see CheckPath).
	PathKind

	// NameKind is a plain file or tmux session name (see CheckName).
	NameKind

	// CommandKind is a command template containing {cmd}, or empty (see
	// CheckCommand).
	CommandKind
)

// String names the kind as the JSON export spells it.
func (k Kind) String() string {
	switch k {
	case OptInKind:
		return "optin"
	case DurationKind:
		return "duration"
	case IntKind:
		return "int"
	case ChoiceKind:
		return "choice"
	case PathKind:
		return "path"
	case NameKind:
		return "name"
	case CommandKind:
		return "command"
	}
	return "unknown"
}

// Setting declares one config key. One entry per leaf key; Feature groups a
// feature's keys.
type Setting struct {
	// ID is the key's JSON path.
	ID ID

	// Feature groups the keys of one feature, e.g. "main_sync".
	Feature string

	// Files is where the key may be written.
	Files Files

	// Kind is the value's shape.
	Kind Kind

	// Choices are the accepted values of a ChoiceKind setting.
	Choices []string

	// Suggestable marks an opt-in that doctor may suggest while it is unset.
	// Tuning keys (an interval, a cap) are registered but never suggested.
	Suggestable bool

	// Rationale is why someone would turn the setting on, in one or two
	// sentences a user reads in doctor's suggestion. Required when Suggestable.
	Rationale string

	// Default is the effective value when no file sets the key, in the key's
	// own spelling: "off" for an opt-in, "5m" for a duration.
	Default string

	// PreconditionCheck optionally names the doctor check that must pass
	// before this setting is worth suggesting. Resolved by the doctor's own
	// Initialize (E-2285), which runs after this package's.
	PreconditionCheck string
}

// Provider is implemented by each feature's runtime config type, so the
// feature's settings are declared beside the type that reads them.
type Provider interface {
	Settings() []Setting
}

var (
	// pending is appended to by Register and read only by Initialize.
	pending []Setting

	// index is built by Initialize; nil means not initialized.
	index map[ID]Setting

	// ordered is index's values sorted by ID.
	ordered []Setting
)

// Register queues settings for Initialize. Call it from init(). It validates
// nothing and returns nothing by design: see the package doc.
func Register(ss ...Setting) {
	pending = append(pending, ss...)
}

// idPattern is a dotted lower_snake_case JSON path.
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

// Initialize validates every registered setting, joining every failure rather
// than stopping at the first, and on success builds and freezes the index. On
// failure the index is left nil, so every accessor keeps refusing.
//
// Calling it again re-validates everything registered so far, which is what a
// test that registers a fixture wants.
func Initialize() (err error) {
	var errs []error
	var seen = make(map[ID]int, len(pending))
	var built map[ID]Setting
	var list []Setting

	for i, s := range pending {
		errs = append(errs, validate(i, s)...)
		if s.ID == "" {
			continue
		}
		if first, dup := seen[s.ID]; dup {
			errs = append(errs, doterr.NewErr(ErrSettings, ErrInvalidSetting, ErrDuplicateID,
				"id", string(s.ID), "first", first, "again", i))
			continue
		}
		seen[s.ID] = i
	}
	err = errors.Join(errs...)
	if err != nil {
		index, ordered = nil, nil
		goto end
	}

	built = make(map[ID]Setting, len(pending))
	list = make([]Setting, 0, len(pending))
	for _, s := range pending {
		built[s.ID] = s
		list = append(list, s)
	}
	slices.SortFunc(list, func(a, b Setting) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})
	index, ordered = built, list

end:
	return err
}

// validate returns every problem with one entry. i is its registration order,
// which names an entry whose ID is the thing that is wrong.
func validate(i int, s Setting) (errs []error) {
	add := func(cond error, kv ...any) {
		parts := append([]any{ErrSettings, ErrInvalidSetting, cond,
			"id", string(s.ID), "index", i}, kv...)
		errs = append(errs, doterr.NewErr(parts...))
	}

	switch {
	case s.ID == "":
		add(ErrEmptyID)
	case !idPattern.MatchString(string(s.ID)):
		add(ErrMalformedID)
	}
	if s.Feature == "" {
		add(ErrEmptyFeature)
	}
	switch s.Files {
	case MachineFile, ProjectFile, BothFiles:
	default:
		add(ErrUnknownFiles, "files", int(s.Files))
	}
	if s.Suggestable && s.Rationale == "" {
		add(ErrMissingRationale)
	}
	if s.Suggestable && s.Kind != OptInKind {
		add(ErrSuggestNonOptIn, "kind", s.Kind.String())
	}

	switch s.Kind {
	case OptInKind:
		if s.Default != OnText && s.Default != OffText {
			add(ErrInvalidDefault, "default", s.Default, "want", `"on" or "off"`)
		}
	case DurationKind:
		d, err := time.ParseDuration(s.Default)
		if err != nil || d <= 0 {
			add(ErrInvalidDefault, "default", s.Default, "want", "a positive Go duration")
		}
	case IntKind:
		if _, err := strconv.Atoi(s.Default); err != nil {
			add(ErrInvalidDefault, "default", s.Default, "want", "an integer")
		}
	case ChoiceKind:
		if len(s.Choices) == 0 {
			add(ErrNoChoices)
		} else if !slices.Contains(s.Choices, s.Default) {
			add(ErrInvalidDefault, "default", s.Default, "want", "one of its choices")
		}
	case PathKind:
		if CheckPath(s.Default) != nil {
			add(ErrInvalidDefault, "default", s.Default, "want", PathAccepts)
		}
	case NameKind:
		if CheckName(s.Default) != nil {
			add(ErrInvalidDefault, "default", s.Default, "want", NameAccepts)
		}
	case CommandKind:
		if CheckCommand(s.Default) != nil {
			add(ErrInvalidDefault, "default", s.Default, "want", CommandAccepts)
		}
	default:
		add(ErrUnknownKind, "kind", int(s.Kind))
	}
	return errs
}

// All returns every setting, ordered by ID.
func All() (ss []Setting, err error) {
	if index == nil {
		err = doterr.NewErr(ErrSettings, ErrNotInitialized)
		goto end
	}
	ss = slices.Clone(ordered)
end:
	return ss, err
}

// Lookup returns the setting registered under id. ok is false when no setting
// has that ID.
func Lookup(id ID) (s Setting, ok bool, err error) {
	if index == nil {
		err = doterr.NewErr(ErrSettings, ErrNotInitialized, "id", string(id))
		goto end
	}
	s, ok = index[id]
end:
	return s, ok, err
}

// Resolve returns v, or the registered default of the opt-in id when v is
// Unset. It is how a reader turns a file's tri-state into on or off without
// keeping its own copy of the default.
//
// An id nobody registered resolves Unset to On: an unknown key fails open, the
// rule Endless has always applied to `checks` keys.
func Resolve(id ID, v OptIn) (o OptIn, err error) {
	var s Setting
	var ok bool

	o = v
	if v != Unset {
		goto end
	}
	s, ok, err = Lookup(id)
	if err != nil {
		goto end
	}
	if !ok {
		o = On
		goto end
	}
	o, err = ParseOptIn(s.Default)

end:
	return o, err
}

// resetForTest empties the registry and returns a func that restores it.
func resetForTest() (restore func()) {
	prevPending, prevIndex, prevOrdered := pending, index, ordered
	pending, index, ordered = nil, nil, nil
	return func() {
		pending, index, ordered = prevPending, prevIndex, prevOrdered
	}
}
