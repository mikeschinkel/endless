package sandboxcmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
)

type sandboxState string

const (
	stateLive     sandboxState = "live"
	stateInUse    sandboxState = "in-use"
	stateOrphaned sandboxState = "orphaned"
)

type listEntry struct {
	Meta  SandboxMeta
	State sandboxState
	Age   time.Duration
	Size  int64
	Dir   string
}

func listCmd(args []string) {
	flags := flag.NewFlagSet("list", flag.ExitOnError)
	if err := flags.Parse(args); err != nil {
		os.Exit(2)
	}

	// A guard failure is not fatal to a read-only listing: classify() falls
	// back to reporting every worktree-bound sandbox as in-use, which is the
	// conservative direction — it can only under-report orphans, never invite
	// a reap of protected work.
	guard, err := NewReapGuard(mainCheckoutRoot())
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox list: %v\n", err)
		guard = nil
	}

	entries, err := scanSandboxes(guard)
	if err != nil {
		fmt.Fprintf(os.Stderr, "endless-sandbox list: %v\n", err)
		os.Exit(1)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tMODE\tSTATE\tAGE\tSIZE\tCREATOR_PID")
	for _, e := range entries {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%d\n",
			e.Meta.Name, e.Meta.Mode, e.State, humanDuration(e.Age), humanSize(e.Size), e.Meta.CreatorPID)
	}
	w.Flush()
}

func scanSandboxes(guard *ReapGuard) ([]listEntry, error) {
	dir := sandboxesDir()
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []listEntry
	now := time.Now().UTC()
	for _, ent := range dirEntries {
		if !ent.IsDir() {
			continue
		}
		sbDir := filepath.Join(dir, ent.Name())
		meta, err := readMeta(sbDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "endless-sandbox list: skipping %s: %v\n", sbDir, err)
			continue
		}
		size, _ := dirSize(sbDir)
		out = append(out, listEntry{
			Meta:  meta,
			State: classify(meta, guard),
			Age:   now.Sub(meta.CreatedAt),
			Size:  size,
			Dir:   sbDir,
		})
	}
	return out, nil
}

// classify reports a sandbox's state.
//
// Worktree-bound sandboxes (keep/persistent) used to return stateInUse
// unconditionally, which made them permanently unreclaimable: prune only
// removes stateOrphaned, so `endless-sandbox prune` could never touch one no
// matter how long its worktree had been gone (E-1904). They now consult the
// guard, and report orphaned once every protection condition is false.
//
// A nil guard means the environment could not be sampled. That falls back to
// the old unconditional stateInUse — under-reporting orphans is recoverable,
// reaping protected work is not.
func classify(meta SandboxMeta, guard *ReapGuard) sandboxState {
	var protected bool

	state := stateOrphaned

	if meta.Mode != modeKeep && meta.Mode != modePersistent {
		if isAlive(meta.CreatorPID) {
			state = stateLive
		}
		goto end
	}

	if guard == nil {
		state = stateInUse
		goto end
	}

	protected, _ = guard.Protected(meta.Name)
	if protected {
		state = stateInUse
	}

end:
	return state
}

// mainCheckoutRoot resolves the main checkout, which is where worktrees and
// branches are enumerated from. `--git-common-dir` points at the shared .git
// even when cwd is inside a linked worktree (whose own .git is a pointer file),
// so this returns the main checkout from anywhere in the repo.
//
// Returns "" when cwd is not in a git repo; NewReapGuard then fails, and every
// caller falls back to its own conservative default.
func mainCheckoutRoot() (root string) {
	out, err := runGuardCmd("", "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		goto end
	}
	root = filepath.Dir(strings.TrimSpace(out))

end:
	return root
}

func dirSize(dir string) (int64, error) {
	var size int64
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			size += info.Size()
		}
		return nil
	})
	return size, err
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

func humanSize(n int64) string {
	const (
		kb = 1 << 10
		mb = 1 << 20
		gb = 1 << 30
	)
	switch {
	case n >= gb:
		return fmt.Sprintf("%.1fG", float64(n)/gb)
	case n >= mb:
		return fmt.Sprintf("%.1fM", float64(n)/mb)
	case n >= kb:
		return fmt.Sprintf("%.1fK", float64(n)/kb)
	default:
		return fmt.Sprintf("%dB", n)
	}
}
