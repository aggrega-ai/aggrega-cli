// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end proof that a boundary-scope Feature is written once per boundary,
// whatever number of components the boundary holds, and that its outputs sit
// under the name basis the boundary gives it.

package compiler_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

const armBoundaryFeatureRelease = "testdata/arm-boundary-feature/release.yaml"

// TestArmBoundaryFeatureRendersOnce builds two boundaries carrying the same
// boundary-wide attachment, one holding two components and one holding one. Each
// boundary's template holds the settings resource once, under a symbolic name
// derived from its own boundary, and its output under the name basis the boundary
// gives the attachment: the boundary-scope identity, not that of any component.
//
// The expected output names are written out in full so that the derivation's
// encoding is pinned by the bytes the author reads, not by the rule that wrote
// them.
func TestArmBoundaryFeatureRendersOnce(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, armInput(armBoundaryFeatureRelease))

	for _, tc := range []struct {
		boundary   string
		symbol     string
		outputName string
		components []string
	}{
		{"stack", "StackSettings", "bfr_zh_zhstack_zh_zhsettings__tier", []string{"alpha__host", "beta__host"}},
		{"edge", "EdgeSettings", "bfr_zh_zhedge_zh_zhsettings__tier", []string{"gamma__host"}},
	} {
		t.Run(tc.boundary, func(t *testing.T) {
			template, raw := findArmTemplate(t, result.Artifacts, "dist/"+tc.boundary+"/")
			t.Logf("assembled template:\n%s", raw)

			if n := strings.Count(string(raw), `"type": "Microsoft.AppConfiguration/configurationStores"`); n != 1 {
				t.Errorf("the boundary's template holds the settings resource %d times, want once", n)
			}
			armResourceBody(t, template, tc.symbol)

			want := append([]string{tc.outputName}, tc.components...)
			sortedWant := append([]string(nil), want...)
			sort.Strings(sortedWant)
			if got := sortedAnyKeys(armSection(t, template, "outputs")); !equalStringSlice(got, sortedWant) {
				t.Errorf("the template declares outputs %v, want exactly %v", got, sortedWant)
			}
		})
	}
}

// TestArmBoundaryFeatureBuildIsDeterministic asserts the two-boundary build is
// byte-identical across repeated runs.
func TestArmBoundaryFeatureBuildIsDeterministic(t *testing.T) {
	pipelinetest.AssertDeterministic(t, armInput(armBoundaryFeatureRelease))
}

const armBoundaryFeatureScopeRelease = "testdata/arm-boundary-feature-scope/release.yaml"

// TestArmBoundaryFeatureTakesTheBoundarysScope builds a boundary deployed at
// subscription scope that carries a boundary-wide attachment. The attachment is
// part of the boundary, so its resource-group content lands in the nested level
// that holds the components' own, at the scope and in the resource group the
// boundary's annotations name.
func TestArmBoundaryFeatureTakesTheBoundarysScope(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, armInput(armBoundaryFeatureScopeRelease))
	template, raw := findArmTemplate(t, result.Artifacts, "dist/stack/")
	t.Logf("assembled template:\n%s", raw)

	if schema, _ := template["$schema"].(string); !strings.Contains(schema, "subscriptionDeploymentTemplate") {
		t.Errorf("$schema = %q, want the subscription template schema", schema)
	}
	level := armResourceBody(t, template, "_resourceGroup")
	if level["resourceGroup"] != "orders-rg" {
		t.Errorf("_resourceGroup deploys into %v, want orders-rg", level["resourceGroup"])
	}
	properties, _ := level["properties"].(map[string]any)
	inner, _ := properties["template"].(map[string]any)
	resources, _ := inner["resources"].(map[string]any)
	if got, want := sortedAnyKeys(resources), []string{"Alpha", "Beta", "StackSettings"}; !equalStringSlice(got, want) {
		t.Errorf("the resource-group level holds %v, want %v", got, want)
	}
}

