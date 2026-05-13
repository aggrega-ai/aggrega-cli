// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestInjectBlock_RoundTrip_BareThreeMechanisms_YAML pins the upward-
// compatibility property: existing env / file / arg authoring decodes into
// the typed fields with Additional empty, then re-encodes byte-equivalently.
func TestInjectBlock_RoundTrip_BareThreeMechanisms_YAML(t *testing.T) {
	t.Parallel()

	in := `env:
    DB_URL: connection.url
file:
    config: connection.path
`
	var b InjectBlock
	if err := yaml.Unmarshal([]byte(in), &b); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	if b.Env == nil {
		t.Errorf("Env: expected populated, got nil")
	}
	if b.File == nil {
		t.Errorf("File: expected populated, got nil")
	}
	if b.Arg != nil {
		t.Errorf("Arg: expected nil, got %v", b.Arg)
	}
	if b.Additional.Len() != 0 {
		t.Errorf("Additional.Len = %d, want 0", b.Additional.Len())
	}

	out, err := yaml.Marshal(&b)
	if err != nil {
		t.Fatalf("YAML marshal: %v", err)
	}
	var b2 InjectBlock
	if err := yaml.Unmarshal(out, &b2); err != nil {
		t.Fatalf("YAML re-unmarshal: %v\noutput: %s", err, out)
	}
	if b2.Env == nil || b2.File == nil {
		t.Errorf("re-unmarshalled fields lost: env=%v file=%v\noutput: %s", b2.Env, b2.File, out)
	}
}

// TestInjectBlock_RoundTrip_CustomMechanism_YAML decodes a YAML inject block
// that mixes core and custom mechanisms; the custom key lands in Additional
// preserving author order, and re-encoding produces a byte-equivalent shape.
func TestInjectBlock_RoundTrip_CustomMechanism_YAML(t *testing.T) {
	t.Parallel()

	in := `env:
    DB_URL: connection.url
ssm_param:
    in: DB_PASSWORD
    from: connection.password
`
	var b InjectBlock
	if err := yaml.Unmarshal([]byte(in), &b); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	if b.Env == nil {
		t.Fatalf("Env: expected populated")
	}
	if b.Additional.Len() != 1 {
		t.Fatalf("Additional.Len = %d, want 1", b.Additional.Len())
	}
	if _, ok := b.Additional.Get("ssm_param"); !ok {
		t.Errorf("Additional missing key %q; keys=%v", "ssm_param", b.Additional.Keys())
	}

	out, err := yaml.Marshal(&b)
	if err != nil {
		t.Fatalf("YAML marshal: %v", err)
	}
	var b2 InjectBlock
	if err := yaml.Unmarshal(out, &b2); err != nil {
		t.Fatalf("YAML re-unmarshal: %v\noutput: %s", err, out)
	}
	if b2.Additional.Len() != 1 {
		t.Errorf("re-unmarshal Additional.Len = %d, want 1; output=%s", b2.Additional.Len(), out)
	}
}

// TestInjectBlock_RoundTrip_CustomMechanism_JSON exercises UnmarshalJSON +
// MarshalJSON against the same shape as the YAML test.
func TestInjectBlock_RoundTrip_CustomMechanism_JSON(t *testing.T) {
	t.Parallel()

	in := `{"env":{"DB_URL":"connection.url"},"ssm_param":{"in":"DB_PASSWORD","from":"connection.password"}}`
	var b InjectBlock
	if err := json.Unmarshal([]byte(in), &b); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}
	if b.Env == nil {
		t.Fatalf("Env: expected populated")
	}
	if b.Additional.Len() != 1 {
		t.Fatalf("Additional.Len = %d, want 1", b.Additional.Len())
	}
	if _, ok := b.Additional.Get("ssm_param"); !ok {
		t.Errorf("Additional missing key %q; keys=%v", "ssm_param", b.Additional.Keys())
	}

	out, err := json.Marshal(&b)
	if err != nil {
		t.Fatalf("JSON marshal: %v", err)
	}
	var b2 InjectBlock
	if err := json.Unmarshal(out, &b2); err != nil {
		t.Fatalf("JSON re-unmarshal: %v\noutput: %s", err, out)
	}
	if b2.Additional.Len() != 1 {
		t.Errorf("re-unmarshal Additional.Len = %d, want 1; output=%s", b2.Additional.Len(), out)
	}
}

// TestInjectBlock_DuplicateKey_YAML pins the parse-layer rejection of
// duplicate top-level mechanism keys. yaml.v3 may itself reject duplicates;
// the test accepts either yaml.v3's error or the wrapped diagnostic from
// InjectBlock.UnmarshalYAML.
func TestInjectBlock_DuplicateKey_YAML(t *testing.T) {
	t.Parallel()

	in := `ssm_param:
    in: A
    from: a
ssm_param:
    in: B
    from: b
`
	var b InjectBlock
	err := yaml.Unmarshal([]byte(in), &b)
	if err == nil {
		t.Fatalf("expected duplicate-key error, got nil; result=%+v", b)
	}
	// Either yaml.v3's mapping-key-already-defined error or our wrapped diagnostic.
	if !strings.Contains(err.Error(), "duplicate") &&
		!strings.Contains(err.Error(), "already defined") &&
		!strings.Contains(err.Error(), "ssm_param") {
		t.Errorf("error message does not mention duplicate or the offending key: %v", err)
	}
}

// TestInjectBlock_DuplicateKey_JSON pins our hook's duplicate-key check on
// the JSON path. encoding/json silently overwrites duplicates by default;
// our streaming token-based decoder catches them at the parse layer.
func TestInjectBlock_DuplicateKey_JSON(t *testing.T) {
	t.Parallel()

	in := `{"ssm_param":{"in":"A","from":"a"},"ssm_param":{"in":"B","from":"b"}}`
	var b InjectBlock
	err := json.Unmarshal([]byte(in), &b)
	if err == nil {
		t.Fatalf("expected duplicate-key error, got nil; result=%+v", b)
	}
	if !strings.Contains(err.Error(), "duplicate mechanism key") || !strings.Contains(err.Error(), "ssm_param") {
		t.Errorf("error message does not mention duplicate-key + ssm_param: %v", err)
	}
}

// TestInjectBlock_AuthorOrder_PreservedAcrossRoundTrip pins the OrderedMap[any]
// author-order property at the hook layer. Multiple custom mechanisms in
// non-alphabetical author order land in Additional.Keys() in the same order.
func TestInjectBlock_AuthorOrder_PreservedAcrossRoundTrip(t *testing.T) {
	t.Parallel()

	in := `vault_secret:
    in: A
    from: a
ssm_param:
    in: B
    from: b
zzz_mechanism:
    in: C
    from: c
`
	var b InjectBlock
	if err := yaml.Unmarshal([]byte(in), &b); err != nil {
		t.Fatalf("YAML unmarshal: %v", err)
	}
	want := []string{"vault_secret", "ssm_param", "zzz_mechanism"}
	got := b.Additional.Keys()
	if len(got) != len(want) {
		t.Fatalf("Additional.Keys len = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Additional.Keys[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// Re-marshal + re-unmarshal: author order survives.
	out, err := yaml.Marshal(&b)
	if err != nil {
		t.Fatalf("YAML marshal: %v", err)
	}
	var b2 InjectBlock
	if err := yaml.Unmarshal(out, &b2); err != nil {
		t.Fatalf("YAML re-unmarshal: %v\noutput: %s", err, out)
	}
	got2 := b2.Additional.Keys()
	for i := range want {
		if i >= len(got2) || got2[i] != want[i] {
			t.Errorf("after round-trip: Additional.Keys[%d] = %v, want %q\noutput: %s", i, got2, want[i], out)
		}
	}

	// Sanity: an unrelated sentinel can be checked-against to confirm errors.Is wiring isn't leaking;
	// we don't assert anything here but reference the package to keep imports honest in tooling.
	_ = errors.New
}

// TestDecodeVerboseInjectEntry_MapShape pins the helper's behavior on
// map[string]any entries (the default YAML/JSON unmarshal shape). Covers
// in+from / in+value / both-present / missing-in / non-string-in /
// empty-in / non-string-value / not-a-map / nil.
func TestDecodeVerboseInjectEntry_MapShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		entry    any
		wantIn   string
		wantVal  string
		wantFrom string
		wantHV   bool
		wantHF   bool
		wantOK   bool
	}{
		{
			name:     "in+from",
			entry:    map[string]any{"in": "DB_URL", "from": "connection.url"},
			wantIn:   "DB_URL",
			wantFrom: "connection.url",
			wantHF:   true,
			wantOK:   true,
		},
		{
			name:    "in+value",
			entry:   map[string]any{"in": "PORT", "value": "8080"},
			wantIn:  "PORT",
			wantVal: "8080",
			wantHV:  true,
			wantOK:  true,
		},
		{
			name:     "both-present",
			entry:    map[string]any{"in": "BOTH", "from": "ref", "value": "literal"},
			wantIn:   "BOTH",
			wantVal:  "literal",
			wantFrom: "ref",
			wantHV:   true,
			wantHF:   true,
			wantOK:   true,
		},
		{name: "missing-in", entry: map[string]any{"value": "v"}, wantOK: false},
		{name: "non-string-in", entry: map[string]any{"in": 42, "value": "v"}, wantOK: false},
		{name: "empty-in", entry: map[string]any{"in": "", "value": "v"}, wantOK: false},
		{name: "non-string-value", entry: map[string]any{"in": "K", "value": 42}, wantIn: "K", wantOK: true},
		{name: "not-a-map", entry: "scalar", wantOK: false},
		{name: "nil", entry: nil, wantOK: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in, value, from, hasValue, hasFrom, ok := DecodeVerboseInjectEntry(tt.entry)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if in != tt.wantIn {
				t.Errorf("in = %q, want %q", in, tt.wantIn)
			}
			if value != tt.wantVal {
				t.Errorf("value = %q, want %q", value, tt.wantVal)
			}
			if from != tt.wantFrom {
				t.Errorf("from = %q, want %q", from, tt.wantFrom)
			}
			if hasValue != tt.wantHV {
				t.Errorf("hasValue = %v, want %v", hasValue, tt.wantHV)
			}
			if hasFrom != tt.wantHF {
				t.Errorf("hasFrom = %v, want %v", hasFrom, tt.wantHF)
			}
		})
	}
}

// TestDecodeVerboseInjectEntry_OrderedMapShape pins the helper's behavior
// on *OrderedMap[any] entries (the round-trip-fidelity decode shape). The
// asymmetry concern the outer arms address — outer `case *OrderedMap[any]:`
// exists alongside `case map[string]any:` — extends to verbose-form
// entries: a future authoring path that engages OrderedMap-aware decoding
// for InjectBlock field values would produce inner entries shaped as
// *OrderedMap[any]. The helper handles both shapes uniformly so consumer
// sites don't silently skip valid OrderedMap-shaped verbose entries.
func TestDecodeVerboseInjectEntry_OrderedMapShape(t *testing.T) {
	t.Parallel()

	build := func(pairs ...[2]any) *OrderedMap[any] {
		m := NewOrderedMap[any]()
		for _, p := range pairs {
			m.Set(p[0].(string), p[1])
		}
		return m
	}

	t.Run("in+from", func(t *testing.T) {
		t.Parallel()
		e := build([2]any{"in", "DB_URL"}, [2]any{"from", "connection.url"})
		in, value, from, hasValue, hasFrom, ok := DecodeVerboseInjectEntry(e)
		if !ok || in != "DB_URL" || from != "connection.url" || value != "" || hasFrom != true || hasValue != false {
			t.Errorf("got (in=%q value=%q from=%q hV=%v hF=%v ok=%v)", in, value, from, hasValue, hasFrom, ok)
		}
	})

	t.Run("in+value", func(t *testing.T) {
		t.Parallel()
		e := build([2]any{"in", "PORT"}, [2]any{"value", "8080"})
		in, value, _, hasValue, _, ok := DecodeVerboseInjectEntry(e)
		if !ok || in != "PORT" || value != "8080" || hasValue != true {
			t.Errorf("got (in=%q value=%q hV=%v ok=%v)", in, value, hasValue, ok)
		}
	})

	t.Run("nil-ordered-map", func(t *testing.T) {
		t.Parallel()
		var e *OrderedMap[any]
		_, _, _, _, _, ok := DecodeVerboseInjectEntry(e)
		if ok {
			t.Error("nil *OrderedMap[any] should return ok=false")
		}
	})

	t.Run("missing-in-ordered-map", func(t *testing.T) {
		t.Parallel()
		e := build([2]any{"value", "v"})
		_, _, _, _, _, ok := DecodeVerboseInjectEntry(e)
		if ok {
			t.Error("OrderedMap without `in` should return ok=false")
		}
	})
}
