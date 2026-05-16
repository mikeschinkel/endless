package monitor

import (
	"path/filepath"
	"testing"
)

func TestIsSandboxActive(t *testing.T) {
	cache := t.TempDir()
	sandboxRoot := filepath.Join(cache, "endless", "sandboxes")
	otherConfig := filepath.Join(t.TempDir(), "elsewhere")

	cases := []struct {
		name      string
		xdgCfg    string
		xdgCache  string
		want      bool
	}{
		{
			name:     "no XDG_CONFIG_HOME, default ~/.config",
			xdgCfg:   "",
			xdgCache: cache,
			want:     false,
		},
		{
			name:     "config under sandbox root",
			xdgCfg:   filepath.Join(sandboxRoot, "worktree-e-1354"),
			xdgCache: cache,
			want:     true,
		},
		{
			name:     "config outside sandbox root",
			xdgCfg:   otherConfig,
			xdgCache: cache,
			want:     false,
		},
		{
			name:     "config in unrelated sandboxes-named dir",
			xdgCfg:   filepath.Join(t.TempDir(), "fake", "sandboxes", "x"),
			xdgCache: cache,
			want:     false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", tc.xdgCfg)
			t.Setenv("XDG_CACHE_HOME", tc.xdgCache)
			if got := IsSandboxActive(); got != tc.want {
				t.Errorf("IsSandboxActive() = %v, want %v (XDG_CONFIG_HOME=%q XDG_CACHE_HOME=%q)",
					got, tc.want, tc.xdgCfg, tc.xdgCache)
			}
		})
	}
}
