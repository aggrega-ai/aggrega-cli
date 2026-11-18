// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package exportgen

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const mebibyte = 1 << 20

func TestGenerateIsDeterministicAndLarge(t *testing.T) {
	spec := Spec{Providers: []string{"registry.terraform.io/example/big"}, TypesPerProvider: 1800,
		AttributesPerType: 40, BlockDepth: 2}
	first, second := Generate(spec), Generate(spec)
	if !bytes.Equal(first, second) {
		t.Fatal("two runs of one Spec differ")
	}
	if len(first) < 17*mebibyte {
		t.Errorf("the export is %d bytes, want at least 17 MiB", len(first))
	}
	var tree struct {
		FormatVersion   string `json:"format_version"`
		ProviderSchemas map[string]struct {
			ResourceSchemas map[string]struct {
				Block struct {
					Attributes map[string]json.RawMessage `json:"attributes"`
				} `json:"block"`
			} `json:"resource_schemas"`
		} `json:"provider_schemas"`
	}
	if err := json.Unmarshal(first, &tree); err != nil {
		t.Fatal(err)
	}
	p := tree.ProviderSchemas["registry.terraform.io/example/big"]
	if tree.FormatVersion == "" || len(p.ResourceSchemas) != 1800 {
		t.Fatalf("the export holds %d types", len(p.ResourceSchemas))
	}
	for name, r := range p.ResourceSchemas {
		if len(r.Block.Attributes) != 40 {
			t.Fatalf("%s holds %d attributes", name, len(r.Block.Attributes))
		}
		break
	}
}

func TestGenerateShapes(t *testing.T) {
	spec := Spec{Providers: []string{"registry.terraform.io/hashicorp/google", "registry.terraform.io/hashicorp/google-beta"},
		TypesPerProvider: 3, AttributesPerType: 5, BlockDepth: 3}
	out := Generate(spec)
	if !json.Valid(out) {
		t.Fatal("the export is not JSON")
	}
	text := string(out)
	// Both providers of a family carry the same type names, as a provider and
	// its beta sibling do.
	if n := strings.Count(text, `"google_resource_0000":`); n != 2 {
		t.Errorf("the first type is written %d times, want once per provider", n)
	}
	if bytes.Equal(out, Generate(Spec{Providers: spec.Providers, TypesPerProvider: 3, AttributesPerType: 6, BlockDepth: 3})) {
		t.Errorf("a different shape gives the same export")
	}
	if !strings.Contains(text, `"nested_3"`) || strings.Contains(text, `"nested_4"`) {
		t.Errorf("the nesting is not 3 deep")
	}
	if got := Generate(Spec{}); !json.Valid(got) || !strings.Contains(string(got), `"provider_schemas":{}`) {
		t.Errorf("an empty Spec gives %s", got)
	}
}
