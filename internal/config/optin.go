package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/mikeschinkel/go-dt"

	"github.com/mikeschinkel/endless/internal/settings"
)

// OptInText is an opt-in as the config file spells it: "on", "off", "unset",
// or "" for a missing key, which is unset too (E-2284). It is the FILE layer;
// settings.ParseOptIn turns it into the runtime settings.OptIn that readers
// trust.
//
// It unmarshals a JSON boolean too, as the text "true" or "false", rather
// than failing inside encoding/json with a message that names neither the
// file nor what to write instead. Normalize then refuses that text with
// settings.ErrLegacyBool, naming the file, the key and the replacement.
type OptInText string

// UnmarshalJSON accepts a JSON string verbatim and a legacy JSON boolean as
// its text. See OptInText.
func (t *OptInText) UnmarshalJSON(b []byte) (err error) {
	var s string
	var v bool

	switch string(b) {
	case "true", "false":
		err = json.Unmarshal(b, &v)
		if err == nil {
			*t = OptInText(string(b))
		}
		goto end
	}
	err = json.Unmarshal(b, &s)
	if err == nil {
		*t = OptInText(s)
	}
end:
	return err
}

// optInTextType is matched by the key walker below.
var optInTextType = reflect.TypeFor[OptInText]()

// KeyValue is one leaf key of a config file and its value.
type KeyValue struct {
	Key   string
	Value reflect.Value
}

// LeafKeys returns the JSON path of every leaf key EndlessConfig declares, in
// sorted order. A struct-valued field is walked into; anything else — a
// scalar, a slice, a map, a pointer — is a leaf. A map's path names the map
// itself, since its keys are data, not schema.
//
// It is how the settings coverage test (cmd/endless-go) finds a key that is
// neither registered nor allowlisted.
func LeafKeys() (keys []string) {
	walkFields(reflect.ValueOf(EndlessConfig{}), "", func(key string, _ reflect.Value) {
		keys = append(keys, key)
	})
	sort.Strings(keys)
	return keys
}

// IsOptInKey reports whether key is an opt-in in the file schema: an
// OptInText field ("main_sync.enabled"), or a key of a map of them
// ("checks.anything"). It is how a migration recognizes a legacy boolean it
// may rewrite without rewriting a boolean that merely shares a name — the
// `enabled` of an entry in some unrelated array, say.
func IsOptInKey(key string) (ok bool) {
	parent, _, _ := cutLast(key)
	walkFields(reflect.ValueOf(EndlessConfig{}), "", func(k string, v reflect.Value) {
		switch {
		case v.Type() == optInTextType && k == key:
			ok = true
		case v.Kind() == reflect.Map && v.Type().Elem() == optInTextType && k == parent:
			ok = true
		}
	})
	return ok
}

// cutLast splits a dotted key at its last dot.
func cutLast(key string) (parent, leaf string, found bool) {
	i := strings.LastIndexByte(key, '.')
	if i < 0 {
		return "", key, false
	}
	return key[:i], key[i+1:], true
}

// OptIns returns every opt-in value c holds, keyed by its JSON path, sorted.
// A map of opt-ins (`checks`) contributes one entry per key it holds, as
// "checks.<key>". A key c does not set is absent.
func (c *EndlessConfig) OptIns() (kvs []KeyValue) {
	walkFields(reflect.ValueOf(c).Elem(), "", func(key string, v reflect.Value) {
		switch {
		case v.Type() == optInTextType:
			if v.String() != "" {
				kvs = append(kvs, KeyValue{Key: key, Value: v})
			}
		case v.Kind() == reflect.Map && v.Type().Elem() == optInTextType:
			for _, mk := range v.MapKeys() {
				kvs = append(kvs, KeyValue{Key: key + "." + mk.String(), Value: v.MapIndex(mk)})
			}
		}
	})
	sort.Slice(kvs, func(i, j int) bool { return kvs[i].Key < kvs[j].Key })
	return kvs
}

// OptInError is one opt-in value a config file spells wrong. Its message is
// the whole fix on one line: the file, the key, what is there and what to
// write. It unwraps to ErrInvalidOptIn and to the settings error that refused
// the value (settings.ErrLegacyBool or settings.ErrUnknownOptIn).
type OptInError struct {
	File  string
	Key   string
	Value string
	cause error
}

// Error renders the one-line fix.
func (e *OptInError) Error() string {
	accepted := `"on", "off" or "unset"`
	if errors.Is(e.cause, settings.ErrLegacyBool) {
		write := settings.OffText
		if e.Value == "true" {
			write = settings.OnText
		}
		return fmt.Sprintf(`%s: %s is the legacy boolean %s; write "%s" instead (accepted: %s)`,
			e.File, e.Key, e.Value, write, accepted)
	}
	return fmt.Sprintf(`%s: %s is %q; write %s`, e.File, e.Key, e.Value, accepted)
}

// Unwrap exposes the sentinels.
func (e *OptInError) Unwrap() []error {
	return []error{ErrInvalidOptIn, e.cause}
}

// OptInErrors collects every OptInError in err's tree, once each, in the
// order found. go-cfgstore and errors.Join nest them; a caller rendering a
// refusal wants the flat list.
func OptInErrors(err error) (out []*OptInError) {
	seen := map[string]bool{}
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		if oe, ok := e.(*OptInError); ok {
			if k := oe.File + "\x00" + oe.Key; !seen[k] {
				seen[k] = true
				out = append(out, oe)
			}
			return
		}
		switch u := e.(type) {
		case interface{ Unwrap() []error }:
			for _, c := range u.Unwrap() {
				walk(c)
			}
		case interface{ Unwrap() error }:
			walk(u.Unwrap())
		}
	}
	walk(err)
	return out
}

// validateOptIns refuses an opt-in value that settings.ParseOptIn does not
// accept, naming the file and the key, and — for a legacy boolean — what to
// write instead. Every bad value is reported, not just the first.
func (c *EndlessConfig) validateOptIns(file dt.Filepath) (err error) {
	var errs []error
	for _, kv := range c.OptIns() {
		_, perr := settings.ParseOptIn(kv.Value.String())
		if perr != nil {
			errs = append(errs, &OptInError{File: string(file), Key: kv.Key,
				Value: kv.Value.String(), cause: perr})
		}
	}
	return errors.Join(errs...)
}

// walkFields calls fn for each leaf field of the struct v, with its dotted
// JSON path.
func walkFields(v reflect.Value, prefix string, fn func(key string, v reflect.Value)) {
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" || !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		key := name
		if prefix != "" {
			key = prefix + "." + name
		}
		fv := v.Field(i)
		if f.Type.Kind() == reflect.Struct {
			walkFields(fv, key, fn)
			continue
		}
		fn(key, fv)
	}
}
