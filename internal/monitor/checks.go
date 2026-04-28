package monitor

import (
	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/go-dt"
)

// IsCheckEnabled returns whether the named check is enabled for the
// project, honoring the layered Endless config (CLI defaults plus
// per-project overrides) and falling back to config.DefaultCheckEnabled
// when neither layer specifies the key.
//
// On any DB or config-load failure this defers to the hardcoded default
// rather than blocking, matching the conservative behavior of the prior
// bespoke reader.
func IsCheckEnabled(projectID int64, name string) bool {
	db, err := DB()
	if err != nil {
		return config.DefaultCheckEnabled(name)
	}
	var projectPath string
	err = db.QueryRow(`SELECT path FROM projects WHERE id=?`, projectID).Scan(&projectPath)
	if err != nil {
		return config.DefaultCheckEnabled(name)
	}
	cfg, err := config.Load(dt.DirPath(projectPath))
	if err != nil {
		return config.DefaultCheckEnabled(name)
	}
	return cfg.IsCheckEnabled(name)
}
