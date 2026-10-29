// Package shipped holds the files Endless writes into every project it
// manages, every version of them it has ever shipped, and the rule for when
// re-running `endless project init` may replace a project's copy (E-2421).
//
// Each file is an embedded Go template, so every install carries the current
// text and needs no download or setup. Every version it ever shipped is kept
// only as a SHA-256 of its rendered bytes (versions.go): "is this file exactly
// something Endless wrote?" needs the hash, never the old text.
//
// What init last wrote into a particular project is not kept here. It belongs
// to that project, so it is an event in the project's own ledger
// (shipped_file.written), projected into the shipped_file_writes table, and a
// clone of the project knows it too.
//
// The registry is keyed by Kind so it can hold other shipped files later; today
// it holds one, the task rules.
package shipped

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"slices"
	"text/template"
)

//go:embed templates/*.tmpl
var templates embed.FS

// Kind names one file Endless ships into projects.
type Kind string

// TasksRules is `.endless/tasks/CLAUDE.md`: the rules for a task's own
// directory and its verification suite.
//
// It ships with every registered project because the mistakes it exists to
// prevent are not Endless-specific: any project using Endless accumulates
// per-task suites that look like a regression suite and rot. It is explicitly
// NOT counted as a defense — it is prose, and prose is the layer that already
// failed. The enforcement is the runner's own-task-only refusal; this file is
// the explanation that refusal points at, placed where an agent working in that
// directory meets it before it acts.
//
// It teaches the manifest form (`verify.toml`) alone, because that is the only
// form every project can run (E-2419). The endless repo's legacy script suites
// and their `_harness.sh` are documented in that file's own header instead.
const TasksRules Kind = "tasks-rules"

// File is one shipped file: where it goes in a project, and the template that
// renders it.
type File struct {
	Kind     Kind
	Path     string // relative to the project root, slash-separated
	template string // name under templates/
}

var files = map[Kind]File{
	TasksRules: {Kind: TasksRules, Path: ".endless/tasks/CLAUDE.md", template: "tasks-rules.md.tmpl"},
}

// Lookup returns the shipped file of this kind.
func Lookup(k Kind) (File, bool) {
	f, ok := files[k]
	return f, ok
}

// Kinds lists every shipped kind, sorted.
func Kinds() []Kind {
	ks := make([]Kind, 0, len(files))
	for k := range files {
		ks = append(ks, k)
	}
	slices.Sort(ks)
	return ks
}

// Content renders the version of f this Endless ships.
func (f File) Content() ([]byte, error) {
	tmpl, err := template.ParseFS(templates, "templates/"+f.template)
	if err != nil {
		return nil, fmt.Errorf("shipped: parse %s: %w", f.template, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, nil); err != nil {
		return nil, fmt.Errorf("shipped: render %s: %w", f.template, err)
	}
	return buf.Bytes(), nil
}

// Hash is the hex SHA-256 every shipped-version comparison is made by.
func Hash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// IsShipped reports whether hash is some version of f that Endless has shipped.
func (f File) IsShipped(hash string) bool {
	return slices.Contains(versions[f.Kind], hash)
}

// Action is what placing a shipped file did, or would do, to a project's copy.
type Action string

const (
	Created Action = "created" // there was no copy; the shipped one was written
	Updated Action = "updated" // an unedited older copy was replaced
	Current Action = "current" // the copy is already what this Endless ships
	Edited  Action = "edited"  // someone edited the copy, so it was left alone
)

// Writes reports whether the action writes the shipped content.
func (a Action) Writes() bool { return a == Created || a == Updated }

// Decide says what to do with a project's copy of f.
//
// existing is the copy's content, nil when there is none. recorded is the hash
// of what init last wrote into this project, "" when it has no record — a copy
// that predates the record, or a clone whose ledger never held one. current is
// the hash of the version this Endless ships.
//
// A copy is replaced only when nobody edited it: with a record, it must be
// exactly what init last wrote; without one, exactly some version Endless has
// shipped.
func (f File) Decide(existing []byte, recorded, current string) Action {
	if existing == nil {
		return Created
	}
	have := Hash(existing)
	switch {
	case have == current:
		return Current
	case recorded != "" && have == recorded:
		return Updated
	case recorded == "" && f.IsShipped(have):
		return Updated
	}
	return Edited
}
