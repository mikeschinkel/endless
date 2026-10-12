package settingscmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/config"
	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/settings"
)

// Rewrite is one legacy boolean migrate replaced.
type Rewrite struct {
	Key  string
	From string
	To   string
}

func runMigrate(args []string) {
	var files []dt.Filepath

	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			refusal.NoReport(
				fmt.Sprintf("endless-go settings migrate: unknown flag %q", a),
				"migrate takes only file paths: correct the call and retry",
			).Command("settings migrate").Detail(usageText()).Exit(2)
		}
		files = append(files, dt.Filepath(a))
	}
	if len(files) == 0 {
		machine, err := config.MachineFile()
		if err != nil {
			refusal.Fault(err).Command("settings migrate").Exit(1)
		}
		files = append(files, machine)
		if dir := enclosingProject(); dir != "" {
			files = append(files, config.ProjectFile(dir))
		}
	}

	for _, f := range files {
		rewrites, err := MigrateFile(f)
		if errors.Is(err, fs.ErrNotExist) {
			fmt.Printf("%s: no such file, nothing to migrate\n", f)
			continue
		}
		if err != nil {
			refusal.Report(
				fmt.Sprintf("endless-go settings migrate: %s: %v", f, err),
				"how to repair a config file that is not valid JSON",
			).Command("settings migrate").Exit(1)
		}
		if len(rewrites) == 0 {
			fmt.Printf("%s: nothing to migrate\n", f)
			continue
		}
		for _, r := range rewrites {
			fmt.Printf("%s: %s %s -> %q\n", f, r.Key, r.From, r.To)
		}
	}
}

// MigrateFile rewrites every legacy JSON boolean at an opt-in key of file to
// "on" or "off", in place, changing no other byte: key order, spacing and every
// other value stay exactly as written. A boolean that is not at an opt-in key —
// an `enabled` inside an array entry, say — is left alone.
func MigrateFile(file dt.Filepath) (rewrites []Rewrite, err error) {
	var src, out []byte
	var spans []span
	var info os.FileInfo

	info, err = os.Stat(string(file))
	if err != nil {
		goto end
	}
	src, err = os.ReadFile(string(file))
	if err != nil {
		goto end
	}
	spans, err = legacySpans(src)
	if err != nil {
		goto end
	}
	if len(spans) == 0 {
		goto end
	}

	out = make([]byte, 0, len(src)+len(spans)*2)
	for i, sp := range spans {
		prev := 0
		if i > 0 {
			prev = spans[i-1].end
		}
		out = append(out, src[prev:sp.start]...)
		out = append(out, '"')
		out = append(out, sp.to...)
		out = append(out, '"')
		rewrites = append(rewrites, Rewrite{Key: sp.key, From: sp.from, To: sp.to})
	}
	out = append(out, src[spans[len(spans)-1].end:]...)
	err = writeAtomic(file, out, info.Mode().Perm())

end:
	return rewrites, err
}

// span is one boolean literal's byte range and its replacement.
type span struct {
	key        string
	start, end int
	from, to   string
}

// frame is one open JSON container while walking tokens.
type frame struct {
	object  bool
	key     string
	wantKey bool
}

// legacySpans finds every boolean literal at an opt-in key.
func legacySpans(src []byte) (spans []span, err error) {
	var stack []frame
	dec := json.NewDecoder(bytes.NewReader(src))

	for {
		var tok json.Token
		tok, err = dec.Token()
		if err == io.EOF {
			err = nil
			break
		}
		if err != nil {
			break
		}
		var top *frame
		if len(stack) > 0 {
			top = &stack[len(stack)-1]
		}
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{', '[':
				stack = append(stack, frame{object: d == '{', wantKey: d == '{'})
			case '}', ']':
				stack = stack[:len(stack)-1]
				if len(stack) > 0 && stack[len(stack)-1].object {
					stack[len(stack)-1].wantKey = true
				}
			}
			continue
		}
		if top == nil || !top.object {
			continue
		}
		if s, ok := tok.(string); ok && top.wantKey {
			top.key, top.wantKey = s, false
			continue
		}
		top.wantKey = true

		b, isBool := tok.(bool)
		if !isBool {
			continue
		}
		key, inArray := keyPath(stack)
		if inArray || !config.IsOptInKey(key) {
			continue
		}
		from := "false"
		to := settings.OffText
		if b {
			from, to = "true", settings.OnText
		}
		end := int(dec.InputOffset())
		start := end - len(from)
		if start < 0 || string(src[start:end]) != from {
			err = fmt.Errorf("cannot locate %s at %s", from, key)
			break
		}
		spans = append(spans, span{key: key, start: start, end: end, from: from, to: to})
	}
	return spans, err
}

// keyPath is the dotted path to the current value; inArray is true when any
// enclosing container is an array, which no config key lives in.
func keyPath(stack []frame) (key string, inArray bool) {
	keys := make([]string, 0, len(stack))
	for _, f := range stack {
		if !f.object {
			return "", true
		}
		keys = append(keys, f.key)
	}
	return strings.Join(keys, "."), false
}

// writeAtomic replaces file with b through a sibling temp file and a rename,
// so a reader never sees half a config.
func writeAtomic(file dt.Filepath, b []byte, perm fs.FileMode) (err error) {
	tmp := string(file) + ".migrate.tmp"
	err = os.WriteFile(tmp, b, perm)
	if err != nil {
		goto end
	}
	err = os.Rename(tmp, string(file))
	if err != nil {
		_ = os.Remove(tmp)
	}
end:
	return err
}
