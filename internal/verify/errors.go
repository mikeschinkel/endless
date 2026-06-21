package verify

import (
	"errors"
)

var (
	ErrInvalidManifest     = errors.New("invalid verify.toml manifest")
	ErrReadingManifest     = errors.New("reading verify.toml manifest")
	ErrDecodingManifest    = errors.New("decoding verify.toml manifest")
	ErrUnknownManifestKeys = errors.New("unknown keys in verify.toml manifest")
	ErrMissingField        = errors.New("missing required field")
	ErrUnknownSchema       = errors.New("unknown manifest schema version")
	ErrUnknownFormat       = errors.New("unknown result-stream format")
	ErrTaskIDMismatch      = errors.New("manifest task id does not match suite directory")
	ErrDiscoveringSuites   = errors.New("discovering verification suites")
)
