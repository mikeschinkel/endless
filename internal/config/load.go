package config

import (
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
