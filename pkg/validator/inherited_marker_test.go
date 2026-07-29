// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

func markerNode(t *testing.T) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(InheritedSchemaMarker), &doc); err != nil {
		t.Fatalf("parse marker: %v", err)
	}
	return &doc
}

// TestValidateSchemaPillars_MarkerCarveOutIsExact pins the width of the
// mapping-node gate's one exception. The gate exists because a spec.schema that
// is not a map of pillars has nothing the pillar walk can inspect; the marker is
// admitted because it declares that the pillars live on the base entity, not
// here. Nothing else widens: not the marker on an entity that extends nothing,
// and not another scalar on an entity that does.
func TestValidateSchemaPillars_MarkerCarveOutIsExact(t *testing.T) {
	cases := []struct {
		name     string
		extends  string
		schema   *yaml.Node
		wantPass bool
	}{
		{"marker on an extending entity", "core/base", markerNode(t), true},
		{"marker with no extends", "", markerNode(t), false},
		{"another scalar on an extending entity", "core/base", &yaml.Node{Kind: yaml.ScalarNode, Value: "inherit", Line: 7}, false},
		{"a sequence on an extending entity", "core/base", &yaml.Node{Kind: yaml.SequenceNode, Line: 7}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSchemaPillars("Component", "core/subject", "fixtures/subject.yaml", tc.extends, tc.schema, nil)
			if tc.wantPass {
				if err != nil {
					t.Fatalf("want the marker admitted; got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want rejection for %s; got nil", tc.name)
			}
			// The existing mapping-node diagnostic is unchanged for every
			// rejected shape — the carve-out adds an exception, it does not
			// reword the gate.
			for _, want := range []string{"core/subject", "YAML map of pillars", "values / infra / provides", "fixtures/subject.yaml"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("diagnostic should mention %q; got: %s", want, err.Error())
				}
			}
		})
	}
}

// TestInheritedMarkerNeverReachesCompactform guards a silent failure rather than
// a behaviour, which is why it first proves the failure is real.
//
// compactform wraps a scalar root into a one-key schema named "value". So a
// marker that reached it would not halt: the entity would acquire one
// meaningless pillar named "value" and every downstream reader would see a
// Component with no values, no infra and no provides — the exact
// silent-degradation class the marker exists to remove. The first assertion
// below pins that wrap, so the rest of the test cannot go vacuous if the wrap is
// ever removed.
//
// Then the two paths that compile a Component's spec.schema are shown to refuse
// the marker before compiling: the pillar validator returns on the carve-out,
// and a resolved Component's schema is its base's mapping, so what reaches
// compactform is pillar content and never the marker.
func TestInheritedMarkerNeverReachesCompactform(t *testing.T) {
	// (1) The hazard is real: compactform would wrap the marker.
	wrapped, err := compactform.Compile(markerNode(t))
	if err != nil {
		t.Fatalf("compile marker: %v", err)
	}
	if got, ok := wrapped["value"]; !ok || got != InheritedSchemaMarker {
		t.Fatalf("this test guards compactform's scalar-root wrap; that wrap no longer produces "+
			"a one-key %q schema, so re-point the guard at whatever it produces now rather than deleting it: %#v", "value", wrapped)
	}

	// (2) The pillar validator returns before compiling anything.
	if err := ValidateSchemaPillars("Component", "core/child", "child.yaml", "core/base", markerNode(t), nil); err != nil {
		t.Fatalf("the pillar validator must admit the marker without compiling it: %v", err)
	}

	// (3) A resolved Component's schema is the base's mapping, so the schema any
	// consumer compiles carries pillars and not the wrap.
	var baseSchema yaml.Node
	if err := yaml.Unmarshal([]byte("values:\n  port: { type: integer }\n"), &baseSchema); err != nil {
		t.Fatalf("parse base schema: %v", err)
	}
	base := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/base", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Schema: baseSchema},
	}
	child := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/child", Version: "1.0.0"},
		Spec:     types.ComponentSpec{Extends: "core/base", Schema: *markerNode(t)},
	}
	resolved, errs := ResolveComponentExtends(child, func(uri string) (types.Component, bool) {
		if uri == "core/base" {
			return base, true
		}
		return types.Component{}, false
	})
	if len(errs) > 0 {
		t.Fatalf("ResolveComponentExtends: %v", errs)
	}
	compiled, err := compactform.Compile(&resolved.Spec.Schema)
	if err != nil {
		t.Fatalf("compile resolved child schema: %v", err)
	}
	if _, ok := compiled["value"]; ok {
		t.Errorf("the resolved child's schema went through compactform's scalar wrap: %#v", compiled)
	}
	props, _ := compiled["properties"].(map[string]any)
	if _, ok := props["values"]; !ok {
		t.Errorf("the resolved child's compiled schema should carry the base's values pillar; got %#v", compiled)
	}
}

// TestResolveComponentExtends_MultiLevelChainCarriesRootSchema pins that only a
// chain root authors pillar content: every descendant declares the marker and
// receives the root's block, so a three-level chain propagates one node rather
// than composing at each hop.
func TestResolveComponentExtends_MultiLevelChainCarriesRootSchema(t *testing.T) {
	t.Parallel()
	rootLabels := types.OrderedMap[string]{}
	rootLabels.Set(types.ComponentTypeLabel, "server")
	root := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/root", Version: "1.0.0", Labels: rootLabels},
		Spec: types.ComponentSpec{
			Schema: mustParseSchema(t, "values:\n  port: { type: integer, minimum: 1024 }\nprovides:\n  host: { type: string }\n"),
		},
	}
	midLabels := types.OrderedMap[string]{}
	midLabels.Set("tier", "mid")
	mid := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/mid", Version: "1.0.0", Labels: midLabels},
		Spec:     types.ComponentSpec{Extends: "core/root", Schema: mustParseSchema(t, InheritedSchemaMarker)},
	}
	leafLabels := types.OrderedMap[string]{}
	leafLabels.Set("tier", "leaf")
	leaf := types.Component{
		Metadata: types.ComponentMetadata{Name: "core/leaf", Version: "1.0.0", Labels: leafLabels},
		Spec:     types.ComponentSpec{Extends: "core/mid", Schema: mustParseSchema(t, InheritedSchemaMarker)},
	}
	lookup := func(uri string) (types.Component, bool) {
		switch uri {
		case "core/root":
			return root, true
		case "core/mid":
			return mid, true
		case "core/leaf":
			return leaf, true
		}
		return types.Component{}, false
	}

	wantBytes, err := yaml.Marshal(&root.Spec.Schema)
	if err != nil {
		t.Fatalf("marshal root schema: %v", err)
	}
	for _, subject := range []types.Component{mid, leaf} {
		got, errs := ResolveComponentExtends(subject, lookup)
		if len(errs) > 0 {
			t.Fatalf("ResolveComponentExtends(%s): %v", subject.Metadata.Name, errs)
		}
		gotBytes, err := yaml.Marshal(&got.Spec.Schema)
		if err != nil {
			t.Fatalf("marshal %s schema: %v", subject.Metadata.Name, err)
		}
		if string(gotBytes) != string(wantBytes) {
			t.Errorf("%s schema is not the chain root's, verbatim:\n got: %s\nwant: %s",
				subject.Metadata.Name, gotBytes, wantBytes)
		}
		// A verbatim copy carries no provenance tags; a one-element run through
		// the union primitive would.
		if strings.Contains(string(gotBytes), "__source_origin") {
			t.Errorf("%s schema carries provenance tags, so it was composed rather than copied:\n%s",
				subject.Metadata.Name, gotBytes)
		}
		// The structural half still merges: the leaf keeps its own label and
		// inherits the root's.
		if typeLbl, _ := got.Metadata.Labels.Get(types.ComponentTypeLabel); typeLbl != "server" {
			t.Errorf("%s lost the root's type label; got %v", subject.Metadata.Name, got.Metadata.Labels.Keys())
		}
	}
}
