// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"strings"
	"testing"
)

func deployed(symbols ...string) map[string]any {
	m := map[string]any{}
	for _, s := range symbols {
		m[s] = map[string]any{"type": "T"}
	}
	return m
}

// provenanceOf returns the provenance block of a resource, or nil.
func provenanceOf(f map[string]any, symbol string) map[string]any {
	md, _ := f["resources"].(map[string]any)[symbol].(map[string]any)["metadata"].(map[string]any)
	block, _ := md[ProvenanceMetadataKey].(map[string]any)
	return block
}

func TestStampProvenance(t *testing.T) {
	t.Parallel()
	set := map[string]string{"aggrega.ai/component-id": "payments-db", "aggrega.ai/release": "orders"}

	t.Run("stamps each deployed resource", func(t *testing.T) {
		t.Parallel()
		f := map[string]any{"resources": deployed("Db", "Api")}
		if adv := StampProvenance(f, set); adv != "" {
			t.Fatalf("advisory = %q", adv)
		}
		for _, s := range []string{"Db", "Api"} {
			block := provenanceOf(f, s)
			if block["aggrega.ai/component-id"] != "payments-db" || len(block) != 2 {
				t.Errorf("%s block = %v", s, block)
			}
		}
		if _, ok := f["metadata"]; ok {
			t.Errorf("template metadata written: %v", f["metadata"])
		}
	})
	t.Run("merges into authored resource metadata", func(t *testing.T) {
		t.Parallel()
		f := map[string]any{"resources": map[string]any{"Db": map[string]any{"type": "T", "metadata": map[string]any{"author": "x"}}}}
		if adv := StampProvenance(f, set); adv != "" {
			t.Fatalf("advisory = %q", adv)
		}
		md := f["resources"].(map[string]any)["Db"].(map[string]any)["metadata"].(map[string]any)
		if md["author"] != "x" || md[ProvenanceMetadataKey] == nil {
			t.Errorf("metadata = %v", md)
		}
	})
	t.Run("a metadata that is not an object is left and named", func(t *testing.T) {
		t.Parallel()
		f := map[string]any{"resources": map[string]any{
			"Bad":  map[string]any{"type": "T", "metadata": "text"},
			"Good": map[string]any{"type": "T"},
		}}
		adv := StampProvenance(f, set)
		if f["resources"].(map[string]any)["Bad"].(map[string]any)["metadata"] != "text" {
			t.Error("metadata rewritten")
		}
		if provenanceOf(f, "Good") == nil {
			t.Error("the other resource is not stamped")
		}
		for _, want := range []string{"Bad", "/resources/Bad/metadata", "not an object"} {
			if !strings.Contains(adv, want) {
				t.Errorf("advisory %q does not contain %q", adv, want)
			}
		}
	})
	t.Run("an existing resource is skipped", func(t *testing.T) {
		t.Parallel()
		f := map[string]any{"resources": map[string]any{"Old": map[string]any{"existing": true}, "Db": map[string]any{"type": "T"}}}
		if adv := StampProvenance(f, set); adv != "" {
			t.Errorf("advisory = %q", adv)
		}
		if provenanceOf(f, "Old") != nil || provenanceOf(f, "Db") == nil {
			t.Errorf("resources = %v", f["resources"])
		}
	})
	t.Run("only existing resources gives an advisory", func(t *testing.T) {
		t.Parallel()
		f := map[string]any{"resources": map[string]any{"Old": map[string]any{"existing": true}}}
		if adv := StampProvenance(f, set); !strings.Contains(adv, "no resource") {
			t.Errorf("advisory = %q", adv)
		}
		if provenanceOf(f, "Old") != nil {
			t.Error("stamped an existing resource")
		}
	})
	t.Run("a fragment with no resource gets an advisory", func(t *testing.T) {
		t.Parallel()
		f := map[string]any{"parameters": map[string]any{"p": map[string]any{}}}
		if adv := StampProvenance(f, set); !strings.Contains(adv, "no resource") {
			t.Errorf("advisory = %q", adv)
		}
	})
	t.Run("an empty set stamps nothing and says nothing", func(t *testing.T) {
		t.Parallel()
		f := map[string]any{"resources": deployed("Db")}
		if adv := StampProvenance(f, nil); adv != "" {
			t.Errorf("advisory = %q", adv)
		}
		if provenanceOf(f, "Db") != nil {
			t.Error("stamped an empty set")
		}
	})
}

func TestStampedFragmentsAssembleWithoutCollision(t *testing.T) {
	t.Parallel()
	stamp := func(node, symbol, id string) Contribution {
		frag, _, err := ReadFragment([]byte(storage(symbol)))
		if err != nil {
			t.Fatal(err)
		}
		f := map[string]any{"resources": frag.Sections["resources"]}
		if adv := StampProvenance(f, map[string]string{"aggrega.ai/component-id": id}); adv != "" {
			t.Fatal(adv)
		}
		content, err := encodeTemplate(f)
		if err != nil {
			t.Fatal(err)
		}
		return Contribution{NodeID: node, Files: [][]byte{content}}
	}
	m, _ := assembled(t, stamp("payments-db", "Db", "payments-db"), stamp("orders-api", "Api", "orders-api"))
	for symbol, id := range map[string]string{"Db": "payments-db", "Api": "orders-api"} {
		got := provenanceOf(m, symbol)
		if got["aggrega.ai/component-id"] != id {
			t.Errorf("%s provenance = %v", symbol, got)
		}
	}
	if _, ok := m["metadata"]; ok {
		t.Errorf("template metadata written: %v", m["metadata"])
	}
}

func TestWriteFragmentReadsBack(t *testing.T) {
	t.Parallel()
	src := []byte(`{
  "$schema": "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
  "parameters": {"location": {"type": "string"}},
  "functions": [
    {"namespace": "b", "members": {"two": {"output": {"type": "int", "value": 2}}}},
    {"namespace": "a", "members": {"one": {"output": {"type": "int", "value": 1}}}}
  ],
  "resources": {"Db": {"type": "T"}},
  "outputs": {"host": {"type": "string", "value": "x"}}
}`)
	first, _, err := ReadFragment(src)
	if err != nil {
		t.Fatal(err)
	}
	written, err := WriteFragment(first)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := ReadFragment(written)
	if err != nil {
		t.Fatalf("written fragment does not read back: %v\n%s", err, written)
	}
	if second.Scope != ScopeSubscription {
		t.Errorf("scope = %q", second.Scope)
	}
	for _, section := range []string{"parameters", "functions", "resources", "outputs"} {
		if len(second.Sections[section]) != len(first.Sections[section]) {
			t.Errorf("section %s: %v != %v", section, second.Sections[section], first.Sections[section])
		}
	}
	if got := second.Functions["a.one"]; got.Namespace != "a" || got.Member != "one" {
		t.Errorf("function source = %v", got)
	}
	again, err := WriteFragment(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(written) {
		t.Errorf("a second write differs:\n%s\n%s", written, again)
	}
}
