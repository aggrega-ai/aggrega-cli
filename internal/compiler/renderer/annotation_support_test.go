// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// annotationLookup builds a provider-Plugin lookup over one provider name whose
// support claim is the given entries, so a test states a claim the way a provider
// entity states it rather than restating a support table.
func annotationLookup(provider string, entries ...types.WellKnownAnnotation) func(string) (types.Plugin, bool, error) {
	return func(ref string) (types.Plugin, bool, error) {
		if ref != provider {
			return types.Plugin{}, false, nil
		}
		return types.Plugin{Spec: types.PluginSpec{
			Provider: &types.PluginProvider{WellKnownAnnotations: entries},
		}}, true, nil
	}
}

func annotations(t *testing.T, kv ...string) types.OrderedMap[string] {
	t.Helper()
	var m types.OrderedMap[string]
	for i := 0; i+1 < len(kv); i += 2 {
		m.Set(kv[i], kv[i+1])
	}
	return m
}

func collect() (func(Diagnostic), *[]Diagnostic) {
	var got []Diagnostic
	return func(d Diagnostic) { got = append(got, d) }, &got
}

// TestWarnUnhonorableAnnotations_AdvisesOnUncoveredKey proves the advisory fires
// for a platform annotation the target's support claim does not cover on the
// node's input kind, names the key, the target and the input kind, carries the
// shared class code so the existing suppression flag covers it, and is attributed
// to the node and target.
func TestWarnUnhonorableAnnotations_AdvisesOnUncoveredKey(t *testing.T) {
	t.Parallel()
	sink, got := collect()
	// The claim covers the key on synthesized input only; the node is sourced.
	lookup := annotationLookup("helm", types.WellKnownAnnotation{
		Key:   "aggrega.ai/image-registry",
		Scope: []string{types.WellKnownAnnotationScopeSynthesized},
	})
	kind, ok := classifyNodeInputKind(true, false, false)
	if !ok {
		t.Fatal("classifyNodeInputKind(hasSource): ok=false; want the sourced classification")
	}

	warnUnhonorableAnnotations("api", "helm", "helm", kind,
		annotations(t, "aggrega.ai/image-registry", "registry.example.com"), lookup, sink)

	if len(*got) != 1 {
		t.Fatalf("advisory count = %d, want 1: %+v", len(*got), *got)
	}
	d := (*got)[0]
	if d.Code != UnsupportedFieldCode {
		t.Errorf("code = %q, want the shared class code %q so one suppression flag covers it", d.Code, UnsupportedFieldCode)
	}
	if d.SourceRef != "aggrega.ai/image-registry" {
		t.Errorf("SourceRef = %q, want the authored key", d.SourceRef)
	}
	for _, want := range []string{"aggrega.ai/image-registry", `"helm"`, "sourced"} {
		if !strings.Contains(d.Message, want) {
			t.Errorf("message %q does not name %s", d.Message, want)
		}
	}
	if d.Location == nil || d.Location.URI != "api" || d.Location.Kind != "helm" {
		t.Errorf("advisory not attributed to node/target: %+v", d.Location)
	}
}

// TestWarnUnhonorableAnnotations_SubPrefixKeyNeverAdvises proves a sub-prefixed
// key is never considered: it is authored vocabulary the compiler passes through
// by design. The same call is repeated with the sub-prefix stripped, so the
// silence is shown to be a decision about the key rather than an inert check —
// a negative that could not fire would prove nothing.
func TestWarnUnhonorableAnnotations_SubPrefixKeyNeverAdvises(t *testing.T) {
	t.Parallel()
	// A provider that HAS declared a claim, covering one unrelated key: the
	// comparison is live and every other key is uncovered, so the only thing that
	// can keep a key silent here is the prefix test. A provider declaring nothing
	// would make the whole comparison stand down and the silence prove nothing.
	lookup := annotationLookup("helm", types.WellKnownAnnotation{
		Key: "aggrega.ai/image-registry", Scope: []string{types.WellKnownAnnotationScopeSynthesized}})
	kind, _ := classifyNodeInputKind(true, false, false)

	sink, got := collect()
	warnUnhonorableAnnotations("api", "helm", "helm", kind, annotations(t,
		"compliance.aggrega.ai/data-class", "restricted",
		"component.aggrega.ai/type", "service",
		"acme.aggrega.ai/team", "payments",
	), lookup, sink)
	if len(*got) != 0 {
		t.Fatalf("sub-prefixed keys produced %d advisories, want 0: %+v", len(*got), *got)
	}

	// Calibration: the instrument fires on the bare-prefix spelling of the same
	// concept, so the zero above is the prefix rule and not a dead check.
	calibrationSink, calibration := collect()
	warnUnhonorableAnnotations("api", "helm", "helm", kind,
		annotations(t, "aggrega.ai/data-class", "restricted"), lookup, calibrationSink)
	if len(*calibration) != 1 {
		t.Fatalf("calibration: bare-prefix key produced %d advisories, want 1 — the check is inert, so the zero above proves nothing", len(*calibration))
	}
}

// TestWarnUnhonorableAnnotations_CoveringClaimsStaySilent proves the three shapes
// of claim that cover a key: the node's own input kind, the boundary scope (a key
// resolved once per boundary is honoured for every node beneath it, whatever each
// renders), and an entry with no scope (which makes no per-path claim to
// contradict).
func TestWarnUnhonorableAnnotations_CoveringClaimsStaySilent(t *testing.T) {
	t.Parallel()
	kind, _ := classifyNodeInputKind(true, false, false)
	for _, tc := range []struct {
		name  string
		entry types.WellKnownAnnotation
	}{
		{"the node's own input kind", types.WellKnownAnnotation{
			Key: "aggrega.ai/namespace", Scope: []string{types.WellKnownAnnotationScopeSourced}}},
		{"the boundary scope", types.WellKnownAnnotation{
			Key: "aggrega.ai/chart-name", Scope: []string{types.WellKnownAnnotationScopeBoundary}}},
		{"no scope at all", types.WellKnownAnnotation{Key: "aggrega.ai/namespace"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sink, got := collect()
			warnUnhonorableAnnotations("api", "helm-chart", "helm-chart", kind,
				annotations(t, tc.entry.Key, "value"), annotationLookup("helm-chart", tc.entry), sink)
			if len(*got) != 0 {
				t.Fatalf("a claim covering the key produced %d advisories, want 0: %+v", len(*got), *got)
			}
		})
	}
}

// TestWarnUnhonorableAnnotations_PerNodeAndAuthorOrdered proves one authored key
// set produces one advisory per uncovered key, in the order the deployment
// authored them, so the stream is byte-stable across runs of identical input.
func TestWarnUnhonorableAnnotations_PerNodeAndAuthorOrdered(t *testing.T) {
	t.Parallel()
	lookup := annotationLookup("helm-chart", types.WellKnownAnnotation{
		Key: "aggrega.ai/chart-name", Scope: []string{types.WellKnownAnnotationScopeBoundary}})
	kind, _ := classifyNodeInputKind(true, false, false)

	sink, got := collect()
	warnUnhonorableAnnotations("api", "helm-chart", "helm-chart", kind, annotations(t,
		"aggrega.ai/service-account", "api-runner",
		"aggrega.ai/chart-name", "platform",
		"aggrega.ai/namespace", "prod",
		"aggrega.ai/image-registry", "registry.example.com",
	), lookup, sink)

	want := []string{"aggrega.ai/service-account", "aggrega.ai/namespace", "aggrega.ai/image-registry"}
	if len(*got) != len(want) {
		t.Fatalf("advisory count = %d, want %d: %+v", len(*got), len(want), *got)
	}
	for i, key := range want {
		if (*got)[i].SourceRef != key {
			t.Errorf("advisory %d = %q, want %q — author order is not preserved", i, (*got)[i].SourceRef, key)
		}
	}
}

// TestWarnUnhonorableAnnotations_AbsentClaimStaysSilent proves the shape most
// easily got backwards: a provider that declares NO support claim is not the same
// as one whose claim omits the key. It has said nothing, so reporting a refusal
// would state a claim its author never made. The positive control is the same
// call against a provider that HAS declared a claim omitting the same key, which
// does advise — so this silence is the absent-claim rule and not an inert check.
func TestWarnUnhonorableAnnotations_AbsentClaimStaysSilent(t *testing.T) {
	t.Parallel()
	sourced, _ := classifyNodeInputKind(true, false, false)
	authored := annotations(t, "aggrega.ai/namespace", "prod")

	sink, got := collect()
	warnUnhonorableAnnotations("api", "quiet", "quiet", sourced, authored,
		annotationLookup("quiet"), sink)
	if len(*got) != 0 {
		t.Errorf("a provider declaring no support claim advised %d times, want 0: %+v", len(*got), *got)
	}

	// Control: a declared claim that omits the same key does advise.
	controlSink, control := collect()
	warnUnhonorableAnnotations("api", "declares", "declares", sourced, authored,
		annotationLookup("declares", types.WellKnownAnnotation{
			Key: "aggrega.ai/chart-name", Scope: []string{types.WellKnownAnnotationScopeBoundary}}), controlSink)
	if len(*control) != 1 {
		t.Fatalf("control: a declared claim omitting the key advised %d times, want 1 — the check is inert, so the silence above proves nothing", len(*control))
	}
}

// TestWarnUnhonorableAnnotations_NothingToCompareStaysSilent proves each shape
// that leaves nothing to compare emits nothing rather than guessing: no sink, no
// lookup, no resolving entity, no annotations, and a node classifying to no input
// kind.
func TestWarnUnhonorableAnnotations_NothingToCompareStaysSilent(t *testing.T) {
	t.Parallel()
	// A provider with a real claim, so each silence below is the named condition
	// rather than the absent-claim rule covered by its own test.
	lookup := annotationLookup("helm", types.WellKnownAnnotation{
		Key: "aggrega.ai/chart-name", Scope: []string{types.WellKnownAnnotationScopeBoundary}})
	sourced, _ := classifyNodeInputKind(true, false, false)
	authored := annotations(t, "aggrega.ai/namespace", "prod")

	// No sink: the call must not panic.
	warnUnhonorableAnnotations("api", "helm", "helm", sourced, authored, lookup, nil)

	sink, got := collect()
	warnUnhonorableAnnotations("api", "helm", "helm", sourced, authored, nil, sink)
	if len(*got) != 0 {
		t.Errorf("nil lookup emitted %d advisories, want 0", len(*got))
	}

	sink, got = collect()
	warnUnhonorableAnnotations("api", "no-such-provider", "no-such-provider", sourced, authored, lookup, sink)
	if len(*got) != 0 {
		t.Errorf("unresolving provider emitted %d advisories, want 0", len(*got))
	}

	sink, got = collect()
	warnUnhonorableAnnotations("api", "helm", "helm", sourced, types.OrderedMap[string]{}, lookup, sink)
	if len(*got) != 0 {
		t.Errorf("no annotations emitted %d advisories, want 0", len(*got))
	}

	noKind, ok := classifyNodeInputKind(false, false, false)
	if ok {
		t.Fatal("classifyNodeInputKind(none): ok=true; a node declaring no input classifies to none")
	}
	sink, got = collect()
	warnUnhonorableAnnotations("api", "helm", "helm", noKind, authored, lookup, sink)
	if len(*got) != 0 {
		t.Errorf("unclassified node emitted %d advisories, want 0", len(*got))
	}
}

// TestClassifyNodeInputKind_Precedence pins the source > workload > artifacts
// precedence and the scope/word pairing that the capability gate and the
// annotation advisory both read, so neither can drift into a second vocabulary.
func TestClassifyNodeInputKind_Precedence(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name                        string
		source, workload, artifacts bool
		wantScope, wantDisplay      string
		wantOK                      bool
	}{
		{"source wins over workload and artifacts", true, true, true,
			types.WellKnownAnnotationScopeSourced, "sourced", true},
		{"workload wins over artifacts", false, true, true,
			types.WellKnownAnnotationScopeSynthesized, "synthesised", true},
		{"artifacts only when neither", false, false, true,
			types.WellKnownAnnotationScopeArtifactsStandalone, "artifacts-only", true},
		{"none", false, false, false, "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			kind, ok := classifyNodeInputKind(tc.source, tc.workload, tc.artifacts)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if kind.Scope != tc.wantScope || kind.Display != tc.wantDisplay {
				t.Errorf("got {Scope:%q Display:%q}, want {Scope:%q Display:%q}",
					kind.Scope, kind.Display, tc.wantScope, tc.wantDisplay)
			}
		})
	}
}

// TestNodeInputKind_ClaimedBy pins that each classification reads its own field
// of the provider contract, which is what keeps the capability gate's verdict and
// the annotation advisory's scope derived from one classification.
func TestNodeInputKind_ClaimedBy(t *testing.T) {
	t.Parallel()
	sourced, _ := classifyNodeInputKind(true, false, false)
	synthesized, _ := classifyNodeInputKind(false, true, false)
	artifacts, _ := classifyNodeInputKind(false, false, true)

	only := ProviderContract{Sourced: true}
	if !sourced.claimedBy(only) {
		t.Error("sourced.claimedBy({Sourced:true}) = false, want true")
	}
	if synthesized.claimedBy(only) || artifacts.claimedBy(only) {
		t.Error("a contract claiming only sourced must not claim the other kinds")
	}
	if !synthesized.claimedBy(ProviderContract{Synthesized: true}) {
		t.Error("synthesised.claimedBy({Synthesized:true}) = false, want true")
	}
	if !artifacts.claimedBy(ProviderContract{ArtifactsStandalone: true}) {
		t.Error("artifacts-only.claimedBy({ArtifactsStandalone:true}) = false, want true")
	}
}
