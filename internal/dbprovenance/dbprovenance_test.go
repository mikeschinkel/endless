package dbprovenance

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// fields stands in for a live monitor.DBProvenance answer. The rule about WHEN
// there is something to announce is monitor's and is tested there; what these
// pin is the shape it takes once there is.
var fields = map[string]string{"db": "sandbox (e-1668)", "db_dir": "/c/sandbox"}

func TestInject_Object(t *testing.T) {
	got, err := inject([]byte(`{"id":"E-1668","status":"underway"}`), fields)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("payload is not valid JSON: %v (%s)", err, got)
	}
	if out["id"] != "E-1668" || out["status"] != "underway" {
		t.Errorf("original fields lost: %s", got)
	}
	prov, ok := out[Field].(map[string]any)
	if !ok || prov["db"] != "sandbox (e-1668)" {
		t.Errorf("provenance missing or wrong: %s", got)
	}
}

// The key goes FIRST, so a truncated read still carries it — the same reason
// the CLI writes its in-band trace at both ends.
func TestInject_ObjectPutsItFirst(t *testing.T) {
	got, err := inject([]byte(`{"id":"E-1668"}`), fields)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), `{"`+Field+`":`) {
		t.Errorf("want the provenance as the first member, got %s", got)
	}
}

// Key ORDER of the payload's own fields must survive. Round-tripping through
// map[string]any would sort them, turning every diff of this binary's output
// into noise.
func TestInject_PreservesKeyOrder(t *testing.T) {
	got, err := inject([]byte(`{"zebra":1,"apple":2,"middle":3}`), fields)
	if err != nil {
		t.Fatal(err)
	}
	body := string(got)
	zi, ai, mi := strings.Index(body, `"zebra"`), strings.Index(body, `"apple"`), strings.Index(body, `"middle"`)
	if !(zi < ai && ai < mi) {
		t.Errorf("key order changed: %s", body)
	}
}

func TestInject_ArrayIsPerRow(t *testing.T) {
	got, err := inject([]byte(`[{"id":"E-1"},{"id":"E-2"}]`), fields)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("payload is not valid JSON: %v (%s)", err, got)
	}
	if len(out) != 2 {
		t.Fatalf("row count changed: %s", got)
	}
	for i, row := range out {
		prov, ok := row[Field].(map[string]any)
		if !ok || prov["db"] != "sandbox (e-1668)" {
			t.Errorf("row %d lost its provenance: %s", i, got)
		}
	}
}

func TestInject_EmptyObject(t *testing.T) {
	got, err := inject([]byte(`{}`), fields)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("an empty object must stay valid JSON: %v (%s)", err, got)
	}
	if _, ok := out[Field]; !ok {
		t.Errorf("provenance missing: %s", got)
	}
}

// A payload with nowhere to put a key is returned untouched rather than
// reshaped: reshaping would break the consumer this exists to inform.
func TestInject_NothingToAttachTo(t *testing.T) {
	for _, raw := range []string{`[1,2,3]`, `"plain"`, `null`, `42`} {
		got, err := inject([]byte(raw), fields)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if string(got) != raw {
			t.Errorf("inject(%s) = %s, want it unchanged", raw, got)
		}
	}
}

// Nothing to announce means nothing added — not an empty key.
func TestInject_NoFieldsIsAPassthrough(t *testing.T) {
	raw := `{"id":"E-1668"}`
	got, err := inject([]byte(raw), nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != raw {
		t.Errorf("inject with no fields = %s, want %s", got, raw)
	}
}

// Encode must remain a drop-in for json.NewEncoder(w).Encode: compact, exactly
// one trailing newline. A second newline would break a caller reading one line.
func TestEncode_MatchesTheEncoderItReplaced(t *testing.T) {
	var got bytes.Buffer
	if err := Encode(&got, map[string]string{"a": "b"}); err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if err := json.NewEncoder(&want).Encode(map[string]string{"a": "b"}); err != nil {
		t.Fatal(err)
	}
	// With nothing to announce (no database opened in a test binary), the two
	// must agree byte for byte.
	if got.String() != want.String() {
		t.Errorf("Encode = %q, want %q", got.String(), want.String())
	}
}

func TestEncodeIndent_IsValidAndIndented(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeIndent(&buf, []map[string]string{{"a": "b"}}, "  "); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("not valid JSON: %v (%s)", err, buf.String())
	}
	if !strings.Contains(buf.String(), "\n  ") {
		t.Errorf("want indented output, got %q", buf.String())
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Errorf("want exactly one trailing newline, got %q", buf.String())
	}
}
