// Package shippedcmd implements `endless-go shipped`: placing one of Endless's
// shipped files (internal/shipped) into a project, as `endless project init`
// does on every run (E-2421).
//
// It writes the file but records nothing. The record of what was written is a
// shipped_file.written event, which the Python CLI emits from the JSON this
// prints, through the same `event emit` every other ledger write takes.
package shippedcmd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/mikeschinkel/endless/internal/dbprovenance"
	"github.com/mikeschinkel/endless/internal/events"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/shipped"
)

// Run dispatches `endless-go shipped <verb>`.
func Run(args []string) {
	if len(args) < 1 {
		refusal.NoReport(
			"endless-go shipped: no subcommand given",
			"Re-run with a subcommand from the printed list",
		).Command("shipped").Text(usageText()).Exit(2)
	}
	verb, rest := args[0], args[1:]
	var err error
	switch verb {
	case "place":
		err = runPlace(rest)
	case "-h", "--help", "help":
		refusal.Info(usageText()).Print()
		return
	default:
		refusal.ReportIf(
			fmt.Sprintf("endless-go shipped: unknown subcommand %q", verb),
			"an `endless` verb relayed this rather than you typing endless-go yourself",
			"retry with a subcommand from the printed list",
			"the installed endless-go is older than the endless CLI calling it, and only the user can reinstall a matching pair",
		).Command("shipped").Detail(usageText()).Exit(2)
	}
	if err != nil {
		refusal.From(err).Command("shipped " + verb).Exit(1)
	}
}

func usageText() string {
	var b strings.Builder
	fmt.Fprintln(&b, "Usage: endless-go shipped <subcommand> [args...]")
	fmt.Fprintln(&b, "Subcommands:")
	fmt.Fprintln(&b, "  place <kind> <project> <dir>   write a shipped file into a project unless its copy was edited;")
	fmt.Fprintln(&b, "                                 JSON: kind, path, action (created|updated|current|edited), sha256")
	fmt.Fprintf(&b, "Kinds: %s\n", strings.Join(kindNames(), ", "))
	return b.String()
}

func kindNames() []string {
	var names []string
	for _, k := range shipped.Kinds() {
		names = append(names, string(k))
	}
	return names
}

// placeResult is what `place` prints. SHA256 is the hash of the shipped
// content — what was written, when Action writes.
type placeResult struct {
	Kind   shipped.Kind   `json:"kind"`
	Path   string         `json:"path"`
	Action shipped.Action `json:"action"`
	SHA256 string         `json:"sha256"`
}

func runPlace(args []string) error {
	if len(args) != 3 {
		return refusal.NoReport(
			fmt.Sprintf("endless-go shipped place: expected <kind> <project> <dir>, got %d arguments", len(args)),
			"Pass a kind, a registered project name and its directory, and retry")
	}
	file, ok := shipped.Lookup(shipped.Kind(args[0]))
	if !ok {
		return refusal.NoReport(
			fmt.Sprintf("endless-go shipped place: unknown kind %q", args[0]),
			fmt.Sprintf("Retry with one of: %s", strings.Join(kindNames(), ", ")))
	}
	project := args[1]
	dir, err := monitor.ResolvedProjectPath(args[2])
	if err != nil {
		return err
	}
	content, err := file.Content()
	if err != nil {
		return err
	}
	current := shipped.Hash(content)

	target := filepath.Join(dir, filepath.FromSlash(file.Path))
	existing, err := os.ReadFile(target)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		existing = nil
	case err != nil:
		return fmt.Errorf("shipped: read %s: %w", target, err)
	}

	db, err := monitor.DB()
	if err != nil {
		return err
	}
	recorded, err := events.RecordedShippedFile(db, project, file.Path)
	if err != nil {
		return err
	}

	action := file.Decide(existing, recorded, current)
	if action.Writes() {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("shipped: create %s: %w", filepath.Dir(target), err)
		}
		if err := os.WriteFile(target, content, 0o644); err != nil {
			return fmt.Errorf("shipped: write %s: %w", target, err)
		}
	}
	return dbprovenance.Encode(os.Stdout, placeResult{
		Kind: file.Kind, Path: file.Path, Action: action, SHA256: current,
	})
}
