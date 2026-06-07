package tmuxcmd

import (
	"crypto/rand"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// runInit is the target for `set-hook -g session-created "run-shell
// 'endless tmux init'"` in ~/.tmux.conf. Self-gates via the tmux
// server-level user option @server_uuid: on a fresh server (no
// @server_uuid) it runs reset+apply and stamps a fresh UUID; on a
// subsequent session-created in the same server (which fires for every
// new tmux session, not just the first) it no-ops.
//
// Why the gate is the verb's responsibility: tmux's `if-shell`
// alternative would force users to write the gate inline in their
// .tmux.conf, where it would diverge from the verb's logic. Owning the
// gate here means the user's config is one stable line.
//
// Manual `endless tmux init` after `tmux kill-server`+`tmux new` works
// the same way (idempotent: first call does work, subsequent calls
// no-op until the next server restart).
func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	binary := fs.String("binary", "", "Override path to endless binary passed to `apply` (default: argv[0])")
	prefixKey := fs.String("hotkey", "e", "Prefix-table key passed to `apply`")
	interval := fs.Int("status-interval", 2, "tmux status-interval passed to `apply`")
	fs.Parse(args)

	if os.Getenv("TMUX") == "" {
		fmt.Fprintln(os.Stderr, "endless-go tmux init: not inside a tmux session ($TMUX is empty)")
		os.Exit(1)
	}

	existing := strings.TrimSpace(readServerOption("@server_uuid"))
	if existing != "" {
		// Server already initialized this lifetime. Quiet no-op so the
		// session-created hook firing for every subsequent session
		// doesn't spam stderr.
		fmt.Fprintf(os.Stderr, "endless-go tmux init: server already initialized (@server_uuid=%s)\n", existing)
		return
	}

	uuid, err := newUUID()
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-go tmux init: generate uuid: %v\n", err)
		os.Exit(1)
	}

	// Order matters: stamp @server_uuid LAST so a crash mid-init still
	// leaves the gate open for a retry. reset/apply are idempotent.
	runReset(nil)

	applyArgs := []string{}
	if *binary != "" {
		applyArgs = append(applyArgs, "--binary="+*binary)
	}
	if *prefixKey != "e" {
		applyArgs = append(applyArgs, "--hotkey="+*prefixKey)
	}
	if *interval != 2 {
		applyArgs = append(applyArgs, fmt.Sprintf("--status-interval=%d", *interval))
	}
	runApply(applyArgs)

	if err := setServerOption("@server_uuid", uuid); err != nil {
		fmt.Fprintf(os.Stderr, "endless-go tmux init: set @server_uuid: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("endless-go tmux init: server initialized (@server_uuid=%s)\n", uuid)
}

// readServerOption returns the value of a tmux server-level user option,
// or "" if unset (including when tmux is unavailable). Stderr is swallowed
// because `tmux show-options -gv <unset>` exits non-zero with a message
// the caller doesn't need.
func readServerOption(name string) string {
	out, err := exec.Command("tmux", "show-options", "-gv", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func setServerOption(name, value string) error {
	cmd := exec.Command("tmux", "set-option", "-g", name, value)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// newUUID returns an 8-4-4-4-12 hex UUID (v4-shaped) derived from
// crypto/rand. Used only as a server-lifetime marker; the exact RFC
// variant/version bits don't matter — uniqueness across server restarts
// is all the gate needs.
func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	// Set version (4) and variant (RFC 4122) bits for correctness even
	// though the gate doesn't validate them.
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
