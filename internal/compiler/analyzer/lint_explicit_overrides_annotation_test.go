// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package analyzer

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Explicit-overrides-annotation lint tests. Each test constructs an
// IRGraph with one or more feature attachments carrying per-leaf
// FieldProvenance and asserts the lint emits the expected warning
// strings.

func buildIRGraphWithFeature(nodeID, instanceName string, entry types.IRFeatureEntry) *types.IRGraph {
	graph := &types.IRGraph{}
	graph.Spec.Nodes.Set(nodeID, types.IRNode{})
	features := types.OrderedMap[types.IRFeatureEntry]{}
	features.Set(instanceName, entry)
	graph.Spec.FeatureValuesMapping.Set(nodeID, features)
	return graph
}

func provenance(origin, side, key, source string) types.FieldProvenance {
	return types.FieldProvenance{
		Origin:           types.AttachmentOrigin(origin),
		Side:             side,
		AnnotationKey:    key,
		AnnotationSource: source,
	}
}

func makeProvenanceMap(pairs ...any) types.OrderedMap[types.FieldProvenance] {
	m := types.OrderedMap[types.FieldProvenance]{}
	for i := 0; i < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1].(types.FieldProvenance))
	}
	return m
}

func TestLintExplicitOverridesAnnotation_NoOverride_NoWarning(t *testing.T) {
	t.Parallel()
	// IRGraph with one feature whose leaves are all explicit; no
	// OverridesAnnotation populated anywhere. The lint produces no
	// warnings.
	entry := types.IRFeatureEntry{
		ValuesProvenance: makeProvenanceMap(
			"pool", provenance("explicit", "", "", ""),
			"replicas", provenance("explicit", "", "", ""),
		),
	}
	graph := buildIRGraphWithFeature("api", "feature.myco/db-pool", entry)
	warnings := lintExplicitOverridesAnnotation(graph)
	if len(warnings) != 0 {
		t.Errorf("expected zero warnings; got %d: %v", len(warnings), warnings)
	}
}

func TestLintExplicitOverridesAnnotation_OneOverride_OneWarning(t *testing.T) {
	t.Parallel()
	// One leaf carries an OverridesAnnotation reference; the lint emits
	// one warning naming the suppressed annotation key + source.
	suppressed := provenance("annotation", "aggregate", "feature.myco/db-pool",
		"aggregate.yaml:subsystems.data.annotations.feature.myco/db-pool")
	override := types.FieldProvenance{Origin: "explicit", OverridesAnnotation: &suppressed}
	entry := types.IRFeatureEntry{
		ValuesProvenance: makeProvenanceMap("pool", override),
	}
	graph := buildIRGraphWithFeature("api", "feature.myco/db-pool", entry)
	warnings := lintExplicitOverridesAnnotation(graph)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly 1 warning; got %d: %v", len(warnings), warnings)
	}
	for _, want := range []string{
		"node \"api\"",
		"feature \"feature.myco/db-pool\"",
		"pillar \"values\"",
		"leaf \"pool\"",
		"feature.myco/db-pool",
		"aggregate.yaml:subsystems.data.annotations.feature.myco/db-pool",
	} {
		if !strings.Contains(warnings[0], want) {
			t.Errorf("warning missing %q\nfull: %s", want, warnings[0])
		}
	}
}

func TestLintExplicitOverridesAnnotation_MultipleOverrides_PerOverride(t *testing.T) {
	t.Parallel()
	// Three overrides across both pillars; the lint emits one warning
	// per override, deterministic node-and-pillar-and-leaf order.
	suppressedV := provenance("annotation", "aggregate", "feature.myco/db-pool", "aggregate.yaml:...db-pool")
	suppressedI := provenance("annotation", "release", "feature.myco/rewrite-host", "release.yaml:...rewrite-host")
	overrideV := types.FieldProvenance{Origin: "explicit", OverridesAnnotation: &suppressedV}
	overrideI := types.FieldProvenance{Origin: "explicit", OverridesAnnotation: &suppressedI}
	entry := types.IRFeatureEntry{
		ValuesProvenance: makeProvenanceMap(
			"pool", overrideV,
			"replicas", overrideV,
		),
		InfraProvenance: makeProvenanceMap("upstream", overrideI),
	}
	graph := buildIRGraphWithFeature("api", "feature.myco/db-pool", entry)
	warnings := lintExplicitOverridesAnnotation(graph)
	if len(warnings) != 3 {
		t.Fatalf("expected 3 warnings; got %d: %v", len(warnings), warnings)
	}
	// Two values warnings precede one infra warning per the lint's
	// per-pillar walk order.
	if !strings.Contains(warnings[0], "pillar \"values\"") || !strings.Contains(warnings[0], "leaf \"pool\"") {
		t.Errorf("warnings[0] should be values.pool: %s", warnings[0])
	}
	if !strings.Contains(warnings[1], "pillar \"values\"") || !strings.Contains(warnings[1], "leaf \"replicas\"") {
		t.Errorf("warnings[1] should be values.replicas: %s", warnings[1])
	}
	if !strings.Contains(warnings[2], "pillar \"infra\"") || !strings.Contains(warnings[2], "leaf \"upstream\"") {
		t.Errorf("warnings[2] should be infra.upstream: %s", warnings[2])
	}
}

func TestLintExplicitOverridesAnnotation_WarningContainsKeyAndSource(t *testing.T) {
	t.Parallel()
	// The warning carries the suppressed annotation's key + source
	// verbatim so operators navigate from the diagnostic to the
	// authoring site.
	suppressed := provenance("annotation", "release", "feature.myco/rewrite-host",
		"release.yaml:boundaries.prod.annotations.feature.myco/rewrite-host")
	override := types.FieldProvenance{Origin: "explicit", OverridesAnnotation: &suppressed}
	entry := types.IRFeatureEntry{
		InfraProvenance: makeProvenanceMap("upstream", override),
	}
	graph := buildIRGraphWithFeature("api", "feature.myco/rewrite-host", entry)
	warnings := lintExplicitOverridesAnnotation(graph)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning; got %d", len(warnings))
	}
	if !strings.Contains(warnings[0], "feature.myco/rewrite-host") {
		t.Errorf("warning should carry suppressed key; got: %s", warnings[0])
	}
	if !strings.Contains(warnings[0], "release.yaml:boundaries.prod.annotations.feature.myco/rewrite-host") {
		t.Errorf("warning should carry suppressed source; got: %s", warnings[0])
	}
}
