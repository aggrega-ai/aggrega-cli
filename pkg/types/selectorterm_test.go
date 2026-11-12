// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestSelectorTerm_YAMLBothArms pins the polymorphic unmarshal dispatch:
// a scalar item populates the string arm, a mapping item populates the
// flat-map arm with author order preserved.
func TestSelectorTerm_YAMLBothArms(t *testing.T) {
	t.Parallel()
	src := []byte(`
- "datastore.aggrega.ai/engine in (relational, vector)"
- component.aggrega.ai/type: stateful-server
  tier: critical
`)
	var items []SelectorTerm
	if err := yaml.Unmarshal(src, &items); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("got %d items, want 2", len(items))
	}
	if items[0].IsMap() || items[0].Selector != "datastore.aggrega.ai/engine in (relational, vector)" {
		t.Errorf("item 0 = %+v, want string arm with the authored selector", items[0])
	}
	if !items[1].IsMap() {
		t.Fatalf("item 1 = %+v, want flat-map arm", items[1])
	}
	if keys := items[1].Labels.Keys(); len(keys) != 2 || keys[0] != "component.aggrega.ai/type" || keys[1] != "tier" {
		t.Errorf("item 1 keys = %v, want author order [component.aggrega.ai/type tier]", keys)
	}
}

// TestSelectorTerm_YAMLRoundTripPreservesForm pins that marshalling
// re-emits the authored arm: a scalar stays a scalar, a map stays a map.
func TestSelectorTerm_YAMLRoundTripPreservesForm(t *testing.T) {
	t.Parallel()
	src := []byte(`- "tier in (critical, production)"
- zone: prod
  mesh: enabled
`)
	var items []SelectorTerm
	if err := yaml.Unmarshal(src, &items); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	out, err := yaml.Marshal(items)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var again []SelectorTerm
	if err := yaml.Unmarshal(out, &again); err != nil {
		t.Fatalf("re-Unmarshal: %v", err)
	}
	if len(again) != 2 || again[0].IsMap() || !again[1].IsMap() {
		t.Fatalf("round-trip changed arms: %s", out)
	}
	if again[0].Selector != items[0].Selector {
		t.Errorf("string arm round-trip = %q, want %q", again[0].Selector, items[0].Selector)
	}
	if k := again[1].Labels.Keys(); len(k) != 2 || k[0] != "zone" || k[1] != "mesh" {
		t.Errorf("map arm key order after round-trip = %v, want [zone mesh]", k)
	}
}

// TestSelectorTerm_JSONBothArms pins the JSON dispatch and re-emission.
func TestSelectorTerm_JSONBothArms(t *testing.T) {
	t.Parallel()
	src := []byte(`["tier in (critical, production)", {"zone": "prod", "mesh": "enabled"}]`)
	var items []SelectorTerm
	if err := json.Unmarshal(src, &items); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(items) != 2 || items[0].IsMap() || !items[1].IsMap() {
		t.Fatalf("items = %+v, want [string arm, map arm]", items)
	}
	out, err := json.Marshal(items)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	want := `["tier in (critical, production)",{"zone":"prod","mesh":"enabled"}]`
	if string(out) != want {
		t.Errorf("Marshal = %s, want %s", out, want)
	}
}

// TestSelectorTerm_RejectsOtherShapes pins the rejection of shapes
// outside the two arms (the retired nested-term form, sequences,
// non-string scalars on the JSON side).
func TestSelectorTerm_RejectsOtherShapes(t *testing.T) {
	t.Parallel()
	var item SelectorTerm
	if err := yaml.Unmarshal([]byte(`["not", "a", "term"]`), &item); err == nil {
		t.Error("YAML sequence item should be rejected")
	} else if !strings.Contains(err.Error(), "selector string or a flat key-value map") {
		t.Errorf("YAML rejection should name the two accepted forms; got %v", err)
	}
	if err := json.Unmarshal([]byte(`42`), &item); err == nil {
		t.Error("JSON number item should be rejected")
	}
	// The retired nested-term shape parses as a map of non-string values
	// and is rejected by the flat-map arm's value type.
	nested := []byte(`{"matchLabels": {"tier": "prod"}}`)
	if err := json.Unmarshal(nested, &item); err == nil {
		t.Error("nested matchLabels term should be rejected by the flat string-map arm")
	}
}
