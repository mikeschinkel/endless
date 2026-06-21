package verify

import (
	"github.com/BurntSushi/toml"

	"github.com/mikeschinkel/go-doterr"
	"github.com/mikeschinkel/go-dt"
)

// ProjectConfigFile is the project-level verify config filename. It lives at
// <root>/.endless/verify.toml — one directory above the per-task suites under
// .endless/tasks/<id>/ — and is merged beneath every per-task manifest at
// discovery time (see Merge).
const ProjectConfigFile = "verify.toml"

// ProjectDir is the project-controlled directory, relative to a project root,
// that holds the project-level verify config. It is the same .endless directory
// that contains the per-task SuitesDir.
const ProjectDir = ".endless"

// ProjectConfig is the project-level .endless/verify.toml: shared verification
// defaults layered beneath every per-task manifest. It carries no task or runner
// (those are inherently per-task) and no tiers; supplying one is rejected as an
// unknown key. It holds shared Setup steps and shared Seed fixtures (both run
// before the per-task steps) plus default Format and Needs that a task inherits
// when it omits them.
//
// The project config is optional: a project with no .endless/verify.toml simply
// contributes no shared layer, and each per-task manifest stands on its own.
type ProjectConfig struct {
	Schema int      `toml:"schema"`
	Setup  []string `toml:"setup"`
	Format Format   `toml:"format"`
	Seed   []string `toml:"seed"`
	Needs  []string `toml:"needs"`
}

// ParseProjectConfig decodes a project-level verify.toml, rejects unknown keys
// (including the per-task-only runner/task/tiers), and validates the result. It
// returns a validated ProjectConfig or an error that wraps
// ErrInvalidProjectConfig.
func ParseProjectConfig(data []byte) (pc *ProjectConfig, err error) {
	var md toml.MetaData
	var undecoded []toml.Key

	pc = &ProjectConfig{}
	md, err = toml.Decode(string(data), pc)
	if err != nil {
		err = doterr.NewErr(ErrInvalidProjectConfig, ErrDecodingManifest, err)
		goto end
	}

	undecoded = md.Undecoded()
	if len(undecoded) > 0 {
		err = doterr.NewErr(ErrInvalidProjectConfig, ErrUnknownManifestKeys,
			"keys", joinKeys(undecoded))
		goto end
	}

	err = pc.Validate()
	if err != nil {
		goto end
	}

end:
	return pc, err
}

// Validate checks the project config's schema and, when present, its default
// format. Unlike a per-task manifest the project config has no required content
// fields (task/runner are per-task); it need only declare a schema this build
// understands. Errors wrap ErrInvalidProjectConfig.
func (pc *ProjectConfig) Validate() (err error) {
	switch {
	case pc.Schema == 0:
		err = doterr.NewErr(ErrInvalidProjectConfig, ErrMissingField, "field", "schema")
	case pc.Schema != SchemaVersion:
		err = doterr.NewErr(ErrInvalidProjectConfig, ErrUnknownSchema,
			"schema", pc.Schema, "supported", SchemaVersion)
	case pc.Format != "" && !pc.Format.Valid():
		err = doterr.NewErr(ErrInvalidProjectConfig, ErrUnknownFormat,
			"format", string(pc.Format), "supported", formatList())
	}
	return err
}

// LoadProjectConfig reads, decodes, and validates the project-level verify.toml
// at <root>/.endless/verify.toml. A project without that file is not an error:
// LoadProjectConfig returns a nil ProjectConfig and nil error, and Merge treats
// a nil config as the identity layer. Any malformed or invalid project config
// fails loudly.
func LoadProjectConfig(root dt.DirPath) (pc *ProjectConfig, err error) {
	var fp dt.Filepath
	var data []byte
	var exists bool

	fp = dt.FilepathJoin3(root, ProjectDir, ProjectConfigFile)

	exists, err = fp.Exists()
	if err != nil {
		err = doterr.NewErr(ErrInvalidProjectConfig, err)
		goto end
	}
	if !exists {
		goto end
	}

	data, err = fp.ReadFile()
	if err != nil {
		err = doterr.NewErr(ErrInvalidProjectConfig, ErrReadingManifest, err)
		goto end
	}

	pc, err = ParseProjectConfig(data)
	if err != nil {
		goto end
	}

end:
	if err != nil {
		err = doterr.WithErr(err, "filepath", fp)
	}
	return pc, err
}
