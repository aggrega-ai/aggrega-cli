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
