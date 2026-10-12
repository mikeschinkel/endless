package mainsyncjob

import (
	"fmt"
	"os"
	"testing"

	"github.com/mikeschinkel/endless/internal/settings"
)

// TestMain freezes the settings registry, as endless-go's startup does, so
// the readers under test resolve unset opt-ins against registered defaults.
func TestMain(m *testing.M) {
	if err := settings.Initialize(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}
