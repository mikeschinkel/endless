//go:build !windows

package monitor

// windowsKnownFolder has no answer off Windows.
func windowsKnownFolder(string, string) (string, bool) {
	return "", false
}
