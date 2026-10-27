package refusal

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSlogHandlerCreatesNothingUntilItLogs pins the lazy open (E-2296): every
// endless-go subcommand installs this handler, and one that logs nothing must
// leave the config directory as it found it.
func TestSlogHandlerCreatesNothingUntilItLogs(t *testing.T) {
	dir := t.TempDir()
	logger := slog.New(SlogHandler(dir, "x.log", slog.LevelWarn))

	logger.Info("below the level")
	if _, err := os.Stat(filepath.Join(dir, LogDirName)); !os.IsNotExist(err) {
		t.Fatalf("log dir exists before anything was logged (stat err = %v)", err)
	}

	logger.Warn("a config file could not be closed")
	got, err := os.ReadFile(filepath.Join(dir, LogDirName, "x.log"))
	if err != nil {
		t.Fatalf("read log after a warning: %v", err)
	}
	if !strings.Contains(string(got), "a config file could not be closed") {
		t.Errorf("log = %q, want the warning", got)
	}
}
