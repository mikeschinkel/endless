package tmuxcmd

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mikeschinkel/endless/internal/refusal"
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
	fs := refusal.NewFlags("init")
	binary := fs.String("binary", "", "Override path to endless binary passed to `apply` (default: argv[0])")
	prefixKey := fs.String("hotkey", "e", "Prefix-table key passed to `apply`")
	interval := fs.Int("status-interval", 2, "tmux status-interval passed to `apply`")
	if err := fs.Parse(args); err != nil {
		// -h asked a question and got an answer; it is not a refusal.
		fs.ExitOnHelp(err)
		// NewFlags is ContinueOnError, so the exit flag.ExitOnError used to take
		// from inside Parse is ours to take here. Both callers — the
		// session-created hook and a manual run — pass flags this verb defines,
		// so a flag that misses was typed.
		refusal.NoReport(err.Error(), "Correct the flag and retry").
			Command("tmux init").Text(fs.Output()).Exit(2)
	}

	if os.Getenv("TMUX") == "" {
		// Only the user can start or attach to their own tmux, so there is no
		// retry an agent can make. Never fires from the session-created hook,
		// where $TMUX is set by definition: this is the manual path.
		refusal.Report(
			"endless-go tmux init: not inside a tmux session ($TMUX is empty)",
			"running `endless tmux init` from inside their own tmux session",
		).Command("tmux init").Exit(1)
	}

	existing := strings.TrimSpace(readServerOption("@server_uuid"))
	if existing != "" {
		// Server already initialized this lifetime. Quiet no-op so the
		// session-created hook firing for every subsequent session
		// doesn't spam stderr.
		//
		// Nothing is broken and nothing is left to do — the gate did its job —
		// so an agent that reads it carries straight on.
		refusal.NoReport(
			fmt.Sprintf("endless-go tmux init: server already initialized (@server_uuid=%s)", existing),
			"Nothing to do: this tmux server is already initialized",
		).Command("tmux init").Print()
		return
	}

	uuid, err := newUUID()
	if err != nil {
		// crypto/rand failed. Nothing about the invocation is wrong and no retry
		// changes it: the machine could not produce random bytes.
		refusal.Faultf("endless-go tmux init: generate uuid: %v", err).
			Command("tmux init").Cause(err).Exit(1)
	}

	// This used to call runReset(nil) first, and that call was the trigger for
	// the 2026-08-05 incident: a freshly started tmux server fires
	// session-created -> `endless tmux init`, so the reaper ran against a
	// ONE-PANE view of a brand-new server and concluded every session in the
	// project was dead — 37 rows in a single batch. E-1898 removed the reaper
	// outright; liveness is now derived per read and never swept.
	//
	// Order still matters: stamp @server_uuid LAST so a crash mid-init leaves
	// the gate open for a retry. apply is idempotent.
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
		// tmux printed its own reason just above (setServerOption passes its
		// stderr through). apply already ran and re-running init is safe — the
		// gate is still open and apply is idempotent — but why this server
		// refused a set-option is the user's to look into.
		refusal.Report(
			fmt.Sprintf("endless-go tmux init: set @server_uuid: %v", err),
			"why their tmux server refused the @server_uuid option",
		).Command("tmux init").Exit(1)
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
	// tmux's own diagnostic, verbatim: it names the option and the version
	// constraint that the caller's refusal cannot.
	cmd.Stderr = refusal.Passthrough()
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
