// Package dbprovenance makes an endless-go answer say which database produced
// it (E-1668).
//
// The Python CLI has its own copy of this rule in src/endless/provenance.py,
// and the two are not a duplication: they announce for different callers. When
// `endless <verb>` shells out to this binary, the user reads Python's trace and
// this one would be a second line saying the same thing — which is why the Go
// trace is written by the SUBCOMMANDS a person or an agent invokes directly,
// and the shellout verbs keep their payloads clean of prose.
//
// The incident this exists for was a direct invocation:
//
//	endless-go session-query list-live --project-root <main checkout>
//
// run from inside a worktree, using main's binary. It answered from the
// worktree's sandbox — 2 rows where main held 59 — and said nothing about it.
// The session that ran it reported the shortfall as a product defect, then ran
// the same command against two different binaries, got identical output, and
// read the agreement as corroboration. That is a sound control for "did my
// change alter this?" and no control at all for "is this number right?".
//
// WHAT IT ANNOUNCES, and what it does not. The DATABASE, always, in a self-dev
// project — either one could have been right on any invocation. Never for a
// context pinned in code, and never when the process did not open a database at
// all; monitor.DBProvenance decides both, so a verb that starts reading the
// database later begins announcing it without anyone remembering to.
//
// The PROJECT half of the rule lives in the Python layer alone, deliberately.
// Downstream — the only place the project half fires — endless-go is Python's
// helper rather than anything a user types, and the project a command resolved
// is resolved up there. Announcing it here would mean threading a fact through
// every internal verb so that nobody could read it.
package dbprovenance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// Field is the key a machine payload carries the provenance under. Leading
// underscore because it is data ABOUT the result rather than a column OF it: a
// consumer destructuring known fields is unaffected, and one that iterates can
// tell the difference.
const Field = "_answered_from"

// Fields is the provenance as a payload value, or nil when there is nothing to
// announce. The directory rides along with the name because a machine render
// has room for the unambiguous form, and because "sandbox" alone does not say
// WHICH machine's sandbox once the payload travels.
func Fields() map[string]string {
	name, dir, ok := monitor.DBProvenance()
	if !ok {
		return nil
	}
	return map[string]string{"db": name, "db_dir": dir}
}

// Line is the in-band trace for a human- or agent-facing render, or "" when
// there is nothing to announce.
//
// `#`-prefixed, matching the comment idiom Endless's agent-facing output
// already uses (rowcap's footer, the `# <project>` group headers), so an agent
// meets one convention across our output rather than one per surface.
func Line() string {
	name, _, ok := monitor.DBProvenance()
	if !ok {
		return ""
	}
	return "# db: " + name
}

// Echo writes Line() to w when there is one, and nothing when there is not.
func Echo(w io.Writer) {
	if line := Line(); line != "" {
		fmt.Fprintln(w, line)
	}
}

// Encode writes v to w as JSON carrying its provenance, in the shape
// `json.NewEncoder(w).Encode(v)` produces: compact, one trailing newline. Drop-in
// for that call.
func Encode(w io.Writer, v any) error {
	return encode(w, v, "")
}

// EncodeIndent is Encode for the surfaces that emit indented JSON.
func EncodeIndent(w io.Writer, v any, indent string) error {
	return encode(w, v, indent)
}

func encode(w io.Writer, v any, indent string) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	out, err := inject(raw, Fields())
	if err != nil {
		return err
	}
	if indent != "" {
		var buf bytes.Buffer
		if err := json.Indent(&buf, out, "", indent); err != nil {
			return err
		}
		out = buf.Bytes()
	}
	_, err = w.Write(append(out, '\n'))
	return err
}

// Rows is the key an enveloped array payload's elements live under. It matches
// what `session-status --json` and `project-status --json` have always
// returned — the two richest JSON surfaces in this binary were already objects
// with a `rows` array, so enveloping the rest makes them agree rather than
// diverge.
const Rows = "rows"

// inject gives an already-marshalled payload its provenance.
//
// An OBJECT takes it as one more top-level key. An ARRAY is WRAPPED:
// `{"_answered_from": …, "rows": [...]}`.
//
// The wrap is UNCONDITIONAL — it happens whether or not there is anything to
// announce. A shape that depended on the provenance being present would hand a
// consumer an array on one invocation and an object on the next, which is a
// worse contract than either shape on its own.
//
// The first draft put a copy on every element instead, to avoid changing the
// shape. That said nothing at all for an EMPTY array — which is precisely the
// shape the incident took, a short and plausible answer from the wrong store —
// and repeated one identical object once per row besides.
//
// Done by surgery on the encoded bytes rather than by round-tripping through
// map[string]any, which would reorder every object's keys (Go sorts map keys on
// marshal) and turn a readable diff of this binary's output into noise. A
// payload that is neither object nor array — a string, a number, null — is
// returned untouched: there is nothing there for a consumer to index.
func inject(raw []byte, fields map[string]string) ([]byte, error) {
	trimmed := bytes.TrimSpace(raw)

	var entry []byte
	if len(fields) > 0 {
		encoded, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		entry = append([]byte(`"`+Field+`":`), encoded...)
	}

	switch {
	case bytes.HasPrefix(trimmed, []byte("[")):
		// The envelope, always — see the unconditional note above.
		out := make([]byte, 0, len(trimmed)+len(entry)+16)
		out = append(out, '{')
		if entry != nil {
			out = append(out, entry...)
			out = append(out, ',')
		}
		out = append(out, `"`+Rows+`":`...)
		out = append(out, trimmed...)
		out = append(out, '}')
		return out, nil
	case entry == nil:
		return raw, nil
	case bytes.HasPrefix(trimmed, []byte("{")):
		return injectObject(trimmed, entry), nil
	default:
		return raw, nil
	}
}

// injectObject inserts entry as the first member of the encoded object obj.
// First rather than last so it survives a truncated read, for the same reason
// the CLI's in-band trace is written at both ends.
func injectObject(obj, entry []byte) []byte {
	body := bytes.TrimSpace(obj[1:])
	if bytes.HasPrefix(body, []byte("}")) {
		// An empty object has no members to separate the entry from.
		return append(append([]byte("{"), entry...), '}')
	}
	out := make([]byte, 0, len(obj)+len(entry)+1)
	out = append(out, '{')
	out = append(out, entry...)
	out = append(out, ',')
	out = append(out, body...)
	return out
}
