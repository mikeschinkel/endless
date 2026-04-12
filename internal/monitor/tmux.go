package monitor

import (
	"os"
	"os/exec"
	"strings"

	"crypto/rand"
	"fmt"
)

// TmuxContext holds the UUID-based identity of the current tmux context.
type TmuxContext struct {
	SessionUUID string
	WindowUUID  string
	PaneUUID    string
	SessionName string
	WindowName  string
}

// InTmux returns true if we're running inside a tmux session.
func InTmux() bool {
	return os.Getenv("TMUX") != ""
}

// GetTmuxContext reads or creates UUIDs for the current tmux
// session, window, and pane.
func GetTmuxContext() (*TmuxContext, error) {
	if !InTmux() {
		return nil, nil
	}

	ctx := &TmuxContext{}

	// Get session name
	ctx.SessionName = tmuxDisplay("#{session_name}")
	ctx.WindowName = tmuxDisplay("#{window_name}")

	// Ensure UUIDs exist
	var err error
	ctx.SessionUUID, err = ensureTmuxUUID("", "@session_uuid")
	if err != nil {
		return ctx, err
	}
	ctx.WindowUUID, err = ensureTmuxUUID("", "@window_uuid")
	if err != nil {
		return ctx, err
	}
	ctx.PaneUUID, err = ensureTmuxUUID("", "@pane_uuid")
	if err != nil {
		return ctx, err
	}

	return ctx, nil
}

// ToMap converts TmuxContext to a string map for JSON storage.
func (tc *TmuxContext) ToMap() map[string]string {
	if tc == nil {
		return nil
	}
	return map[string]string{
		"session_uuid": tc.SessionUUID,
		"window_uuid":  tc.WindowUUID,
		"pane_uuid":    tc.PaneUUID,
		"session_name": tc.SessionName,
		"window_name":  tc.WindowName,
	}
}

func tmuxDisplay(format string) string {
	out, err := exec.Command("tmux", "display-message", "-p", format).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func ensureTmuxUUID(target, key string) (string, error) {
	// Read existing
	args := []string{"display-message", "-p", fmt.Sprintf("#{%s}", key)}
	if target != "" {
		args = []string{"display-message", "-p", "-t", target, fmt.Sprintf("#{%s}", key)}
	}
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return "", err
	}
	uuid := strings.TrimSpace(string(out))
	if uuid != "" {
		return uuid, nil
	}

	// Generate and set
	uuid = generateUUID()
	setArgs := []string{"set-option", key, uuid}
	if target != "" {
		setArgs = []string{"set-option", "-t", target, key, uuid}
	}
	err = exec.Command("tmux", setArgs...).Run()
	if err != nil {
		return "", err
	}

	return uuid, nil
}

func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
