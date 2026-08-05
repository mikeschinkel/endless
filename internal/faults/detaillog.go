package faults

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mikeschinkel/go-doterr"
)

// The fault detail log (errors.jsonl).
//
// Append-only, machine-local, one line per fault OCCURRENCE — not per incident.
// The `errors` table carries the bounded index; this file carries everything a
// diagnosis might want and the table has no room for: stack captures, command
// output, structured context.
//
// Directly modeled on internal/monitor/usermachinelog.go, and the same
// disclaimers apply: this is NOT the shareable ledger and NOT a write-ahead
// log. Nothing here is ever replayed into the DB or shared with other
// developers. It exists so an incident summarized in one line of `errors show`
// can still be investigated in full afterwards.
//
// Every write is best-effort: a logging failure must never surface to a caller,
// so all errors here are deliberately swallowed.

// detailLogFile is the log's basename. It lives beside user-machine.jsonl under
// the bound log directory, which is ConfigDir()-routed — so a self-dev sandbox
// gets its own file rather than polluting the real one.
const detailLogFile = "errors.jsonl"

// Detail is one line in the detail log: the full capture of a single fault
// occurrence. The top-level `kind` discriminator matches the user-machine.jsonl
// convention, so a future reader can consume either file with one decoder. A
// struct (not a map) keeps field order and shape stable.
type Detail struct {
	Kind        string         `json:"kind"` // always "fault"
	TS          string         `json:"ts"`
	FaultID     int64          `json:"fault_id"`   // errors.id this occurrence belongs to
	Occurrence  int64          `json:"occurrence"` // 1-based count within the incident
	Code        string         `json:"code"`
	Severity    string         `json:"severity"`
	Source      string         `json:"source"`
	Fingerprint string         `json:"fingerprint"`
	Summary     string         `json:"summary"`
	Detail      string         `json:"detail,omitempty"`
	Fields      map[string]any `json:"fields,omitempty"`
}

// appendDetail writes one occurrence line. Best-effort and silent: an
// unwritable log must never turn into a user-visible failure, and must never
// recurse into Record.
func appendDetail(f Fault, id, occurrence int64) {
	var dir string
	var path string
	var data []byte
	var file *os.File
	var err error

	dir = logDir()
	if dir == "" {
		goto end
	}

	err = os.MkdirAll(dir, 0o755)
	if err != nil {
		goto end
	}

	data, err = json.Marshal(Detail{
		Kind:        "fault",
		TS:          time.Now().UTC().Format("2006-01-02T15:04:05"),
		FaultID:     id,
		Occurrence:  occurrence,
		Code:        f.Code.ID,
		Severity:    string(f.Code.Severity),
		Source:      f.Source,
		Fingerprint: f.Fingerprint,
		Summary:     f.Summary,
		Detail:      f.Detail,
		Fields:      f.Fields,
	})
	if err != nil {
		goto end
	}

	path = filepath.Join(dir, detailLogFile)
	file, err = os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		goto end
	}
	// Both results are captured into err and then dropped at end: by contract
	// there is no channel left to report a failure to write the log itself.
	_, err = file.Write(append(data, '\n'))
	err = file.Close()

end:
	return
}

// DetailLogPath returns the absolute path of the detail log, or "" when the
// package is unbound. Exposed so `errors show --detail` and the verify suite
// read the file without duplicating the resolution rule.
func DetailLogPath() (path string) {
	var dir string

	dir = logDir()
	if dir == "" {
		goto end
	}
	path = filepath.Join(dir, detailLogFile)

end:
	return path
}

// Details returns every logged occurrence for one incident id, oldest first.
//
// Lines that fail to parse are skipped rather than failing the read: the log is
// appended to by multiple processes, so a torn final line is possible and must
// not hide the intact records before it. A missing file is not an error — it
// means nothing has been recorded yet.
func Details(id int64) (details []Detail, err error) {
	var path string
	var data []byte
	var line string
	var detail Detail

	path = DetailLogPath()
	if path == "" {
		err = doterr.NewErr(ErrFaults, ErrNotBound)
		goto end
	}

	data, err = os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			err = nil
			goto end
		}
		err = doterr.NewErr(ErrFaults, ErrReadingLog, err)
		goto end
	}

	for _, line = range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		detail = Detail{}
		if json.Unmarshal([]byte(line), &detail) != nil {
			continue
		}
		if detail.FaultID != id {
			continue
		}
		details = append(details, detail)
	}

end:
	return details, err
}
