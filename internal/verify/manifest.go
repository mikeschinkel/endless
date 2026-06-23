// Package verify defines the per-task verification-suite manifest (verify.toml)
// and the discovery convention Endless uses to find suites. It implements
// section A of the E-1596 epic's interface contract: the manifest is a POINTER
// to native test runners, never a re-description of the tests.
//
// A verification is a list of [[check]] entries (see Check): one ticket
// composes multiple runner invocations into one proof. A first-class runner
// (gotest, pytest) uses a structured selection Endless translates to the native
// filter and a format Endless infers; any other runner uses a literal command
// plus a declared format. A verification suite lives in the product-controlled
// directory .endless/tasks/<id>/, beside its runner files and an optional
// fixtures/.
//
// This package owns the schema, discovery, and the first-class translation /
// bare-clone command emission only. Running a suite (creating the isolated temp
// environment, invoking the checks, normalizing the native result streams)
// belongs to the consumers of these manifests.
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

// Manifest is a parsed verify.toml. It holds the list of checks a bare clone
// runs (each contributing to the proof), plus optional execution hints (setup,
// teardown, tiers, seed fixtures, isolation needs). Every check's resolved
// command must exit 0 on all-pass and non-zero on any failure with no Endless
// present (see RenderRunScript for the bare-clone emission).
//
// A Manifest may be a per-task file (under .endless/tasks/<id>/) or the
// effective manifest produced by merging a project-level ProjectConfig beneath a
// per-task file (see Merge). The three precondition kinds are distinct: Needs
// provisions the substrate, Setup prepares the project (build/install/migrate/
// codegen), and Seed loads state. The runner executes them in the order
// provision -> setup -> seed -> checks -> teardown.
//
// TOML ordering note: because the [[check]] array is an array-of-tables, the
// top-level scalar/array fields below must appear BEFORE the first [[check]]
// block in a verify.toml file, or TOML binds them to that check table instead of
// the document root (and the unknown-key check then rejects them).
type Manifest struct {
	Schema   int      `toml:"schema"`
	Task     string   `toml:"task"`
	Checks   []Check  `toml:"check"`
	Setup    []string `toml:"setup"`
	Teardown []string `toml:"teardown"`
	Tiers    []string `toml:"tiers"`
	Seed     []string `toml:"seed"`
	Needs    []string `toml:"needs"`
}

// ParseManifest decodes a complete verify.toml document, rejects unknown keys,
// and validates the result. It returns a fully validated Manifest or an error
// that wraps ErrInvalidManifest. Use this for a standalone, self-sufficient
// manifest (the bare-clone case); the layered discovery path decodes per-task
// files leniently and validates the merged effective manifest instead.
func ParseManifest(data []byte) (m *Manifest, err error) {
	m, err = decodeManifest(data)
	if err != nil {
		goto end
	}

	err = m.Validate()
	if err != nil {
		goto end
	}

end:
	return m, err
}

// decodeManifest decodes a verify.toml document into a Manifest and rejects
// unknown keys, but does NOT enforce required fields. The layered discovery path
// decodes per-task files this way so a field a task omits (e.g. format) can be
// supplied by the project-level config before the effective manifest is
// validated. Errors wrap ErrInvalidManifest.
func decodeManifest(data []byte) (m *Manifest, err error) {
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

end:
	return m, err
}

// Validate checks the required top-level fields, that schema holds a known
// value, and that the manifest declares at least one check, then validates each
// check by its form (see validateCheck). Optional fields (setup, teardown,
// tiers, seed, needs) are not constrained here. Errors wrap ErrInvalidManifest.
func (m *Manifest) Validate() (err error) {
	switch {
	case m.Schema == 0:
		err = doterr.NewErr(ErrInvalidManifest, ErrMissingField, "field", "schema")
		goto end
	case m.Schema != SchemaVersion:
		err = doterr.NewErr(ErrInvalidManifest, ErrUnknownSchema,
			"schema", m.Schema, "supported", SchemaVersion)
		goto end
	case m.Task == "":
		err = doterr.NewErr(ErrInvalidManifest, ErrMissingField, "field", "task")
		goto end
	case len(m.Checks) == 0:
		err = doterr.NewErr(ErrInvalidManifest, ErrNoChecks)
		goto end
	}

	for i, c := range m.Checks {
		err = validateCheck(c, i)
		if err != nil {
			goto end
		}
	}

end:
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
