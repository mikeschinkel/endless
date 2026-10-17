//go:build windows

package monitor

import (
	"os"

	"golang.org/x/sys/windows"
)

var windowsFolderIDs = map[string]*windows.KNOWNFOLDERID{
	"Desktop":   windows.FOLDERID_Desktop,
	"Documents": windows.FOLDERID_Documents,
	"Downloads": windows.FOLDERID_Downloads,
	"Music":     windows.FOLDERID_Music,
	"Pictures":  windows.FOLDERID_Pictures,
	"Videos":    windows.FOLDERID_Videos,
}

// windowsKnownFolder asks Windows where a user folder is, so a folder moved by
// OneDrive redirection or to another drive still matches. OneDrive has no
// known-folder ID; its client sets %OneDrive%.
func windowsKnownFolder(key, _ string) (string, bool) {
	if key == "OneDrive" {
		p := os.Getenv("OneDrive")
		return p, p != ""
	}
	id, ok := windowsFolderIDs[key]
	if !ok {
		return "", false
	}
	p, err := windows.KnownFolderPath(id, windows.KF_FLAG_DEFAULT)
	if err != nil || p == "" {
		return "", false
	}
	return p, true
}
