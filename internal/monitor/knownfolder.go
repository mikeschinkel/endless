package monitor

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// knownFolder is where this OS keeps a user folder named by its English key
// (Downloads, Documents, ...). Endless keeps no translation tables: the OS
// already knows. False when this machine has no such folder.
//
//   - macOS: fixed English names on disk; Finder only localizes the display.
//   - Linux: xdg-user-dirs records the real, localized paths in user-dirs.dirs.
//     A server without that file has none of these folders, so none apply.
//   - Windows: the known-folder API, which follows a relocated folder.
func knownFolder(goos, key, home string) (string, bool) {
	switch goos {
	case "darwin":
		return filepath.Join(home, darwinFolderName(key)), true
	case "windows":
		return windowsKnownFolder(key, home)
	}
	return xdgUserDir(key, home)
}

// darwinFolderName is the folder's name on macOS, which calls Videos Movies.
func darwinFolderName(key string) string {
	if key == "Videos" {
		return "Movies"
	}
	return key
}

// xdgKeys maps a known-folder key to its user-dirs.dirs variable.
var xdgKeys = map[string]string{
	"Desktop":   "XDG_DESKTOP_DIR",
	"Documents": "XDG_DOCUMENTS_DIR",
	"Downloads": "XDG_DOWNLOAD_DIR",
	"Music":     "XDG_MUSIC_DIR",
	"Pictures":  "XDG_PICTURES_DIR",
	"Videos":    "XDG_VIDEOS_DIR",
	"Public":    "XDG_PUBLICSHARE_DIR",
	"Templates": "XDG_TEMPLATES_DIR",
}

// xdgUserDir reads one folder from user-dirs.dirs, whose lines look like
// XDG_DOWNLOAD_DIR="$HOME/Downloads". A folder the user disabled is set to
// "$HOME/" and is reported absent.
func xdgUserDir(key, home string) (string, bool) {
	name, ok := xdgKeys[key]
	if !ok {
		return "", false
	}
	cfgHome := os.Getenv("XDG_CONFIG_HOME")
	if cfgHome == "" {
		cfgHome = filepath.Join(home, ".config")
	}
	f, err := os.Open(filepath.Join(cfgHome, "user-dirs.dirs"))
	if err != nil {
		return "", false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, found := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !found || k != name {
			continue
		}
		v = strings.Trim(v, `"`)
		switch {
		case v == "$HOME" || v == "$HOME/":
			return "", false
		case strings.HasPrefix(v, "$HOME/"):
			return filepath.Join(home, v[len("$HOME/"):]), true
		case filepath.IsAbs(v):
			return v, true
		}
		return "", false
	}
	return "", false
}
