package verify_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mikeschinkel/endless/internal/verify"
)

// ctrfSchemaFile is the CTRF schema at the release verify.SpecVersion names,
// committed so the test runs offline and pins the release it checks against.
const ctrfSchemaFile = "testdata/ctrf-" + verify.SpecVersion + ".schema.json"

// A written report validates against the CTRF schema it claims, with every
// optional field Endless produces populated: suite, stdout and stderr as line
// arrays, a check label, and a failure's message and trace.
func TestReport_WriteValidatesAgainstCTRFSchema(t *testing.T) {
	var buf bytes.Buffer
	var doc any

	r := &verify.Report{Results: verify.Results{
		Tool:    verify.Tool{Name: "endless"},
		Summary: verify.Summary{Tests: 2, Passed: 1, Failed: 1, Start: 10, Stop: 20},
		Tests: []verify.Test{
			{Name: "TestA", Status: verify.StatusPassed, Duration: 5, Suite: []string{"pkg"}},
			{Name: "the guide names sh", Status: verify.StatusFailed, Duration: 7,
				Message: "exited 1", Trace: "trace",
				Stdout: []string{"line 1", "line 2"}, Stderr: []string{"oops"}},
		},
	}}
	r.LabelTests("guide checks")

	if err := r.Write(&buf); err != nil {
		t.Fatalf("Write: %v", err)
	}

	f, err := os.Open(ctrfSchemaFile)
	if err != nil {
		t.Fatalf("open schema: %v", err)
	}
	defer f.Close()
	schemaDoc, err := jsonschema.UnmarshalJSON(f)
	if err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err = c.AddResource(ctrfSchemaFile, schemaDoc); err != nil {
		t.Fatalf("add schema: %v", err)
	}
	sch, err := c.Compile(ctrfSchemaFile)
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}

	if err = json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if err = sch.Validate(doc); err != nil {
		t.Errorf("written report does not validate against %s:\n%v\n---\n%s",
			ctrfSchemaFile, err, buf.String())
	}
}
