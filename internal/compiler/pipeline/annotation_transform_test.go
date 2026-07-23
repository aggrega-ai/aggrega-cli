// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// releaseWithComponent builds a minimal resolved Release carrying one boundary
// and one component, so the confinement tests have a real target coordinate to
// accept and a real tree to mutate.
func releaseWithComponent(boundary, component string) *types.Release {
	comp := types.BoundaryComponent{}
	b := types.Boundary{}
	b.Components.Set(component, comp)
	rel := &types.Release{}
	rel.Spec.Boundaries.Set(boundary, b)
	return rel
}

// TestApplyAnnotationInjections_RejectsUnknownCoordinate proves the
// annotation-transform role's structural confinement: an injection naming a
// coordinate the host did not supply as a target halts loudly with
// ErrAnnotationInjectionRejected rather than being applied. This is the
// host-enforced facet guarantee — the host adds annotations only onto the
// coordinates it offered, regardless of guest output.
func TestApplyAnnotationInjections_RejectsUnknownCoordinate(t *testing.T) {
	rr := &resolver.ResolveResult{Release: releaseWithComponent("edge", "api")}
	_, targetSet := annotationTargets(rr.Release)

	out := AnnotationTransformOutput{Injections: []AnnotationInjection{
		{BoundaryPath: "edge", ComponentName: "does-not-exist", AnnotationKey: "k", AnnotationValue: "v"},
	}}
	err := applyAnnotationInjections(rr, out, targetSet, "proof://x")
	if err == nil {
		t.Fatalf("injection onto an unsupplied coordinate: expected a halt, got nil")
	}
	if !errors.Is(err, ErrAnnotationInjectionRejected) {
		t.Fatalf("halt not errors.Is ErrAnnotationInjectionRejected: %v", err)
	}
}

// TestApplyAnnotationInjections_AcceptsSuppliedCoordinate proves an injection
// onto a supplied target coordinate is applied: the annotation lands on the
// component, the host stamps the injected-by companion, and the provenance
// side-table records the injecting plugin.
func TestApplyAnnotationInjections_AcceptsSuppliedCoordinate(t *testing.T) {
	rr := &resolver.ResolveResult{Release: releaseWithComponent("edge", "api")}
	_, targetSet := annotationTargets(rr.Release)

	out := AnnotationTransformOutput{Injections: []AnnotationInjection{
		{BoundaryPath: "edge", ComponentName: "api", AnnotationKey: "feature.x/k", AnnotationValue: "v"},
	}}
	if err := applyAnnotationInjections(rr, out, targetSet, "proof://x"); err != nil {
		t.Fatalf("injection onto a supplied coordinate: unexpected halt: %v", err)
	}

	b, _ := rr.Release.Spec.Boundaries.Get("edge")
	comp, _ := b.Components.Get("api")
	if v, ok := comp.Annotations.Get("feature.x/k"); !ok || !v.IsScalar() || *v.Scalar != "v" {
		t.Errorf("injected annotation not applied to the component: %+v (present=%v)", v, ok)
	}
	if v, ok := comp.Annotations.Get(injectedByAnnotationKey); !ok || !v.IsScalar() || *v.Scalar != "proof://x" {
		t.Errorf("host did not stamp the injected-by companion annotation: %+v (present=%v)", v, ok)
	}
	pk := resolver.AnnotationProvenanceKey("edge", "api", "feature.x/k")
	if rr.AnnotationProvenance[pk].InjectedByPlugin != "proof://x" {
		t.Errorf("provenance side-table did not record the injecting plugin: %+v", rr.AnnotationProvenance[pk])
	}
}
