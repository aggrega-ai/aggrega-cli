// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

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
		{BoundaryPath: "edge", ComponentName: "does-not-exist", AnnotationKey: "k", AnnotationValue: json.RawMessage(`"v"`)},
	}}
	err := applyAnnotationInjections(rr, out, targetSet, "proof://x")
	if err == nil {
		t.Fatalf("injection onto an unsupplied coordinate: expected a halt, got nil")
	}
	if !errors.Is(err, ErrAnnotationInjectionRejected()) {
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
		{BoundaryPath: "edge", ComponentName: "api", AnnotationKey: "feature.x/k", AnnotationValue: json.RawMessage(`"v"`)},
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

// componentAnnotation reads one annotation back off the component the
// single-component Release carries.
func componentAnnotation(t *testing.T, rr *resolver.ResolveResult, key string) (*types.AnnotationValue, bool) {
	t.Helper()
	b, _ := rr.Release.Spec.Boundaries.Get("edge")
	comp, _ := b.Components.Get("api")
	return comp.Annotations.Get(key)
}

// TestApplyAnnotationInjections_StructuredValueIsReadAsAuthored proves an
// injected object lands as the structured value the same text authored in the
// Release decodes to — subkeys in the order the plugin wrote them, which is not
// sorted order, and each subvalue the Go value the authored decode yields — so
// expansion meets an injected value exactly as it meets an authored one.
func TestApplyAnnotationInjections_StructuredValueIsReadAsAuthored(t *testing.T) {
	rr := &resolver.ResolveResult{Release: releaseWithComponent("edge", "api")}
	_, targetSet := annotationTargets(rr.Release)

	out := AnnotationTransformOutput{Injections: []AnnotationInjection{{
		BoundaryPath: "edge", ComponentName: "api", AnnotationKey: "feature.x/monitoring",
		AnnotationValue: json.RawMessage(`{"path":"/metrics","interval":"30s","port":"metrics","replicas":3,"tls":{"z":true,"a":false}}`),
	}}}
	if err := applyAnnotationInjections(rr, out, targetSet, "proof://x"); err != nil {
		t.Fatalf("a structured injection onto a supplied coordinate: unexpected halt: %v", err)
	}

	got, ok := componentAnnotation(t, rr, "feature.x/monitoring")
	if !ok || !got.IsStructured() {
		t.Fatalf("the injected object did not land as a structured value: %+v (present=%v)", got, ok)
	}
	if keys := got.Structured.Keys(); strings.Join(keys, ",") != "path,interval,port,replicas,tls" {
		t.Errorf("subkey order = %v, want the order the plugin wrote", keys)
	}

	var authored types.AnnotationValue
	if err := yaml.Unmarshal([]byte("path: /metrics\ninterval: 30s\nport: metrics\nreplicas: 3\ntls:\n  z: true\n  a: false\n"), &authored); err != nil {
		t.Fatalf("decode the authored twin: %v", err)
	}
	gotJSON, _ := json.Marshal(got.Structured)
	wantJSON, _ := json.Marshal(authored.Structured)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("injected value reads differently from the same value authored:\n injected: %s\n authored: %s", gotJSON, wantJSON)
	}
	replicas, _ := got.Structured.Get("replicas")
	authoredReplicas, _ := authored.Structured.Get("replicas")
	if replicas != authoredReplicas {
		t.Errorf("subvalue Go value = %#v (%T), want the authored decode's %#v (%T)", replicas, replicas, authoredReplicas, authoredReplicas)
	}
}

// TestApplyAnnotationInjections_RefusesAValueNoAnnotationTakes drives every value
// shape an injection may not carry, one per row, and proves each halts with
// ErrAnnotationInjectionRejected naming the coordinate, the annotation and the
// shape — and that the refusal leaves the Release untouched even where an
// acceptable injection precedes the refused one in the same output.
func TestApplyAnnotationInjections_RefusesAValueNoAnnotationTakes(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"null", `null`, "carries null"},
		{"a list", `["a","b"]`, "carries a list"},
		{"a number", `30`, "carries a number"},
		{"a boolean", `true`, "carries a boolean"},
		{"an object with no subkeys", `{}`, "carries an object with no subkeys"},
		{"a string read as null", `"null"`, "carries a string an annotation reads as null"},
		{"not JSON", `{interval: 30s}`, "not a JSON document"},
		{"nothing at all", ``, "not a JSON document"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := &resolver.ResolveResult{Release: releaseWithComponent("edge", "api")}
			_, targetSet := annotationTargets(rr.Release)
			out := AnnotationTransformOutput{Injections: []AnnotationInjection{
				{BoundaryPath: "edge", ComponentName: "api", AnnotationKey: "feature.x/first", AnnotationValue: json.RawMessage(`"fine"`)},
				{BoundaryPath: "edge", ComponentName: "api", AnnotationKey: "feature.x/k", AnnotationValue: json.RawMessage(tc.value)},
			}}
			err := applyAnnotationInjections(rr, out, targetSet, "proof://x")
			if err == nil {
				t.Fatalf("an injection carrying %s: expected a halt, got nil", tc.name)
			}
			if !errors.Is(err, ErrAnnotationInjectionRejected()) {
				t.Fatalf("halt not errors.Is ErrAnnotationInjectionRejected: %v", err)
			}
			for _, part := range []string{`"edge"`, `"api"`, `"feature.x/k"`, tc.want} {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("refusal does not name %s: %v", part, err)
				}
			}
			if _, ok := componentAnnotation(t, rr, "feature.x/first"); ok {
				t.Errorf("a refused output applied an injection before the refused one")
			}
			if len(rr.AnnotationProvenance) != 0 {
				t.Errorf("a refused output recorded provenance: %+v", rr.AnnotationProvenance)
			}
		})
	}
}

// TestAnnotationTargets_CarryEveryValueShape proves a target carries every
// annotation written on the component — a string, a structured value in its
// written subkey order, and the null of an opt-out — so a plugin can see each key
// it would otherwise clobber.
func TestAnnotationTargets_CarryEveryValueShape(t *testing.T) {
	var written types.OrderedMap[*types.AnnotationValue]
	if err := yaml.Unmarshal([]byte("zz/scalar: v\naa/structured:\n  z: 1\n  a: two\nmm/opted-out: null\n"), &written); err != nil {
		t.Fatalf("decode the authored annotations: %v", err)
	}
	comp := types.BoundaryComponent{Annotations: written}
	b := types.Boundary{}
	b.Components.Set("api", comp)
	rel := &types.Release{}
	rel.Spec.Boundaries.Set("edge", b)

	targets, _ := annotationTargets(rel)
	if len(targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(targets))
	}
	got, err := json.Marshal(targets[0].Annotations)
	if err != nil {
		t.Fatalf("encode the projected annotations: %v", err)
	}
	if want := `{"zz/scalar":"v","aa/structured":{"z":1,"a":"two"},"mm/opted-out":null}`; string(got) != want {
		t.Errorf("projected annotations = %s, want %s", got, want)
	}
}

// TestApplyAnnotationInjections_RenderProjectionCarriesScalarsOnly proves the
// renderer-facing projection treats an injected value as it treats an authored
// one: a scalar is set, and a structured value leaves no scalar behind under its
// key, removing one an earlier write put there.
func TestApplyAnnotationInjections_RenderProjectionCarriesScalarsOnly(t *testing.T) {
	rr := &resolver.ResolveResult{
		Release:       releaseWithComponent("edge", "api"),
		BoundaryInfra: map[string]*resolver.BoundaryInfra{"api": {}},
	}
	rr.BoundaryInfra["api"].Annotations.Set("feature.x/k", "inherited")
	_, targetSet := annotationTargets(rr.Release)
	out := AnnotationTransformOutput{Injections: []AnnotationInjection{
		{BoundaryPath: "edge", ComponentName: "api", AnnotationKey: "feature.x/k", AnnotationValue: json.RawMessage(`{"a":"1"}`)},
		{BoundaryPath: "edge", ComponentName: "api", AnnotationKey: "feature.x/s", AnnotationValue: json.RawMessage(`"v"`)},
	}}
	if err := applyAnnotationInjections(rr, out, targetSet, "proof://x"); err != nil {
		t.Fatalf("unexpected halt: %v", err)
	}
	proj := rr.BoundaryInfra["api"].Annotations
	if v, ok := proj.Get("feature.x/k"); ok {
		t.Errorf("a structured injection left a scalar %q in the render projection", v)
	}
	if v, ok := proj.Get("feature.x/s"); !ok || v != "v" {
		t.Errorf("a scalar injection did not reach the render projection: %q (present=%v)", v, ok)
	}
}
