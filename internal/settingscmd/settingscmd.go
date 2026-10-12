// Package settingscmd implements `endless-go settings` — the export of the
// settings registry (E-2284).
//
//	endless-go settings list [--json] [--project DIR]
//	endless-go settings migrate [FILE...]
//
// `list` is how anything outside Go learns the registered keys: Python's
// `endless project set` takes its field list and accepted values from
// `list --json`, rather than parsing config files for registered keys itself.
//
// `migrate` rewrites legacy JSON booleans at opt-in keys to "on" / "off", the
// one-time step an existing config needs before this binary will load it.
package settingscmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/settings"
)

// Run dispatches the `settings` subcommand.
func Run(args []string) {
	if len(args) == 0 {
		refusal.NoReport(
			"endless-go settings: no command given",
			"Re-run with a verb — list [--json] [--project DIR] or migrate [FILE...]",
		).Command("settings").Text(usageText()).Exit(2)
	}
	switch args[0] {
	case "list":
		runList(args[1:])
	case "migrate":
		runMigrate(args[1:])
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usageText())
	default:
		refusal.NoReport(
			fmt.Sprintf("endless-go settings: unknown command %q", args[0]),
			"Pick one of the listed verbs and retry",
		).Command("settings").Detail(usageText()).Exit(2)
	}
}

func usageText() string {
	return strings.Join([]string{
		"Usage: endless-go settings <command>",
		"Commands:",
		"  list [--json] [--project DIR]  every registered key, its effective value and where it came from",
		"       [--metadata]            the keys alone: no config file is read",
		"  migrate [FILE...]              rewrite legacy true/false opt-ins to \"on\"/\"off\"",
		"                                 (default: the machine file and this directory's project file)",
	}, "\n") + "\n"
}

// Entry is one setting as `list --json` exports it.
type Entry struct {
	ID                string   `json:"id"`
	Feature           string   `json:"feature"`
	Files             string   `json:"files"`
	Kind              string   `json:"kind"`
	Choices           []string `json:"choices,omitempty"`
	Suggestable       bool     `json:"suggestable"`
	Rationale         string   `json:"rationale,omitempty"`
	Default           string   `json:"default"`
	PreconditionCheck string   `json:"precondition_check,omitempty"`

	// Value is the effective value in the setting's own spelling.
	Value string `json:"value"`

	// Source is where Value came from: "project", "machine" or "default". An
	// opt-in whose source is "default" is unset.
	Source string `json:"source"`
}

// Export is the whole `list --json` document.
type Export struct {
	MachineFile string  `json:"machine_file"`
	ProjectFile string  `json:"project_file,omitempty"`
	Settings    []Entry `json:"settings"`
}

func runList(args []string) {
	fs := refusal.NewFlags("list")
	asJSON := fs.Bool("json", false, "print the export as JSON")
	project := fs.String("project", "", "project directory (default: the nearest enclosing one)")
	metadata := fs.Bool("metadata", false, "registry only: read no config file, report defaults as values")
	if err := fs.Parse(args); err != nil {
		fs.ExitOnHelp(err)
		refusal.NoReport(err.Error(),
			"Only --json, --metadata and --project DIR exist here: correct the flag and retry").
			Command("settings list").Text(fs.Output()).Exit(2)
	}

	var exp Export
	var err error
	if *metadata {
		exp, err = Metadata()
	} else {
		exp, err = Build(dt.DirPath(*project))
	}
	if err != nil {
		listRefusal(err).Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err = enc.Encode(exp); err != nil {
			refusal.Fault(err).Command("settings list").Exit(1)
		}
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tVALUE\tSOURCE\tFILES\tSUGGESTABLE")
	for _, e := range exp.Settings {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%t\n", e.ID, e.Value, e.Source, e.Files, e.Suggestable)
	}
	if err = tw.Flush(); err != nil {
		refusal.Fault(err).Command("settings list").Exit(1)
	}
}

// listRefusal classifies a Build failure. An opt-in a config file spells
// wrong is the agent's to fix — each line names the file, the key and what to
// write — so it is a NO-REPORT; anything else is Endless failing.
func listRefusal(err error) *refusal.Error {
	return OptInRefusal(err, "settings list")
}

// OptInRefusal renders err as one line per bad opt-in when it holds any, and
// as a fault otherwise.
func OptInRefusal(err error, command string) *refusal.Error {
	bad := config.OptInErrors(err)
	if len(bad) == 0 {
		return refusal.Fault(err).Command(command)
	}
	lines := make([]string, len(bad))
	for i, e := range bad {
		lines[i] = e.Error()
	}
	return refusal.NoReport(
		"A config file holds an opt-in Endless cannot read. Nothing was changed.",
		"Rewrite each value named below — `endless-go settings migrate FILE` rewrites "+
			"legacy true/false for you — then retry",
	).Command(command).Detail(strings.Join(lines, "\n")).Cause(err)
}

// Metadata is the export without values: every setting at its default, and
// no config file read. It is what a caller that only needs to know which keys
// exist asks for, so a malformed file elsewhere cannot stand in its way.
func Metadata() (exp Export, err error) {
	var all []settings.Setting
	all, err = settings.All()
	if err != nil {
		goto end
	}
	exp.Settings = make([]Entry, 0, len(all))
	for _, s := range all {
		exp.Settings = append(exp.Settings, entryFor(s))
	}
end:
	return exp, err
}

// entryFor is s with its default as its value.
func entryFor(s settings.Setting) Entry {
	return Entry{
		ID:                string(s.ID),
		Feature:           s.Feature,
		Files:             s.Files.String(),
		Kind:              s.Kind.String(),
		Choices:           s.Choices,
		Suggestable:       s.Suggestable,
		Rationale:         s.Rationale,
		Default:           s.Default,
		PreconditionCheck: s.PreconditionCheck,
		Value:             s.Default,
		Source:            "default",
	}
}

// Build assembles the export for projectDir, or for the project enclosing
// the working directory when projectDir is empty. Both files are loaded
// through config first, so a malformed opt-in is refused with its file and
// key before anything is reported.
func Build(projectDir dt.DirPath) (exp Export, err error) {
	var machineFile, projectFile dt.Filepath
	var machineRaw, projectRaw map[string]any
	var all []settings.Setting

	if projectDir == "" {
		projectDir = enclosingProject()
	}
	machineFile, err = config.MachineFile()
	if err != nil {
		goto end
	}
	exp.MachineFile = string(machineFile)
	if projectDir != "" {
		projectFile = config.ProjectFile(projectDir)
		exp.ProjectFile = string(projectFile)
	}

	// Validate through the real loader — it is what every reader uses.
	_, err = config.Load(projectDir)
	if err != nil {
		goto end
	}
	machineRaw, err = readRaw(machineFile)
	if err != nil {
		goto end
	}
	if projectFile != "" {
		projectRaw, err = readRaw(projectFile)
		if err != nil {
			goto end
		}
	}

	all, err = settings.All()
	if err != nil {
		goto end
	}
	exp.Settings = make([]Entry, 0, len(all))
	for _, s := range all {
		e := entryFor(s)
		// The project file wins, as it does in the merge; a file the setting
		// may not live in is not consulted, as its reader does not consult it.
		switch {
		case s.Files.Has(settings.ProjectFile) && setIn(projectRaw, s, &e.Value):
			e.Source = "project"
		case s.Files.Has(settings.MachineFile) && setIn(machineRaw, s, &e.Value):
			e.Source = "machine"
		}
		exp.Settings = append(exp.Settings, e)
	}

end:
	return exp, err
}

// setIn looks the setting up in one file's raw JSON. It reports whether the
// file decides the value — a key written "unset" does not — and if so stores
// the value's text in value.
func setIn(raw map[string]any, s settings.Setting, value *string) (ok bool) {
	var text string

	v, found := lookup(raw, string(s.ID))
	if !found {
		goto end
	}
	switch tv := v.(type) {
	case string:
		text = tv
	case float64:
		text = strconv.FormatFloat(tv, 'f', -1, 64)
	case bool:
		text = strconv.FormatBool(tv)
	default:
		b, _ := json.Marshal(tv)
		text = string(b)
	}
	if s.Kind == settings.OptInKind && (text == "" || text == settings.UnsetText) {
		goto end
	}
	*value = text
	ok = true
end:
	return ok
}

// lookup walks a dotted path through nested JSON objects.
func lookup(raw map[string]any, path string) (v any, ok bool) {
	var cur any = raw
	for seg := range strings.SplitSeq(path, ".") {
		m, isMap := cur.(map[string]any)
		if !isMap {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// readRaw reads a config file as generic JSON. A missing file is empty.
func readRaw(file dt.Filepath) (raw map[string]any, err error) {
	var b []byte
	b, err = os.ReadFile(string(file))
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
		goto end
	}
	if err != nil {
		goto end
	}
	err = json.Unmarshal(b, &raw)
end:
	return raw, err
}

// enclosingProject is the nearest directory at or above the working directory
// holding .endless/config.json, or "" when there is none. No database: this
// command answers about files.
func enclosingProject() dt.DirPath {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err = os.Stat(string(config.ProjectFile(dt.DirPath(dir)))); err == nil {
			return dt.DirPath(dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
