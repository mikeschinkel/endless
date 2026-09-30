package config

import (
	"errors"

	"github.com/mikeschinkel/go-cfgstore"
	"github.com/mikeschinkel/go-doterr"
	"github.com/mikeschinkel/go-dt"
)

// Load reads and merges the Endless configuration for a specific project.
// Loads ~/.config/endless/config.json (CLI layer) and merges
// <projectPath>/.endless/config.json (project layer) on top of it. Project
// values override CLI values per the merge rules in EndlessConfig.Merge.
//
// Pass an empty projectPath to load the CLI layer alone. A non-empty
// projectPath that does not contain an .endless/config.json file silently
// falls back to the CLI layer.
//
// Always returns a non-nil *EndlessConfig on success. Errors from missing
// CLI config (file does not exist) are treated as "no values set"; only
// real read or parse failures bubble up.
func Load(projectPath dt.DirPath) (cfg *EndlessConfig, err error) {
	dp := cfgstore.DefaultDirsProvider()
	if projectPath != "" {
		dp.ProjectDirFunc = func() (dt.DirPath, error) {
			return projectPath, nil
		}
	}
	cfg, err = cfgstore.LoadDefaultConfig[EndlessConfig, *EndlessConfig](cfgstore.LoadConfigArgs{
		ConfigSlug:   ConfigSlug,
		ConfigFile:   ConfigFile,
		DirsProvider: dp,
	})
	if err != nil {
		err = doterr.NewErr(
			ErrFailedToLoadConfig,
			doterr.StringKV("project_path", string(projectPath)),
			err,
		)
		goto end
	}
end:
	return cfg, err
}

// LoadProject reads the PROJECT layer alone: <projectPath>/.endless/config.json,
// with nothing inherited from the CLI layer. It is how project-only settings
// are read — the ones that must never fall through to a user-level value, such
// as AutoSpawn.Enabled.
//
// A project with no config file returns a zero config and no error: an absent
// file sets nothing, which for every project-only setting means "off".
func LoadProject(projectPath dt.DirPath) (cfg *EndlessConfig, err error) {
	dp := cfgstore.DefaultDirsProvider()
	dp.ProjectDirFunc = func() (dt.DirPath, error) {
		return projectPath, nil
	}
	cfg, err = cfgstore.LoadConfig[EndlessConfig, *EndlessConfig](cfgstore.LoadConfigArgs{
		ConfigSlug:   ConfigSlug,
		ConfigFile:   ConfigFile,
		DirsProvider: dp,
		DirTypes:     []cfgstore.DirType{cfgstore.ProjectConfigDirType},
	})
	if errors.Is(err, cfgstore.ErrNotValidConfigDirsAvailable) {
		cfg, err = &EndlessConfig{}, nil
		goto end
	}
	if err != nil {
		err = doterr.NewErr(
			ErrFailedToLoadConfig,
			doterr.StringKV("project_path", string(projectPath)),
			err,
		)
		goto end
	}
	if cfg == nil {
		cfg = &EndlessConfig{}
	}
end:
	return cfg, err
}
