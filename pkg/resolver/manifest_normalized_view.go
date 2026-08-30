// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// manifestNormalizedView presents a Manifest's annotation binding registry
// alongside the catalog, as the named facts complete once the catalog is
// reachable.
//
// It satisfies [constraint.NormalizedView], and the compiler holds it to that
// contract at the bottom of this file rather than a comment claiming it.
//
// # Why this presentation exists at all
//
// A rule over an annotation binding's target reads two things that are complete
// at different points: the binding's own declarations, which the document carries
// as it is handed over, and the contract of the Feature it routes to, which is
// only readable once the catalog can be reached. The second is what places such a
// rule after the catalog becomes reachable. The presentation the resolve builds
// for that point holds the application and deployment documents and answers no
// question about a Manifest — it refuses them, deliberately, because an empty
// answer there would be indistinguishable from a Manifest whose registry declares
// nothing. So a rule about a binding needs a presentation of its own, exactly as
// the document stage needs one presentation per authored document.
//
// # The later facts are embedded, not re-projected
//
// Every fact this value does not itself hold is answered by the presentation the
// resolve already built, embedded and delegated to. One fact has one producer,
// and two producers of a fact are free to disagree about it with nothing
// comparing them.
//
// The binding facts are delegated too, to the document presentation that already
// states them, rather than projected a second time from the same registry here.
// That keeps one reading of the registry: a second projection agreeing today is
// what hides the divergence the day either moves.
//
// # The bound Features' contracts resolve here, once
//
// The contract of each Feature a binding targets is resolved when this value is
// built, once per Feature, and a resolution that failed is kept with the failure
// that produced it. That is the same discipline the resolve applies to component
// contracts and it exists for the same reason: a rule reading a bare absence
// cannot tell a Feature declaring no contract from one whose contract could not
// be resolved, so the failure is reported where the resolving happened rather
// than inferred by every reader.
type manifestNormalizedView struct {
	constraint.NormalizedView

	// bindings is the document presentation of the Manifest this value was built
	// for. The binding facts are answered by delegating to it.
	bindings manifestDocumentView

	// targetContracts is the resolved contract of each Feature a binding targets,
	// produced once when this value was built.
	targetContracts map[string]resolvedContract
}

// newManifestNormalizedView presents the Manifest a caller is holding together
// with the facts the resolve's own presentation states, resolving the contract of
// every Feature the Manifest's bindings target.
//
// The later presentation is taken rather than reached for, because a view
// projects facts already produced. What this value does produce is the resolution
// of the bound Features' contracts, and that production is the point of the type.
func newManifestNormalizedView(
	path string,
	manifest *types.Manifest,
	later constraint.NormalizedView,
	catalog CatalogLookup,
) *manifestNormalizedView {
	v := &manifestNormalizedView{
		NormalizedView:  later,
		bindings:        newManifestDocumentView(path, manifest),
		targetContracts: map[string]resolvedContract{},
	}
	for _, key := range v.bindings.ManifestAnnotationBindingKeys(path) {
		featureType, declared := v.bindings.ManifestAnnotationBindingFeature(path, key)
		if !declared {
			continue
		}
		if _, seen := v.targetContracts[featureType]; seen {
			continue
		}
		schema, hasContract, err := compileFeatureSchema(catalog, featureType)
		v.targetContracts[featureType] = resolvedContract{
			schema: schema, declared: hasContract, err: err,
		}
	}
	return v
}

// ManifestAnnotationBindingKeys returns the annotation keys the presented
// Manifest's registry declares, in author order.
func (v *manifestNormalizedView) ManifestAnnotationBindingKeys(document string) []string {
	return v.bindings.ManifestAnnotationBindingKeys(document)
}

// ManifestAnnotationBindingFeature returns the Feature the named binding routes
// to, and whether the presented Manifest declares a binding of that key at all.
func (v *manifestNormalizedView) ManifestAnnotationBindingFeature(
	document string, binding string,
) (string, bool) {
	return v.bindings.ManifestAnnotationBindingFeature(document, binding)
}

// ManifestAnnotationBindingRoutes returns the routing slots the named binding
// claims on the Feature it targets, in author order.
func (v *manifestNormalizedView) ManifestAnnotationBindingRoutes(
	document string, binding string,
) []constraint.AnnotationBindingRoute {
	return v.bindings.ManifestAnnotationBindingRoutes(document, binding)
}

// CompiledFeatureSchema returns the contract of a Feature a binding targets,
// resolved, and whether there was a contract that resolved at all.
//
// It answers from the resolution this value performed when it was built, so every
// rule invoked over this presentation reads one result. A Feature no binding
// targets is not in that set and answers as absent — which is correct here: the
// facts this presentation states are about the bindings the Manifest declares,
// and a Feature outside that set is not one of its subjects.
func (v *manifestNormalizedView) CompiledFeatureSchema(featureType string) (map[string]any, bool) {
	return v.targetContracts[featureType].resolved()
}

// unresolvableTargets names each Feature a binding targets whose declared
// contract could not be resolved, paired with the failure, in Feature-type order.
//
// It is how the third resolution outcome leaves this value, and it is a method
// rather than a declared fact for the reason its counterpart on the resolve's own
// presentation is one: a rule reads the outcome and never produces it, so the
// failure is reported by the caller that holds the resolution, once, rather than
// raised again by every rule that met the absence.
func (v *manifestNormalizedView) unresolvableTargets() []FeatureContractResolutionFailure {
	var out []FeatureContractResolutionFailure
	for _, featureType := range sortedKeysOf(v.targetContracts) {
		c := v.targetContracts[featureType]
		if c.declared && c.err != nil {
			out = append(out, FeatureContractResolutionFailure{FeatureType: featureType, Err: c.err})
		}
	}
	return out
}

// FeatureContractResolutionFailure is one Feature contract that could not be
// resolved: the Feature type declaring it, and why it did not resolve.
//
// It is the value a finding is composed from, not the finding itself. Keeping the
// two apart is what lets one classification serve a diagnostic without the
// resolution deciding the verdict.
type FeatureContractResolutionFailure struct {
	// FeatureType is the Feature whose contract did not resolve.
	FeatureType string

	// Err is why the contract did not resolve.
	Err error
}

// The compiler holds this type to the contract rather than a comment claiming it:
// a signature drifting from the declared facts stops compiling here.
var _ constraint.NormalizedView = (*manifestNormalizedView)(nil)
