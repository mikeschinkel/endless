package settings

import (
	"github.com/mikeschinkel/go-doterr"
)

// OptIn is the runtime form of a three-way opt-in setting. Unset is the zero
// value: a key nobody wrote. Off is an explicit decline, which doctor reads as
// "do not suggest this again"; Unset is what doctor may suggest.
type OptIn uint8

const (
	Unset OptIn = iota
	On
	Off
)

// The three spellings an opt-in takes in a config file. A missing key is
// Unset too, so UnsetText is only ever written to record "no opinion" on
// purpose.
const (
	OnText    = "on"
	OffText   = "off"
	UnsetText = "unset"
)

// OptInTexts lists the accepted spellings in the order messages name them.
var OptInTexts = []string{OnText, OffText, UnsetText}

// ParseOptIn turns a config file's opt-in text into an OptIn. The empty
// string — a missing key — is Unset.
//
// The legacy JSON booleans arrive here as "true" and "false" (see
// config.OptInText) and are refused with ErrLegacyBool, which says what to
// write instead. Anything else is ErrUnknownOptIn. Callers add the file and the
// key; this function knows neither.
func ParseOptIn(s string) (o OptIn, err error) {
	switch s {
	case "", UnsetText:
		o = Unset
	case OnText:
		o = On
	case OffText:
		o = Off
	case "true", "false":
		err = doterr.NewErr(ErrSettings, ErrInvalidValue, ErrLegacyBool,
			"value", s, "write", legacyReplacement(s))
	default:
		err = doterr.NewErr(ErrSettings, ErrInvalidValue, ErrUnknownOptIn,
			"value", s)
	}
	return o, err
}

// legacyReplacement is the opt-in text a legacy boolean spelling becomes:
// "true" is "on", anything else "off". Used by the error above and by the
// one-time migration of existing config files.
func legacyReplacement(s string) string {
	if s == "true" {
		return OnText
	}
	return OffText
}

// String returns the config-file spelling.
func (o OptIn) String() string {
	switch o {
	case On:
		return OnText
	case Off:
		return OffText
	}
	return UnsetText
}

// Or returns o, or def when o is Unset.
func (o OptIn) Or(def OptIn) OptIn {
	if o == Unset {
		return def
	}
	return o
}

// IsOn reports whether o is explicitly On. An Unset value is not on; resolve
// it against the setting's registered default first (see Resolve).
func (o OptIn) IsOn() bool {
	return o == On
}
