// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end coverage of what a schema-declared default renders to, per
// authoring surface, in one build: a Component's own infra pillar, a
// component-scope attachment's Feature values pillar, and a boundary-scope
// attachment's Feature infra pillar. All three meet in one assembled template, so
// a surface that materialises a default and a surface that does not are read
// side by side rather than inferred from separate builds.
//
// The Component-declared default is the control: its surface materialises
// already, so an empty rendering there means the fixture stopped exercising what
// it claims, which is what lets the other two be read as statements about the
// attachment surface.

package compiler_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

// TestArmAttachmentSchemaDefaultsReachTheFragment reads the three declared
// defaults out of one assembled template. Each row names the surface that
// declared the default and the rendered value it must reach; each value is a
// property of a different resource, so the three cannot mask one another. The
// rows are asserted against the rendered output rather than the graph: a default
// present in the graph and absent from the artifact is the shape this covers.
func TestArmAttachmentSchemaDefaultsReachTheFragment(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, armInput(armAttachmentsDir+"release-defaults.yaml"))
	template, raw := findArmTemplate(t, result.Artifacts, "stack")
	t.Logf("assembled template:\n%s", raw)

	for _, tc := range []struct {
		surface string
		symbol  string
		path    []string
		want    any
	}{
		{
			surface: "the Component's own infra pillar",
			symbol:  "Store",
			path:    []string{"sku", "name"},
			want:    "Standard_B1ms",
		},
		{
			surface: "a component-scope attachment's Feature values pillar",
			symbol:  "StoreDiag",
			path:    []string{"properties", "retentionDays"},
			// JSON numbers decode as float64.
			want: float64(30),
		},
		{
			surface: "a boundary-scope attachment's Feature infra pillar",
			symbol:  "StackSettings",
			path:    []string{"properties", "tier"},
			want:    "standard",
		},
	} {
		t.Run(tc.symbol, func(t *testing.T) {
			var node any = armResourceBody(t, template, tc.symbol)
			for _, key := range tc.path {
				obj, ok := node.(map[string]any)
				if !ok {
					t.Fatalf("%s: cannot read %v, %#v is not an object", tc.symbol, tc.path, node)
				}
				node, ok = obj[key]
				if !ok {
					t.Fatalf("%s carries nothing at %v: the default declared on %s did not arrive", tc.symbol, tc.path, tc.surface)
				}
			}
			if node != tc.want {
				t.Errorf("%s at %v = %#v, want %#v: the default is declared on %s and nothing authored a value", tc.symbol, tc.path, node, tc.want, tc.surface)
			}
		})
	}
}

// TestArmAttachmentSchemaDefaultsAreDeterministic asserts the build in which
// every value in play arrives from a schema default is byte-identical across
// repeated runs. It is distinct from the authored build's determinism: a fill
// that appended through an unordered map would render identically to itself on
// one run and differently on the next.
func TestArmAttachmentSchemaDefaultsAreDeterministic(t *testing.T) {
	pipelinetest.AssertDeterministic(t, armInput(armAttachmentsDir+"release-defaults.yaml"))
}
