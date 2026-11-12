// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// forbiddenSettingTokens are the words a persistable trace-redaction setting
// would have to be named with. The guard below matches on the token rather than
// on one exact spelling, because the failure it defends against is a NEW key
// somebody adds, and nobody adding it would pick a name this test already knows.
var forbiddenSettingTokens = []string{"trace", "redact"}

// TestTraceRedactionIsNotAPersistableSetting is the structural guard behind the
// invocation-only property of showing declared-sensitive values in the trace.
//
// The property matters because of how the setting would fail: someone turns it
// on once to debug a build, commits the config, and from then on every build in
// that workspace writes secrets in the clear with no one deciding to. An
// invocation-only switch cannot outlive the invocation.
//
// The guard walks the config type the loader unmarshals into and fails if any
// authored key's name carries one of the tokens above. That type is the whole
// reach of a persisted setting: a schema property with no field behind it is
// accepted and then ignored, so a key that could actually change a build's
// behaviour must appear here. The walk descends through nested config blocks and
// through slice and map element types, so a key added under `build:` or `serve:`
// is caught exactly as a top-level one is.
//
// It is deliberately NOT satisfiable by renaming: reaching for a synonym that
// the token list misses would still leave a persisted setting, and the reviewer
// who reads this test is the backstop for that. What the test buys is that the
// obvious spellings cannot land silently.
func TestTraceRedactionIsNotAPersistableSetting(t *testing.T) {
	t.Parallel()

	var offenders []string
	seen := map[reflect.Type]bool{}

	var walk func(typ reflect.Type, path string)
	walk = func(typ reflect.Type, path string) {
		for typ.Kind() == reflect.Ptr || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array || typ.Kind() == reflect.Map {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || seen[typ] {
			return
		}
		seen[typ] = true
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name := yamlKeyName(field)
			if name == "" || name == "-" {
				continue
			}
			full := name
			if path != "" {
				full = path + "." + name
			}
			lower := strings.ToLower(name)
			for _, token := range forbiddenSettingTokens {
				if strings.Contains(lower, token) {
					// One report per key, not one per matching token: a name such
					// as `trace_unredacted` carries both and would otherwise be
					// named twice in the failure.
					offenders = append(offenders, full)
					break
				}
			}
			walk(field.Type, full)
		}
	}
	walk(reflect.TypeOf(Config{}), "")

	if len(offenders) > 0 {
		t.Errorf("workspace config must carry no persistable trace-redaction setting; found %v.\n"+
			"Showing declared-sensitive values in the trace is an invocation-only choice: a persisted\n"+
			"setting would keep writing secrets in the clear long after the debugging session that\n"+
			"turned it on.", offenders)
	}
}

// yamlKeyName reads the authored key a struct field is loaded from, dropping the
// option suffixes (`omitempty` and friends) that are not part of the name.
func yamlKeyName(field reflect.StructField) string {
	tag := field.Tag.Get("yaml")
	if tag == "" {
		return strings.ToLower(field.Name)
	}
	if i := strings.Index(tag, ","); i >= 0 {
		return tag[:i]
	}
	return tag
}

// TestPersistedTraceRedactionKeyIsRejectedAtLoad is the behavioural half: the
// structural guard above proves no such key exists, and this proves the loader
// would refuse one written by hand today. The two are different claims — the
// first is about the type, the second about the gate — and only the second shows
// that a config file carrying the key fails rather than being quietly ignored.
//
// Each spelling is paired with the scope it would plausibly be authored at, so a
// rejection at the root does not stand in for a rejection under `build:`.
func TestPersistedTraceRedactionKeyIsRejectedAtLoad(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		body string
	}{
		{"root scope", "workspace: {}\ntrace_unredacted: true\n"},
		{"build scope", "workspace: {}\nbuild:\n  trace_unredacted: true\n"},
		{"build scope, redaction spelling", "workspace: {}\nbuild:\n  redact_trace: false\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := load(writeFixture(t, tc.body))
			if err == nil {
				t.Fatalf("a persisted trace-redaction key must be rejected at load; the config was accepted:\n%s", tc.body)
			}
			if !errors.Is(err, ErrConfigInvalid()) {
				t.Errorf("rejection should wrap ErrConfigInvalid; got %v", err)
			}
		})
	}
}
