package settings

import (
	"errors"
)

var (
	// Layer.
	ErrSettings = errors.New("settings")

	// Categories.
	ErrInvalidSetting = errors.New("invalid setting registration")
	ErrInvalidValue   = errors.New("invalid setting value")

	// Conditions.
	ErrNotInitialized   = errors.New("the settings registry has not been initialized")
	ErrEmptyID          = errors.New("setting ID is empty")
	ErrMalformedID      = errors.New("setting ID is not a dotted lower_snake_case JSON path")
	ErrDuplicateID      = errors.New("setting ID is registered more than once")
	ErrEmptyFeature     = errors.New("setting has no feature")
	ErrUnknownFiles     = errors.New("setting names no known config file")
	ErrUnknownKind      = errors.New("setting has an unknown kind")
	ErrMissingRationale = errors.New("suggestable setting has no rationale")
	ErrSuggestNonOptIn  = errors.New("only an opt-in setting can be suggestable")
	ErrInvalidDefault   = errors.New("setting default is not a valid value for its kind")
	ErrNoChoices        = errors.New("choice setting lists no choices")
	ErrUnknownOptIn     = errors.New(`opt-in value must be "on", "off" or "unset"`)
	ErrLegacyBool       = errors.New(`opt-in value is a legacy boolean; write "on" for true, "off" for false`)
)
