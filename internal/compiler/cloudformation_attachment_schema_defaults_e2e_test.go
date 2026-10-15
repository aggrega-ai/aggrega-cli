// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end coverage of what a schema-declared default renders to, per
// authoring surface, in ONE build against the sibling catalog.
//
// The subject is not any single surface. It is whether the same build answers
// the same question the same way whichever surface declared the default: a
// Component's own pillar, an attachment's Feature at component scope, and an
// attachment's Feature at boundary scope. All three meet in one assembled stack
// here, so a surface that materialises and a surface that does not are readable
// side by side rather than inferred from two separate builds.
//
// The Component-declared default is the control. Its surface materialises
// already, so an empty rendering there means the fixture stopped exercising what
// it claims to — which is what lets the other two assertions be read as
// statements about the attachment surface rather than about the fixture.
//
// These tests read catalog entities from the aggrega-hub repository checked out
// beside this one (the catalog paths leave this repository for aggrega-hub).
// They skip when the sibling catalog is absent.

package compiler_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

func cfnAttachmentDefaultsInput(t *testing.T) pipelinetest.Input {
	t.Helper()
	skipIfNoSiblingCatalog(t)
	return pipelinetest.Input{
		ReleasePath:  "testdata/cloudformation-published-feature-recipes/release-attachment-defaults.yaml",
		CatalogPaths: cfnFeatureRecipeCatalogPaths(),
	}
}

// TestCloudFormationSchemaDefaults_EveryAuthoringSurfaceMaterialises reads the
// three declared defaults out of one assembled stack.
//
// Each row names the surface that declared the default and the rendered value it
// must reach, and each rendered value is a property of a DIFFERENT resource, so
// the three cannot mask one another. The rows are asserted against rendered
// output rather than against the graph on purpose: a default present in the
// graph and absent from the artifact is the exact shape this covers, and only
// the artifact can tell the two apart.
//
// The empty-fold row is the one worth reading twice. Its realization folds the
// value into a larger string, so a default that never arrives renders as a
// well-formed artifact carrying `tier=` with nothing after it — valid
// CloudFormation, silently wrong, and invisible to any check that asks whether
// the property is present.
func TestCloudFormationSchemaDefaults_EveryAuthoringSurfaceMaterialises(t *testing.T) {
	in := cfnAttachmentDefaultsInput(t)

	result := pipelinetest.Build(t, in)

	stack, raw := findCfnStack(t, result.Artifacts, "dist/edge")
	t.Logf("assembled edge-boundary stack:\n%s", raw)

	resources := mustCfnMap(t, stack, "Resources")

	for _, tc := range []struct {
		surface   string
		logicalID string
		property  string
		want      any
	}{
		{
			// The control: the consumer Component declares infra.tier and the
			// Release authors no infra for that component at all.
			surface:   "the Component's own infra pillar",
			logicalID: "ReportingApp",
			property:  "Tier",
			want:      "Standard",
		},
		{
			// A boundary-wide attachment whose Feature declares an infra
			// default, attached with no infra block. The realization folds the
			// value, so a missing default renders "tier=" and nothing more.
			surface:   "a boundary-scope attachment's Feature infra pillar",
			logicalID: "EdgeSharedConfig",
			property:  "Value",
			want:      "tier=standard",
		},
		{
			// A component-carried attachment whose Feature declares a values
			// default, attached with no values block. JSON numbers decode as
			// float64, which is why the expectation is written as one.
			surface:   "a component-scope attachment's Feature values pillar",
			logicalID: "PaymentsDbStorageHeadroom",
			property:  "Threshold",
			want:      float64(10737418240),
		},
	} {
		t.Run(tc.logicalID+"."+tc.property, func(t *testing.T) {
			props := mustCfnMap(t, mustCfnMap(t, resources, tc.logicalID), "Properties")
			got, present := props[tc.property]
			if !present {
				t.Fatalf("%s.Properties carries no %s at all; every property: %v",
					tc.logicalID, tc.property, sortedCfnKeys(props))
			}
			if got != tc.want {
				t.Errorf("%s.Properties.%s = %#v, want %#v — the default is declared on %s and nothing authored a value, so this is what the declaration renders to",
					tc.logicalID, tc.property, got, tc.want, tc.surface)
			}
		})
	}
}

// TestCloudFormationSchemaDefaults_Deterministic asserts the build in which
// every value in play arrives from a schema default is byte-identical across
// repeated runs.
//
// It is a distinct assertion from the determinism already covered over the
// authored build, because the two exercise different write paths into the
// rendered pillar: an authored pillar carries the author's own key order, while
// a default-filled one carries keys the author never wrote. A fill that appended
// through an unordered map would render identically to itself on a single run and
// differently on the next, which no single-build assertion can see.
func TestCloudFormationSchemaDefaults_Deterministic(t *testing.T) {
	in := cfnAttachmentDefaultsInput(t)
	pipelinetest.AssertDeterministic(t, in)
}
