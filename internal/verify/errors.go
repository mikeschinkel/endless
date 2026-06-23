package verify

import (
	"errors"
)

var (
	ErrInvalidManifest      = errors.New("invalid verify.toml manifest")
	ErrInvalidProjectConfig = errors.New("invalid project-level .endless/verify.toml")
	ErrReadingManifest      = errors.New("reading verify.toml manifest")
	ErrDecodingManifest     = errors.New("decoding verify.toml manifest")
	ErrUnknownManifestKeys  = errors.New("unknown keys in verify.toml manifest")
	ErrMissingField         = errors.New("missing required field")
	ErrUnknownSchema        = errors.New("unknown manifest schema version")
	ErrUnknownFormat        = errors.New("unknown result-stream format")
	ErrTaskIDMismatch       = errors.New("manifest task id does not match suite directory")
	ErrDiscoveringSuites    = errors.New("discovering verification suites")

	// Check-level validation.
	ErrNoChecks                  = errors.New("manifest declares no [[check]] entries")
	ErrCheckMissingRunner        = errors.New("check is missing required field: runner")
	ErrTestsRequireFirstClass    = errors.New("tests is only valid on a first-class runner")
	ErrPathsRequireFirstClass    = errors.New("paths is only valid on a first-class runner")
	ErrRawCheckNeedsCommand      = errors.New("non-first-class runner requires command")
	ErrFirstClassNeedsSelection  = errors.New("first-class runner requires tests, paths, or command")
	ErrFirstClassCommandConflict = errors.New("command and tests/paths are mutually exclusive on a first-class runner")
	ErrFormatMismatch            = errors.New("declared format does not match the runner's inferred format")

	// Result normalization (the CTRF-subset writer and native parsers).
	ErrNormalizingResults = errors.New("normalizing native result stream")
	ErrParsingGotestJSON  = errors.New("parsing go test -json stream")
	ErrParsingPytestJSON  = errors.New("parsing pytest JSON report")
	ErrParsingTAP         = errors.New("parsing TAP stream")
	ErrWritingReport      = errors.New("writing CTRF report")
)
