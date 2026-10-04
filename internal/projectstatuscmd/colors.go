package projectstatuscmd

import (
	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/config"
)

// listStylesFor resolves each list's colors from `project_status.colors` in
// the layered config, over defaultListStyles. A config that cannot be read
// leaves the defaults: colors are a preference, and a typo in one must not
// stop the view from rendering. An index outside 0–255 other than -1 is
// ignored for the same reason.
func listStylesFor(projectDir string) *listStyles {
	st := defaultListStyles
	cfg, err := config.Load(dt.DirPath(projectDir))
	if err != nil || cfg == nil {
		return &st
	}
	c := cfg.ProjectStatus.Colors
	for l, lc := range map[list]config.ListColor{listUrgent: c.Urgent, listEpics: c.Epics, listOther: c.Other} {
		if v, ok := colorIndex(lc.BG); ok {
			st[l].bg = v
		}
		if v, ok := colorIndex(lc.FG); ok {
			st[l].fg = v
		}
	}
	return &st
}

// colorIndex validates one configured index: nil is unset, -1 is "none".
func colorIndex(p *int) (int, bool) {
	if p == nil || *p < -1 || *p > 255 {
		return 0, false
	}
	return *p, true
}
