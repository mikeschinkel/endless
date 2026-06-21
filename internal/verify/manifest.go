// Package verify defines the per-task verification-suite manifest (verify.toml)
// and the discovery convention Endless uses to find suites. It implements
// section A of the E-1596 epic's interface contract: the manifest is a POINTER
// to a native test runner, never a re-description of the tests.
//
// A verification suite lives in the product-controlled directory
// .endless/tasks/<id>/, beside its runner files and an optional fixtures/. The
// manifest is intentionally flat (no nesting); if a future need pushes toward
// nesting, re-evaluate the format rather than nest.
//
// This package owns the schema and discovery only. Running a suite (creating
// the isolated temp environment, invoking the runner, normalizing the native
// result stream) belongs to the consumers of these manifests.
package verify

import (
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/mikeschinkel/go-doterr"
)

// SchemaVersion is the only verify.toml schema version this build understands.
// A manifest declaring any other version fails validation loudly so a newer
// suite is never silently misread by an older Endless.
const SchemaVersion = 1

// Format names the native result stream a suite's runner produces. The
// normalizer reads the stream named here to build the uniform result envelope.
// It selects a parser; it does not describe the tests.
type Format string

const (
	FormatGotestJSON Format = "gotest-json"
	FormatPytestJSON Format = "pytest-json"
	FormatTAP        Format = "tap"
)

// knownFormats is the canonical set of result-stream formats, used both by
// Format.Valid and by the error metadata that lists the accepted values.
var knownFormats = []Format{
	FormatGotestJSON,
	FormatPytestJSON,
	FormatTAP,
}

// Valid reports whether f is one of the known result-stream formats.
func (f Format) Valid() (valid bool) {
	switch f {
	case FormatGotestJSON, FormatPytestJSON, FormatTAP:
		valid = true
	}
	return valid
}

// Manifest is a parsed verify.toml. It names the runner a bare clone executes
// directly, the native result format that runner emits, and optional execution
// hints (tiers, seed fixtures, isolation needs). The runner string alone must
// exit 0 on all-pass and non-zero on any failure with no Endless present.
type Manifest struct {
	Schema int      `toml:"schema"`
	Task   string   `toml:"task"`
	Runner string   `toml:"runner"`
	Format Format   `toml:"format"`
	Tiers  []string `toml:"tiers"`
	Seed   []string `toml:"seed"`
	Needs  []string `toml:"needs"`
}

// ParseManifest decodes a verify.toml document, rejects unknown keys, and
// validates the result. It returns a fully validated Manifest or an error that
// wraps ErrInvalidManifest.
func ParseManifest(data []byte) (m *Manifest, err error) {
	var md toml.MetaData
	var undecoded []toml.Key

	m = &Manifest{}
	md, err = toml.Decode(string(data), m)
	if err != nil {
		err = doterr.NewErr(ErrInvalidManifest, ErrDecodingManifest, err)
		goto end
	}

	undecoded = md.Undecoded()
	if len(undecoded) > 0 {
		err = doterr.NewErr(ErrInvalidManifest, ErrUnknownManifestKeys,
			"keys", joinKeys(undecoded))
		goto end
	}

	err = m.Validate()
	if err != nil {
		goto end
	}

end:
	return m, err
}

// Validate checks that all required fields are present and that schema and
// format hold known values. Optional fields (tiers, seed, needs) are not
// constrained here. Errors wrap ErrInvalidManifest.
func (m *Manifest) Validate() (err error) {
	switch {
	case m.Schema == 0:
		err = doterr.NewErr(ErrInvalidManifest, ErrMissingField, "field", "schema")
	case m.Schema != SchemaVersion:
		err = doterr.NewErr(ErrInvalidManifest, ErrUnknownSchema,
			"schema", m.Schema, "supported", SchemaVersion)
	case m.Task == "":
		err = doterr.NewErr(ErrInvalidManifest, ErrMissingField, "field", "task")
	case m.Runner == "":
		err = doterr.NewErr(ErrInvalidManifest, ErrMissingField, "field", "runner")
	case m.Format == "":
		err = doterr.NewErr(ErrInvalidManifest, ErrMissingField, "field", "format")
	case !m.Format.Valid():
		err = doterr.NewErr(ErrInvalidManifest, ErrUnknownFormat,
			"format", string(m.Format), "supported", formatList())
	}
	return err
}

// joinKeys renders decoded TOML keys as a comma-separated string for error
// metadata.
func joinKeys(keys []toml.Key) (joined string) {
	var parts []string

	parts = make([]string, len(keys))
	for i, key := range keys {
		parts[i] = key.String()
	}
	joined = strings.Join(parts, ", ")
	return joined
}

// formatList renders the known formats as a comma-separated string for error
// metadata.
func formatList() (list string) {
	var parts []string

	parts = make([]string, len(knownFormats))
	for i, f := range knownFormats {
		parts[i] = string(f)
	}
	list = strings.Join(parts, ", ")
	return list
}
