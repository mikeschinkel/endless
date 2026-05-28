package sandboxcmd

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
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

	entries, err := scanSandboxes()
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

func scanSandboxes() ([]listEntry, error) {
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
			State: classify(meta),
			Age:   now.Sub(meta.CreatedAt),
			Size:  size,
			Dir:   sbDir,
		})
	}
	return out, nil
}

func classify(meta SandboxMeta) sandboxState {
	if meta.Mode == modeKeep || meta.Mode == modePersistent {
		return stateInUse
	}
	if isAlive(meta.CreatorPID) {
		return stateLive
	}
	return stateOrphaned
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
